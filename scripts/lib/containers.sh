#!/bin/bash

# ==============================================================================
# CONTAINER OPERATIONS
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/constants.sh"
source "$SCRIPT_DIR/config.sh"

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

# Remove stale filtered-gitconfig copies left behind by previous runs.
function cleanup_stale_gitconfig_tmp() {
    local temp_dir="${DEV_CONFIG_DIR}/tmp"
    [[ -d "$temp_dir" ]] || return 0
    # Anything older than a day is definitely not backing a live container
    find "$temp_dir" -name 'gitconfig.*' -type f -mmin +1440 -delete 2>/dev/null
}

# Populate DEV_VOLUME_ARGS with docker -v arguments as a real argument array.
# Using an array (not a flat string) keeps paths with spaces intact.
DEV_VOLUME_ARGS=()
function build_volume_mounts() {
    DEV_VOLUME_ARGS=()

    # Always mount the current directory (read-write for development)
    DEV_VOLUME_ARGS+=(-v "$(pwd):/workspace")

    # Mount SSH keys only if enabled and they exist
    if [[ "$MOUNT_SSH_KEYS" == "true" ]] && [[ -d "$HOME/.ssh" ]]; then
        DEV_VOLUME_ARGS+=(-v "$HOME/.ssh:/home/appuser/.ssh:ro")
    fi

    # Mount git configuration only if enabled and it exists
    if [[ "$MOUNT_GIT_CONFIG" == "true" ]] && [[ -f "$HOME/.gitconfig" ]]; then
        # Create filtered .gitconfig (remove GPG signing for container compatibility)
        local temp_dir="${DEV_CONFIG_DIR}/tmp"
        mkdir -p "$temp_dir"
        cleanup_stale_gitconfig_tmp
        local temp_gitconfig="$temp_dir/gitconfig.$$"
        # Remove gpgsign and gpg.program lines
        grep -v "gpgsign" "$HOME/.gitconfig" | grep -v "gpg.program" > "$temp_gitconfig"
        DEV_VOLUME_ARGS+=(-v "$temp_gitconfig:/home/appuser/.gitconfig:ro")
    fi

    # Mount Docker socket only when explicitly enabled (security: opt-in).
    # Access to the Docker socket is equivalent to root on the Docker host
    # and defeats container isolation, so this must never be automatic.
    if [[ "${MOUNT_DOCKER_SOCKET:-false}" == "true" ]]; then
        if [[ -S "/var/run/docker.sock" ]]; then
            echo "⚠️  Warning: Docker socket mounted - container can control the Docker host" >&2
            DEV_VOLUME_ARGS+=(-v "/var/run/docker.sock:/var/run/docker.sock")
        else
            echo "⚠️  Warning: mount_docker_socket enabled but /var/run/docker.sock not found" >&2
        fi
    fi
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
    local cmd_args=()

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
        cmd_args=(bash)
    fi

    # Optional command pass-through: dev shell -c "cmd" / dev run -c "cmd"
    if [[ -n "${SHELL_COMMAND:-}" ]]; then
        cmd_args=(bash -c "$SHELL_COMMAND")
    fi

    echo "$action_msg for '$PROJECT_NAME'..."
    ensure_vm_running
    platform_flag=$(get_platform_flag "$TARGET_PLATFORM")
    build_image "$platform_flag"
    cleanup_existing_container

    # Build enhanced container options
    build_volume_mounts
    build_env_forwards
    port_forwards=$(build_port_forwards)

    if [[ ${#DEV_ENV_ARGS[@]} -gt 0 ]]; then
        echo "🔐 Passing environment variables to container" >&2
    fi

    security_flags=$(get_security_flags)
    resource_limits=$(get_resource_limits)

    echo "🔧 Enhanced developer experience:"
    if [[ -n "$port_forwards" ]]; then
        echo "   -> Port forwarding enabled for detected services"
    fi
    if [[ "$MOUNT_SSH_KEYS" == "true" ]] && [[ -d "$HOME/.ssh" ]]; then
        echo "   -> SSH keys mounted for git authentication"
    fi
    if [[ "$MOUNT_GIT_CONFIG" == "true" ]] && [[ -f "$HOME/.gitconfig" ]]; then
        echo "   -> Git configuration mounted for consistent commits"
    fi
    echo "   -> Security hardening enabled (non-root user, limited capabilities)"
    if [[ -n "$resource_limits" ]]; then
        echo "   -> Resource limits applied: $resource_limits"
    fi
    echo ""
    echo "$ready_msg. Your project folder is at '/workspace'."

    # Build command array for proper argument handling.
    # Flag groups that can contain user-controlled values (volumes, env vars)
    # are built as arrays; groups that are known to be space-free tokens
    # (security flags, ports, resource limits) are split with read -ra.
    local cmd_array=(orb -m "$VM_NAME" sudo docker run -it --rm)
    local -a split_flags=()

    if [[ -n "$security_flags" ]]; then
        read -ra split_flags <<< "$security_flags"
        cmd_array+=("${split_flags[@]}")
    fi

    if [[ ${#DEV_VOLUME_ARGS[@]} -gt 0 ]]; then
        cmd_array+=("${DEV_VOLUME_ARGS[@]}")
    fi

    if [[ -n "$port_forwards" ]]; then
        read -ra split_flags <<< "$port_forwards"
        cmd_array+=("${split_flags[@]}")
    fi

    if [[ ${#DEV_ENV_ARGS[@]} -gt 0 ]]; then
        cmd_array+=("${DEV_ENV_ARGS[@]}")
    fi

    if [[ -n "$resource_limits" ]]; then
        read -ra split_flags <<< "$resource_limits"
        cmd_array+=("${split_flags[@]}")
    fi

    cmd_array+=(--name "$CONTAINER_NAME" "$IMAGE_NAME")
    if [[ ${#cmd_args[@]} -gt 0 ]]; then
        cmd_array+=("${cmd_args[@]}")
    fi

    # Execute with proper argument handling
    if ! "${cmd_array[@]}"; then
        echo ""
        echo "❌ Error: Container exited with a failure"
        echo "   If this looks like a port conflict:"
        echo "   1. Find what's using the port: lsof -i :<port>"
        echo "   2. Stop the conflicting service, or"
        echo "   3. Override forwarded ports: DEV_FORWARD_PORTS=\"<ports>\" dev"
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

# Populate DEV_ENV_ARGS with docker env arguments as a real argument array
# (pairs of "-e" "VAR=value" or "--env-file" "path"). Values with spaces
# survive because they are never re-split by the shell.
DEV_ENV_ARGS=()
function build_env_forwards() {
    DEV_ENV_ARGS=()

    if [[ ${#CUSTOM_ENV_VARS[@]} -gt 0 ]]; then
        for env_spec in "${CUSTOM_ENV_VARS[@]}"; do
            if [[ "$env_spec" == *"="* ]]; then
                DEV_ENV_ARGS+=(-e "$env_spec")
            else
                if [[ -n "${!env_spec:-}" ]]; then
                    DEV_ENV_ARGS+=(-e "$env_spec=${!env_spec}")
                fi
            fi
        done
    fi

    if [[ ${#CUSTOM_ENV_FILES[@]} -gt 0 ]]; then
        for env_file in "${CUSTOM_ENV_FILES[@]}"; do
            if [[ -f "$env_file" ]]; then
                DEV_ENV_ARGS+=(--env-file "$env_file")
            else
                echo "⚠️  Warning: Environment file not found: $env_file" >&2
            fi
        done
    fi

    local patterns=$(get_config_array "pass_env_vars.patterns")
    local explicit=$(get_config_array "pass_env_vars.explicit")

    if [[ -n "$patterns" ]]; then
        while IFS= read -r pattern; do
            [[ -z "$pattern" ]] && continue

            if [[ "$pattern" == *"*" ]]; then
                local prefix="${pattern%\*}"
                [[ -z "$prefix" ]] && continue
                while IFS='=' read -r var _; do
                    # Only accept well-formed variable names; skip continuation
                    # lines from multi-line values in env output
                    [[ "$var" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
                    [[ "$var" == "$prefix"* ]] || continue
                    local value="${!var:-}"
                    [[ -z "$value" ]] && continue
                    DEV_ENV_ARGS+=(-e "$var=$value")
                done < <(env)
            else
                if [[ -n "${!pattern:-}" ]]; then
                    DEV_ENV_ARGS+=(-e "$pattern=${!pattern}")
                fi
            fi
        done <<< "$patterns"
    fi

    if [[ -n "$explicit" ]]; then
        while IFS= read -r var; do
            [[ -z "$var" ]] && continue
            if [[ -n "${!var:-}" ]]; then
                DEV_ENV_ARGS+=(-e "$var=${!var}")
            fi
        done <<< "$explicit"
    fi
}

# String view of build_env_forwards for display and tests: one flag pair per
# line ("-e VAR=value" / "--env-file path"). Do NOT word-split this output to
# build a command; use DEV_ENV_ARGS instead.
function get_env_forwards() {
    build_env_forwards
    local i=0
    while [[ $i -lt ${#DEV_ENV_ARGS[@]} ]]; do
        echo "${DEV_ENV_ARGS[$i]} ${DEV_ENV_ARGS[$((i+1))]}"
        i=$((i+2))
    done
}

function build_port_forwards() {
    local forward_ports_config
    forward_ports_config=$(get_config_value "forward_ports")

    local ports=()
    if [[ -n "$forward_ports_config" ]]; then
        IFS=',' read -ra ports <<< "$forward_ports_config"
        echo "🔌 Using configured ports: ${ports[*]}" >&2
    else
        ports=($(detect_common_ports))
        if [[ ${#ports[@]} -gt 0 ]]; then
            echo "🔌 Detected common development ports: ${ports[*]}" >&2
        fi
    fi

    local port_args=""
    local unavailable_ports=()

    if [[ ${#ports[@]} -gt 0 ]]; then

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
    debug_log "Checking if VM '$VM_NAME' is running"

    if ! orb status "$VM_NAME" 2>/dev/null | grep -q "running"; then
        debug_log "VM not running, AUTO_START_VM='$AUTO_START_VM'"
        if [[ "$AUTO_START_VM" == "true" ]]; then
            echo "   -> Starting VM '$VM_NAME'..."
            verbose_log "Running: orb start $VM_NAME"
            if ! orb start "$VM_NAME"; then
                echo "❌ Error: Failed to start VM '$VM_NAME'"
                echo "   • Check OrbStack is installed and running: orb list"
                echo "   • Create the VM if it doesn't exist: dev env new docker-host"
                exit 1
            fi
        else
            echo "❌ Error: VM '$VM_NAME' is not running"
            echo "   Start it manually with: dev env up docker-host"
            echo "   Or set auto_start_vm=true in your config"
            exit 1
        fi
    else
        debug_log "VM '$VM_NAME' is already running"
    fi
}

function build_image() {
    local platform_flag="$1"
    local current_arch=$(detect_architecture)

    debug_log "Building image with platform_flag='$platform_flag'"
    trace_log "Dockerfile: $DOCKERFILE, Image: $IMAGE_NAME, VM: $VM_NAME"

    echo "   -> Building Docker image '$IMAGE_NAME'..."
    echo "   -> Host architecture: $current_arch"

    local build_result
    if [[ -n "$platform_flag" ]]; then
        echo "   -> Target platform: ${platform_flag#--platform }"
        verbose_log "Docker build command: docker build $platform_flag -f $DOCKERFILE -t $IMAGE_NAME ."
        orb -m "$VM_NAME" sudo docker build $platform_flag -f "$DOCKERFILE" -t "$IMAGE_NAME" .
        build_result=$?
    else
        echo "   -> Target platform: auto-detected (linux/$current_arch)"
        verbose_log "Docker build command: docker build -f $DOCKERFILE -t $IMAGE_NAME ."
        orb -m "$VM_NAME" sudo docker build -f "$DOCKERFILE" -t "$IMAGE_NAME" .
        build_result=$?
    fi

    if [[ $build_result -ne 0 ]]; then
        debug_log "Docker build failed with exit code: $build_result"
        echo "❌ Error: Docker build failed"
        echo "   Check your Dockerfile and requirements files"
        exit 1
    fi

    debug_log "Docker build completed successfully"
}

function cleanup_existing_container() {
    if orb -m "$VM_NAME" sudo docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
        echo "   -> Stopping existing container..."
        orb -m "$VM_NAME" sudo docker stop "$CONTAINER_NAME" >/dev/null 2>&1
        orb -m "$VM_NAME" sudo docker rm "$CONTAINER_NAME" >/dev/null 2>&1
    fi
}
