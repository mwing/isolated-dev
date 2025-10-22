#!/bin/bash

# ==============================================================================
# CONFIGURATION FUNCTIONS
# ==============================================================================

function load_config() {
    # Set defaults
    VM_NAME="$DEFAULT_VM_NAME"
    DEFAULT_TEMPLATE=""
    AUTO_START_VM="true"
    CONTAINER_PREFIX="dev"
    
    # Load global config if it exists
    if [[ -f "$GLOBAL_CONFIG" ]]; then
        while IFS= read -r line; do
            # Skip comments and empty lines
            [[ "$line" =~ ^[[:space:]]*# ]] && continue
            [[ -z "$line" ]] && continue
            # Skip section headers like [profile.name]
            [[ "$line" =~ ^\[.*\] ]] && continue
            
            # Only process lines with equals sign
            if [[ "$line" =~ ^[[:space:]]*([^=]+)=[[:space:]]*(.*)$ ]]; then
                key="${BASH_REMATCH[1]}"
                value="${BASH_REMATCH[2]}"
                
                # Clean up key and value
                key=$(echo "$key" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
                value=$(echo "$value" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^"//;s/"$//')
                
                case "$key" in
                    vm_name) VM_NAME="$value" ;;
                    default_template) DEFAULT_TEMPLATE="$value" ;;
                    auto_start_vm) AUTO_START_VM="$value" ;;
                    container_prefix) CONTAINER_PREFIX="$value" ;;
                esac
            fi
        done < "$GLOBAL_CONFIG"
    fi
    
    # Load project-local config if it exists (overrides global)
    if [[ -f "$PROJECT_CONFIG" ]]; then
        while IFS= read -r line; do
            # Skip comments and empty lines
            [[ "$line" =~ ^[[:space:]]*# ]] && continue
            [[ -z "$line" ]] && continue
            # Skip section headers like [profile.name]
            [[ "$line" =~ ^\[.*\] ]] && continue
            
            # Only process lines with equals sign
            if [[ "$line" =~ ^[[:space:]]*([^=]+)=[[:space:]]*(.*)$ ]]; then
                key="${BASH_REMATCH[1]}"
                value="${BASH_REMATCH[2]}"
                
                # Clean up key and value
                key=$(echo "$key" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
                value=$(echo "$value" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^"//;s/"$//')
                
                case "$key" in
                    vm_name) VM_NAME="$value" ;;
                    default_template) DEFAULT_TEMPLATE="$value" ;;
                    auto_start_vm) AUTO_START_VM="$value" ;;
                    container_prefix) CONTAINER_PREFIX="$value" ;;
                esac
            fi
        done < "$PROJECT_CONFIG"
    fi
}

function create_default_config() {
    if [[ ! -f "$GLOBAL_CONFIG" ]]; then
        mkdir -p "$CONFIG_DIR"
        cat > "$GLOBAL_CONFIG" << 'EOF'
# Global configuration for isolated development environment
# This file sets defaults for all projects

# Default VM name to use for containers
vm_name = "dev-vm-docker-host"

# Default template when language has multiple versions
# Example: default_template = "python-3.13"
default_template = ""

# Automatically start VM if not running
auto_start_vm = "true"

# Prefix for container and image names
container_prefix = "dev"
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
# vm_name = "dev-vm-$project_name"

# Default template for this project (optional)
# default_template = "python-3.13"

# Container prefix for this project (optional)
# container_prefix = "$project_name"
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
            echo ""
            echo "Commands:"
            echo "  dev config --edit    Edit global configuration"
            echo "  dev config --init    Create project configuration"
            exit 0
            ;;
        *)
            # Show current configuration for unknown subcommands
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
            echo ""
            echo "Commands:"
            echo "  dev config --edit    Edit global configuration"
            echo "  dev config --init    Create project configuration"
            exit 0
            ;;
    esac
}