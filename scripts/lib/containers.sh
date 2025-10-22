#!/bin/bash

# ==============================================================================
# CONTAINER OPERATIONS
# ==============================================================================

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
    
    # Remove duplicates and return
    printf '%s\n' "${ports[@]}" | sort -nu | tr '\n' ' '
}

function get_ssh_key_mounts() {
    local ssh_mounts=""
    
    # Mount SSH keys if they exist (for non-root user)
    if [[ -d "$HOME/.ssh" ]]; then
        ssh_mounts="-v $HOME/.ssh:/home/appuser/.ssh:ro"
    fi
    
    echo "$ssh_mounts"
}

function get_common_volume_mounts() {
    local volumes=""
    
    # Always mount the current directory (read-write for development)
    volumes="-v $(pwd):/workspace"
    
    # Mount git configuration if it exists (read-only)
    if [[ -f "$HOME/.gitconfig" ]]; then
        volumes="$volumes -v $HOME/.gitconfig:/home/appuser/.gitconfig:ro"
    fi
    
    # Mount Docker socket for Docker-in-Docker development (with warning)
    if [[ -S "/var/run/docker.sock" ]]; then
        echo "⚠️  Warning: Mounting Docker socket reduces container isolation" >&2
        volumes="$volumes -v /var/run/docker.sock:/var/run/docker.sock"
    fi
    
    echo "$volumes"
}

function build_port_forwards() {
    local ports=($(detect_common_ports))
    local port_args=""
    
    if [[ ${#ports[@]} -gt 0 ]]; then
        echo "🔌 Detected common development ports: ${ports[*]}" >&2
        for port in "${ports[@]}"; do
            port_args="$port_args -p $port:$port"
        done
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
    if [[ -n "$platform_flag" ]]; then
        echo "   -> Target platform: ${platform_flag#--platform }"
        orb -m "$VM_NAME" sudo docker build $platform_flag -f "$DOCKERFILE" -t "$IMAGE_NAME" .
    else
        echo "   -> Target platform: auto-detected (linux/$current_arch)"
        orb -m "$VM_NAME" sudo docker build -f "$DOCKERFILE" -t "$IMAGE_NAME" .
    fi
}

function cleanup_existing_container() {
    if orb -m "$VM_NAME" sudo docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
        echo "   -> Stopping existing container..."
        orb -m "$VM_NAME" sudo docker stop "$CONTAINER_NAME" >/dev/null 2>&1
        orb -m "$VM_NAME" sudo docker rm "$CONTAINER_NAME" >/dev/null 2>&1
    fi
}