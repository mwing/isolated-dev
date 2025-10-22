#!/bin/bash

# ==============================================================================
# CONFIGURATION FUNCTIONS
# ==============================================================================

# Configuration schema and defaults
function get_config_type() {
    case "$1" in
        vm_name|default_template|container_prefix|network_mode|port_range) echo "string" ;;
        auto_start_vm|auto_host_networking|enable_port_health_check) echo "boolean" ;;
        port_health_timeout) echo "number" ;;
        *) echo "unknown" ;;
    esac
}

function get_config_default() {
    case "$1" in
        vm_name) echo "dev-vm-docker-host" ;;
        default_template) echo "" ;;
        auto_start_vm) echo "true" ;;
        container_prefix) echo "dev" ;;
        network_mode) echo "bridge" ;;
        auto_host_networking) echo "false" ;;
        port_range) echo "3000-9000" ;;
        enable_port_health_check) echo "true" ;;
        port_health_timeout) echo "5" ;;
        *) echo "" ;;
    esac
}

function parse_yaml_config() {
    local config_file="$1"
    
    if [[ ! -f "$config_file" ]]; then
        return 0
    fi
    
    # Parse YAML using a simple approach that handles our specific format
    while IFS= read -r line; do
        # Skip comments and empty lines
        [[ "$line" =~ ^[[:space:]]*# ]] && continue
        [[ -z "$line" ]] && continue
        
        # Handle YAML key: value format
        if [[ "$line" =~ ^[[:space:]]*([^:]+):[[:space:]]*(.*)$ ]]; then
            local key="${BASH_REMATCH[1]}"
            local value="${BASH_REMATCH[2]}"
            
            # Clean up key and value
            key=$(echo "$key" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
            value=$(echo "$value" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^["'"'"']//;s/["'"'"']$//')
            
            # Set configuration variable
            case "$key" in
                vm_name) VM_NAME="$value" ;;
                default_template) DEFAULT_TEMPLATE="$value" ;;
                auto_start_vm) AUTO_START_VM="$value" ;;
                container_prefix) CONTAINER_PREFIX="$value" ;;
                network_mode) NETWORK_MODE="$value" ;;
                auto_host_networking) AUTO_HOST_NETWORKING="$value" ;;
                port_range) PORT_RANGE="$value" ;;
                enable_port_health_check) ENABLE_PORT_HEALTH_CHECK="$value" ;;
                port_health_timeout) PORT_HEALTH_TIMEOUT="$value" ;;
            esac
        # Handle legacy key = value format for backward compatibility
        elif [[ "$line" =~ ^[[:space:]]*([^=]+)=[[:space:]]*(.*)$ ]]; then
            local key="${BASH_REMATCH[1]}"
            local value="${BASH_REMATCH[2]}"
            
            # Clean up key and value
            key=$(echo "$key" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
            value=$(echo "$value" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^["'"'"']//;s/["'"'"']$//')
            
            # Set configuration variable
            case "$key" in
                vm_name) VM_NAME="$value" ;;
                default_template) DEFAULT_TEMPLATE="$value" ;;
                auto_start_vm) AUTO_START_VM="$value" ;;
                container_prefix) CONTAINER_PREFIX="$value" ;;
                network_mode) NETWORK_MODE="$value" ;;
                auto_host_networking) AUTO_HOST_NETWORKING="$value" ;;
                port_range) PORT_RANGE="$value" ;;
                enable_port_health_check) ENABLE_PORT_HEALTH_CHECK="$value" ;;
                port_health_timeout) PORT_HEALTH_TIMEOUT="$value" ;;
            esac
        fi
    done < "$config_file"
}

function apply_env_overrides() {
    # Apply environment variable overrides
    [[ -n "${DEV_VM_NAME:-}" ]] && VM_NAME="$DEV_VM_NAME"
    [[ -n "${DEV_DEFAULT_TEMPLATE:-}" ]] && DEFAULT_TEMPLATE="$DEV_DEFAULT_TEMPLATE"
    [[ -n "${DEV_AUTO_START_VM:-}" ]] && AUTO_START_VM="$DEV_AUTO_START_VM"
    [[ -n "${DEV_CONTAINER_PREFIX:-}" ]] && CONTAINER_PREFIX="$DEV_CONTAINER_PREFIX"
    [[ -n "${DEV_NETWORK_MODE:-}" ]] && NETWORK_MODE="$DEV_NETWORK_MODE"
    [[ -n "${DEV_AUTO_HOST_NETWORKING:-}" ]] && AUTO_HOST_NETWORKING="$DEV_AUTO_HOST_NETWORKING"
    [[ -n "${DEV_PORT_RANGE:-}" ]] && PORT_RANGE="$DEV_PORT_RANGE"
    [[ -n "${DEV_ENABLE_PORT_HEALTH_CHECK:-}" ]] && ENABLE_PORT_HEALTH_CHECK="$DEV_ENABLE_PORT_HEALTH_CHECK"
    [[ -n "${DEV_PORT_HEALTH_TIMEOUT:-}" ]] && PORT_HEALTH_TIMEOUT="$DEV_PORT_HEALTH_TIMEOUT"
}

