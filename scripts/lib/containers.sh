#!/bin/bash

# ==============================================================================
# CONTAINER OPERATIONS
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/constants.sh"

function detect_common_ports() {
    local ports=()
    
    # Check for common development files and add corresponding ports
    if [[ -f "package.json" ]]; then
        # Node.js common ports
        ports+=(3000 8080 5000 4000)
        
        # Check for specific frameworks in package.json
        if grep -q "next" package.json 2>/dev/null; then
            ports+=(3000)
        fi
        if grep -q "react-scripts" package.json 2>/dev/null; then
            ports+=(3000)
        fi
        if grep -q "vite" package.json 2>/dev/null; then
            ports+=(5173)
        fi
    fi
    
    if [[ -f "requirements.txt" ]] || [[ -f "pyproject.toml" ]] || [[ -f "setup.py" ]]; then
        # Python common ports
        ports+=(8000 5000 8080)
        
        # Check for specific frameworks
        if grep -q "flask" requirements.txt 2>/dev/null || grep -q "Flask" requirements.txt 2>/dev/null; then
            ports+=(5000)
        fi
        if grep -q "django" requirements.txt 2>/dev/null || grep -q "Django" requirements.txt 2>/dev/null; then
            ports+=(8000)
        fi
        if grep -q "fastapi" requirements.txt 2>/dev/null || grep -q "FastAPI" requirements.txt 2>/dev/null; then
            ports+=(8000)
        fi
    fi
    
    if [[ -f "go.mod" ]]; then
        # Go common ports
        ports+=(8080 8000 3000)
    fi
    
    if [[ -f "Cargo.toml" ]]; then
        # Rust common ports
        ports+=(8080 3000 8000)
    fi
    
    if [[ -f "composer.json" ]]; then
        # PHP common ports
        ports+=(8080 8000 80)
    fi
    
    if [[ -f "pom.xml" ]] || [[ -f "build.gradle" ]]; then
        # Java common ports
        ports+=(8080 8000 9000)
    fi
    
    # Remove duplicates using sort and uniq
    if [[ ${#ports[@]} -gt 0 ]]; then
        printf '%s\n' "${ports[@]}" | sort -u | tr '\n' ' '
    fi
}

function get_ssh_key_mounts() {
    local ssh_mounts=""
    
    # Mount SSH keys only if enabled and they exist
    if [[ "$MOUNT_SSH_KEYS" == "true" ]] && [[ -d "$HOME/.ssh" ]]; then
        ssh_mounts="-v $HOME/.ssh:/home/appuser/.ssh:ro"
    fi
    
    echo "$ssh_mounts"
}

function get_gpg_mounts() {
    # GPG agent forwarding is complex and often doesn't work in containers
    # For now, we disable it and let users commit without signing in containers
    echo ""
}

function get_common_volume_mounts() {
    local volumes=""
    
    # Always mount the current directory (read-write for development)
    volumes="-v $(pwd):/workspace"
    
    # Mount git configuration only if enabled and it exists
    if [[ "$MOUNT_GIT_CONFIG" == "true" ]] && [[ -f "$HOME/.gitconfig" ]]; then
        # Create filtered .gitconfig (remove GPG signing for container compatibility)
        local temp_dir="${DEV_CONFIG_DIR}/tmp"
        mkdir -p "$temp_dir"
        local temp_gitconfig="$temp_dir/gitconfig.$$"
        # Remove gpgsign and gpg.program lines
        grep -v "gpgsign" "$HOME/.gitconfig" | grep -v "gpg.program" > "$temp_gitconfig"
        volumes="$volumes -v $temp_gitconfig:/home/appuser/.gitconfig:ro"
    fi
    
    # Mount Docker socket for Docker-in-Docker development (with warning)
    if [[ -S "/var/run/docker.sock" ]]; then
        echo "⚠️  Warning: Mounting Docker socket reduces container isolation" >&2
        volumes="$volumes -v /var/run/docker.sock:/var/run/docker.sock"
    fi
    
    echo "$volumes"
}

function get_resource_limits() {
    local resource_flags=""
    
    # Add memory limit if configured
    if [[ -n "$MEMORY_LIMIT" ]]; then
        resource_flags="$resource_flags --memory=$MEMORY_LIMIT"
    fi
    
    # Add CPU limit if configured
    if [[ -n "$CPU_LIMIT" ]]; then
        resource_flags="$resource_flags --cpus=$CPU_LIMIT"
    fi
    
    echo "$resource_flags"
}

function prepare_and_run_container() {
    local command="$1"
    local action_msg="🚀 Preparing isolated container"
    local ready_msg="✅ Connecting to container"
    local cmd_args=""
    
    # Validate container name and tag before proceeding
    if [[ -n "$CUSTOM_NAME" ]]; then
        if ! validate_container_name "$CUSTOM_NAME"; then
            exit 1
        fi
    fi
    
    if [[ -n "$CUSTOM_TAG" ]]; then
        if ! validate_tag_name "$CUSTOM_TAG"; then
            exit 1
        fi
    fi
    
    if [[ "$command" == "shell" ]]; then
        action_msg="🐚 Opening interactive shell"
        ready_msg="✅ Container ready"
        cmd_args="bash"
    fi
    
    echo "$action_msg for '$PROJECT_NAME'..."
    ensure_vm_running
    platform_flag=$(get_platform_flag "$TARGET_PLATFORM")
    build_image "$platform_flag"
    cleanup_existing_container
    
    # Build enhanced container options
    volume_mounts=$(get_common_volume_mounts)
    ssh_mounts=$(get_ssh_key_mounts)
    gpg_mounts=$(get_gpg_mounts)
    port_forwards=$(build_port_forwards)
    env_forwards=$(get_env_forwards)
    
    if [[ -n "$env_forwards" ]]; then
        echo "🔐 Passing environment variables to container" >&2
    fi
    
    security_flags=$(get_security_flags)
    resource_limits=$(get_resource_limits)
    
    echo "🔧 Enhanced developer experience:"
    if [[ -n "$port_forwards" ]]; then
        echo "   -> Port forwarding enabled for detected services"
    fi
    if [[ -n "$ssh_mounts" ]]; then
        echo "   -> SSH keys mounted for git authentication"
    fi
    if [[ "$MOUNT_GIT_CONFIG" == "true" ]] && [[ -f "$HOME/.gitconfig" ]]; then
        echo "   -> Git configuration mounted for consistent commits"
    fi
    if [[ -n "$gpg_mounts" ]]; then
        echo "   -> GPG agent forwarded for commit signing"
    fi
    echo "   -> Security hardening enabled (non-root user, limited capabilities)"
    if [[ -n "$resource_limits" ]]; then
        echo "   -> Resource limits applied: $resource_limits"
    fi
    echo ""
    echo "$ready_msg. Your project folder is at '/workspace'."
    
    # shellcheck disable=SC2086  # Intentionally unquoted for multiple flags
    if ! orb -m "$VM_NAME" sudo docker run -it --rm \
        $security_flags \
        $volume_mounts \
        $ssh_mounts \
        $gpg_mounts \
        $port_forwards \
        $env_forwards \
        $resource_limits \
        --name "$CONTAINER_NAME" \
        "$IMAGE_NAME" $cmd_args; then
        
        # Check if it was a port conflict
        if orb -m "$VM_NAME" sudo docker logs "$CONTAINER_NAME" 2>&1 | grep -q "port is already allocated"; then
            echo ""
            echo "❌ Error: Port conflict detected"
            echo "   One or more ports are already in use on your system."
            echo ""
            echo "   Solutions:"
            echo "   1. Stop the conflicting service using the port"
            echo "   2. Find what's using the port: lsof -i :<port>"
            echo "   3. Manually specify different ports in your Dockerfile"
            echo "   4. Use host networking: DEV_NETWORK_MODE=host dev"
        fi
        exit 1
    fi
}

function check_port_available() {
    local port="$1"
    if lsof -Pi :"$port" -sTCP:LISTEN -t >/dev/null 2>&1 ; then
        return 1
    fi
    return 0
}

function get_env_forwards() {
    local env_args=""
    
    # Process custom env vars from --env flags
    if [[ ${#CUSTOM_ENV_VARS[@]} -gt 0 ]]; then
        for env_spec in "${CUSTOM_ENV_VARS[@]}"; do
            # Handle both VAR=value and VAR formats
            if [[ "$env_spec" == *"="* ]]; then
                # Direct value: VAR=value
                env_args="$env_args -e $env_spec"
            else
                # Variable name only: VAR (get value from environment)
                if [[ -n "${!env_spec:-}" ]]; then
                    local value="${!env_spec}"
                    env_args="$env_args -e $env_spec='$value'"
                fi
            fi
        done
    fi
    
    # Process custom env files from --env-file flags
    if [[ ${#CUSTOM_ENV_FILES[@]} -gt 0 ]]; then
        for env_file in "${CUSTOM_ENV_FILES[@]}"; do
            if [[ -f "$env_file" ]]; then
                env_args="$env_args --env-file $env_file"
            else
                echo "⚠️  Warning: Environment file not found: $env_file" >&2
            fi
        done
    fi
    
    # Get patterns from config
    local patterns=$(get_config_array "pass_env_vars.patterns")
    local explicit=$(get_config_array "pass_env_vars.explicit")
    
    # Process pattern-based variables
    if [[ -n "$patterns" ]]; then
        while IFS= read -r pattern; do
            [[ -z "$pattern" ]] && continue
            
            # Handle wildcard patterns
            if [[ "$pattern" == *"*" ]]; then
                local prefix="${pattern%\*}"
                while IFS='=' read -r var _; do
                    [[ "$var" == "$prefix"* ]] || continue
                    local value="${!var}"
                    [[ -z "$value" ]] && continue
                    env_args="$env_args -e $var='$value'"
                done < <(env)
            else
                # Exact match - check if variable is set
                if [[ -n "${!pattern:-}" ]]; then
                    local value="${!pattern}"
                    env_args="$env_args -e $pattern='$value'"
                fi
            fi
        done <<< "$patterns"
    fi
    
    # Process explicit variables
    if [[ -n "$explicit" ]]; then
        while IFS= read -r var; do
            [[ -z "$var" ]] && continue
            if [[ -n "${!var:-}" ]]; then
                local value="${!var}"
                env_args="$env_args -e $var='$value'"
            fi
        done <<< "$explicit"
    fi
    
    echo "$env_args"
}

function build_port_forwards() {
    local ports=($(detect_common_ports))
    local port_args=""
    local unavailable_ports=()
    
    if [[ ${#ports[@]} -gt 0 ]]; then
        echo "🔌 Detected common development ports: ${ports[*]}" >&2
        
        for port in "${ports[@]}"; do
            if check_port_available "$port"; then
                port_args="$port_args -p $port:$port"
            else
                unavailable_ports+=("$port")
            fi
        done
        
        if [[ ${#unavailable_ports[@]} -gt 0 ]]; then
            echo "⚠️  Warning: Ports already in use (skipped): ${unavailable_ports[*]}" >&2
            echo "   To use these ports, stop the conflicting services or use different ports" >&2
        fi
    fi
    
    echo "$port_args"
}

function ensure_vm_running() {
    if ! orb status "$VM_NAME" 2>/dev/null | grep -q "running"; then
        if [[ "$AUTO_START_VM" == "true" ]]; then
            echo "   -> Starting VM '$VM_NAME'..."
            orb start "$VM_NAME"
        else
            echo "❌ Error: VM '$VM_NAME' is not running"
            echo "   Start it manually with: dev env up docker-host"
            echo "   Or set auto_start_vm=true in your config"
            exit 1
        fi
    fi
}

function build_image() {
    local platform_flag="$1"
    local current_arch=$(detect_architecture)
    echo "   -> Building Docker image '$IMAGE_NAME'..."
    echo "   -> Host architecture: $current_arch"
    
    local build_result
    if [[ -n "$platform_flag" ]]; then
        echo "   -> Target platform: ${platform_flag#--platform }"
        orb -m "$VM_NAME" sudo docker build $platform_flag -f "$DOCKERFILE" -t "$IMAGE_NAME" .
        build_result=$?
    else
        echo "   -> Target platform: auto-detected (linux/$current_arch)"
        orb -m "$VM_NAME" sudo docker build -f "$DOCKERFILE" -t "$IMAGE_NAME" .
        build_result=$?
    fi
    
    if [[ $build_result -ne 0 ]]; then
        echo "❌ Error: Docker build failed"
        echo "   Check your Dockerfile and requirements files"
        exit 1
    fi
}

function cleanup_existing_container() {
    if orb -m "$VM_NAME" sudo docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
        echo "   -> Stopping existing container..."
        orb -m "$VM_NAME" sudo docker stop "$CONTAINER_NAME" >/dev/null 2>&1
        orb -m "$VM_NAME" sudo docker rm "$CONTAINER_NAME" >/dev/null 2>&1
    fi
}