function validate_config_value() {
    local key="$1"
    local value="$2"
    local type=$(get_config_type "$key")
    
    case "$type" in
        "boolean")
            if [[ ! "$value" =~ ^(true|false)$ ]]; then
                echo "❌ Error: '$key' must be 'true' or 'false', got '$value'"
                return 1
            fi
            ;;
        "string")
            # String validation - check for reasonable length and no special chars
            if [[ ${#value} -gt 100 ]]; then
                echo "❌ Error: '$key' is too long (max 100 characters)"
                return 1
            fi
            if [[ "$key" == "vm_name" && ! "$value" =~ ^[a-zA-Z0-9_-]+$ ]]; then
                echo "❌ Error: '$key' contains invalid characters (use only letters, numbers, hyphens, underscores)"
                return 1
            fi
            if [[ "$key" == "container_prefix" && ! "$value" =~ ^[a-zA-Z0-9_-]+$ ]]; then
                echo "❌ Error: '$key' contains invalid characters (use only letters, numbers, hyphens, underscores)"
                return 1
            fi
            if [[ "$key" == "network_mode" && ! "$value" =~ ^(bridge|host|none|[a-zA-Z0-9_-]+)$ ]]; then
                echo "❌ Error: '$key' must be 'bridge', 'host', 'none', or a custom network name"
                return 1
            fi
            if [[ "$key" == "port_range" && ! "$value" =~ ^[0-9]+-[0-9]+$ ]]; then
                echo "❌ Error: '$key' must be in format 'start-end' (e.g., '3000-9000')"
                return 1
            fi
            ;;
        "number")
            if ! [[ "$value" =~ ^[0-9]+$ ]]; then
                echo "❌ Error: '$key' must be a positive number, got '$value'"
                return 1
            fi
            ;;
    esac
    return 0
}

function validate_config() {
    local config_file="$1"
    local errors=0
    
    if [[ ! -f "$config_file" ]]; then
        echo "❌ Error: Configuration file not found: $config_file"
        return 1
    fi
    
    echo "🔍 Validating configuration: $config_file"
    
    # Parse and validate each line
    while IFS= read -r line; do
        # Skip comments and empty lines
        [[ "$line" =~ ^[[:space:]]*# ]] && continue
        [[ -z "$line" ]] && continue
        
        local key="" value=""
        
        # Handle YAML format
        if [[ "$line" =~ ^[[:space:]]*([^:]+):[[:space:]]*(.*)$ ]]; then
            key="${BASH_REMATCH[1]}"
            value="${BASH_REMATCH[2]}"
        # Handle legacy format
        elif [[ "$line" =~ ^[[:space:]]*([^=]+)=[[:space:]]*(.*)$ ]]; then
            key="${BASH_REMATCH[1]}"
            value="${BASH_REMATCH[2]}"
        else
            echo "❌ Error: Invalid syntax on line: $line"
            ((errors++))
            continue
        fi
        
        # Clean up key and value
        key=$(echo "$key" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
        value=$(echo "$value" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^["'"'"']//;s/["'"'"']$//')
        
        # Check if key is valid
        if [[ $(get_config_type "$key") == "unknown" ]]; then
            echo "❌ Error: Unknown configuration key '$key'"
            ((errors++))
            continue
        fi
        
        # Validate value
        if ! validate_config_value "$key" "$value"; then
            ((errors++))
        fi
    done < "$config_file"
    
    if [[ $errors -eq 0 ]]; then
        echo "✅ Configuration is valid"
        return 0
    else
        echo "❌ Found $errors validation error(s)"
        return 1
    fi
}

function load_config() {
    # Set defaults
    VM_NAME=$(get_config_default "vm_name")
    DEFAULT_TEMPLATE=$(get_config_default "default_template")
    AUTO_START_VM=$(get_config_default "auto_start_vm")
    CONTAINER_PREFIX=$(get_config_default "container_prefix")
    NETWORK_MODE=$(get_config_default "network_mode")
    AUTO_HOST_NETWORKING=$(get_config_default "auto_host_networking")
    PORT_RANGE=$(get_config_default "port_range")
    ENABLE_PORT_HEALTH_CHECK=$(get_config_default "enable_port_health_check")
    PORT_HEALTH_TIMEOUT=$(get_config_default "port_health_timeout")
    
    # Load global config if it exists
    parse_yaml_config "$GLOBAL_CONFIG"
    
    # Load project-local config if it exists (overrides global)
    parse_yaml_config "$PROJECT_CONFIG"
    
    # Apply environment variable overrides (highest priority)
    apply_env_overrides
}

function create_default_config() {
    if [[ ! -f "$GLOBAL_CONFIG" ]]; then
        mkdir -p "$CONFIG_DIR"
        cat > "$GLOBAL_CONFIG" << 'EOF'
# Global configuration for isolated development environment
# This file sets defaults for all projects

# Default VM name to use for containers
vm_name: dev-vm-docker-host

# Default template when language has multiple versions
# Example: default_template: python-3.13
default_template: ""

# Automatically start VM if not running
auto_start_vm: true

# Prefix for container and image names
container_prefix: dev

# Network configuration
network_mode: bridge                    # bridge, host, none, or custom network name
auto_host_networking: false             # Auto-use host networking for single services
port_range: "3000-9000"                 # Port range for auto-detection
enable_port_health_check: true          # Check if ports are accessible
port_health_timeout: 5                  # Timeout for port health checks (seconds)
EOF
        echo "📝 Created default config at $GLOBAL_CONFIG"
        echo "   Edit this file to customize your development environment defaults."
    fi
}

function handle_config_command() {
    local subcommand="$1"
    
    # Load configuration before processing commands
    load_config
    
    case "${subcommand:-}" in
        validate)
            local config_to_validate="$GLOBAL_CONFIG"
            local exit_code=0
            
            # Validate global config
            if [[ -f "$GLOBAL_CONFIG" ]]; then
                if ! validate_config "$GLOBAL_CONFIG"; then
                    exit_code=1
                fi
            else
                echo "ℹ️  No global config found at $GLOBAL_CONFIG"
            fi
            
            # Validate project config if it exists
            if [[ -f "$PROJECT_CONFIG" ]]; then
                echo ""
                if ! validate_config "$PROJECT_CONFIG"; then
                    exit_code=1
                fi
            fi
            
            # Show environment variable overrides
            local env_overrides=()
            [[ -n "${DEV_VM_NAME:-}" ]] && env_overrides+=("DEV_VM_NAME=$DEV_VM_NAME")
            [[ -n "${DEV_DEFAULT_TEMPLATE:-}" ]] && env_overrides+=("DEV_DEFAULT_TEMPLATE=$DEV_DEFAULT_TEMPLATE")
            [[ -n "${DEV_AUTO_START_VM:-}" ]] && env_overrides+=("DEV_AUTO_START_VM=$DEV_AUTO_START_VM")
            [[ -n "${DEV_CONTAINER_PREFIX:-}" ]] && env_overrides+=("DEV_CONTAINER_PREFIX=$DEV_CONTAINER_PREFIX")
            [[ -n "${DEV_NETWORK_MODE:-}" ]] && env_overrides+=("DEV_NETWORK_MODE=$DEV_NETWORK_MODE")
            [[ -n "${DEV_AUTO_HOST_NETWORKING:-}" ]] && env_overrides+=("DEV_AUTO_HOST_NETWORKING=$DEV_AUTO_HOST_NETWORKING")
            [[ -n "${DEV_PORT_RANGE:-}" ]] && env_overrides+=("DEV_PORT_RANGE=$DEV_PORT_RANGE")
            [[ -n "${DEV_ENABLE_PORT_HEALTH_CHECK:-}" ]] && env_overrides+=("DEV_ENABLE_PORT_HEALTH_CHECK=$DEV_ENABLE_PORT_HEALTH_CHECK")
            [[ -n "${DEV_PORT_HEALTH_TIMEOUT:-}" ]] && env_overrides+=("DEV_PORT_HEALTH_TIMEOUT=$DEV_PORT_HEALTH_TIMEOUT")
            
            if [[ ${#env_overrides[@]} -gt 0 ]]; then
                echo ""
                echo "🌍 Environment variable overrides:"
                for override in "${env_overrides[@]}"; do
                    echo "   $override"
                done
            fi
            
            exit $exit_code
            ;;
        --edit)
            create_default_config
            if command -v code >/dev/null 2>&1; then
                echo "📝 Opening config in VS Code..."
                code "$GLOBAL_CONFIG"
            elif command -v vim >/dev/null 2>&1; then
                echo "📝 Opening config in vim..."
                vim "$GLOBAL_CONFIG"
            else
                echo "📝 Edit your config file: $GLOBAL_CONFIG"
            fi
            exit 0
            ;;
        --init)
            if [[ -f "$PROJECT_CONFIG" ]]; then
                echo "⚠️  Project config already exists: $PROJECT_CONFIG"
                if [[ "$AUTO_YES" == "true" ]]; then
                    echo "Auto-confirming overwrite (--yes flag set)"
                else
                    echo -n "Do you want to overwrite it? (y/N): "
                    read -r response
                    if [[ ! "$response" =~ ^[Yy]$ ]]; then
                        echo "Operation cancelled."
                        exit 0
                    fi
                fi
            fi
            
            local project_name=$(basename "$(pwd)")
            cat > "$PROJECT_CONFIG" << EOF
# Project-specific configuration for $project_name
# This file overrides global settings for this project only

# VM name for this project (optional)
# vm_name: dev-vm-$project_name

# Default template for this project (optional)
# default_template: python-3.13

# Container prefix for this project (optional)
# container_prefix: $project_name

# Network configuration (optional)
# network_mode: bridge              # bridge, host, none, or custom network
# auto_host_networking: false       # Auto-use host networking
# port_range: "3000-9000"           # Custom port range
# enable_port_health_check: true    # Enable port health checks
# port_health_timeout: 5            # Port check timeout (seconds)
EOF
            echo "✅ Created project config: $PROJECT_CONFIG"
            echo "   Edit this file to customize settings for this project."
            exit 0
            ;;
        "")
            # Show current configuration when no subcommand provided
            create_default_config
            
            echo "📋 Current Configuration:"
            echo "  Global config: $GLOBAL_CONFIG"
            if [[ -f "$PROJECT_CONFIG" ]]; then
                echo "  Project config: $PROJECT_CONFIG (active)"
            else
                echo "  Project config: none"
            fi
            echo ""
            echo "  VM Name: $VM_NAME"
            echo "  Default Template: ${DEFAULT_TEMPLATE:-"(prompt for selection)"}"
            echo "  Auto Start VM: $AUTO_START_VM"
            echo "  Container Prefix: $CONTAINER_PREFIX"
            echo "  Network Mode: $NETWORK_MODE"
            echo "  Auto Host Networking: $AUTO_HOST_NETWORKING"
            echo "  Port Range: $PORT_RANGE"
            echo "  Port Health Check: $ENABLE_PORT_HEALTH_CHECK"
            echo "  Port Health Timeout: ${PORT_HEALTH_TIMEOUT}s"
            
            # Show environment variable overrides if any
            local env_overrides=()
            [[ -n "${DEV_VM_NAME:-}" ]] && env_overrides+=("DEV_VM_NAME")
            [[ -n "${DEV_DEFAULT_TEMPLATE:-}" ]] && env_overrides+=("DEV_DEFAULT_TEMPLATE")
            [[ -n "${DEV_AUTO_START_VM:-}" ]] && env_overrides+=("DEV_AUTO_START_VM")
            [[ -n "${DEV_CONTAINER_PREFIX:-}" ]] && env_overrides+=("DEV_CONTAINER_PREFIX")
            [[ -n "${DEV_NETWORK_MODE:-}" ]] && env_overrides+=("DEV_NETWORK_MODE")
            [[ -n "${DEV_AUTO_HOST_NETWORKING:-}" ]] && env_overrides+=("DEV_AUTO_HOST_NETWORKING")
            [[ -n "${DEV_PORT_RANGE:-}" ]] && env_overrides+=("DEV_PORT_RANGE")
            [[ -n "${DEV_ENABLE_PORT_HEALTH_CHECK:-}" ]] && env_overrides+=("DEV_ENABLE_PORT_HEALTH_CHECK")
            [[ -n "${DEV_PORT_HEALTH_TIMEOUT:-}" ]] && env_overrides+=("DEV_PORT_HEALTH_TIMEOUT")
            
            if [[ ${#env_overrides[@]} -gt 0 ]]; then
                echo ""
                echo "🌍 Environment overrides active: ${env_overrides[*]}"
            fi
            
            echo ""
            echo "Commands:"
            echo "  dev config --edit      Edit global configuration"
            echo "  dev config --init      Create project configuration"
            echo "  dev config validate    Validate configuration files"
            exit 0
            ;;
        *)
            echo "❌ Error: Unknown config subcommand '$subcommand'"
            echo "Available subcommands: --edit, --init, validate"
            exit 1
            ;;
    esac
}