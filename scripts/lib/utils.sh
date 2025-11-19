#!/bin/bash

# ==============================================================================
# UTILITY FUNCTIONS
# ==============================================================================

# Source constants
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/constants.sh"

# Consolidated logging function with levels
function log() {
    local level="$1"; shift
    case "$level" in
        debug) [[ "${DEBUG:-false}" == "true" ]] && echo "🐛 DEBUG: $*" >&2 ;;
        verbose) [[ "${VERBOSE:-false}" == "true" ]] && echo "📝 VERBOSE: $*" >&2 ;;
        trace) [[ "${TRACE:-false}" == "true" ]] && echo "🔍 TRACE: ${FUNCNAME[2]}: $*" >&2 ;;
    esac
}

# Legacy compatibility functions
function debug_log() { log debug "$@"; }
function verbose_log() { log verbose "$@"; }
function trace_log() { log trace "$@"; }

function debug_show_info() {
    echo "🐛 Debug Information:"
    echo ""
    echo "Environment:"
    echo "  DEBUG: ${DEBUG:-false}"
    echo "  VERBOSE: ${VERBOSE:-false}"
    echo "  TRACE: ${TRACE:-false}"
    echo "  Architecture: $(detect_architecture)"
    echo "  Platform: $(uname -s)/$(uname -m)"
    echo ""
    echo "Available checks:"
    echo "  dev debug --check-vm       # VM connectivity"
    echo "  dev debug --check-docker   # Docker daemon"
}

function debug_check_vm() {
    echo "🔍 Checking VM connectivity..."
    local vm_name="${1:-dev-vm-docker-host}"
    
    [[ -z "$vm_name" ]] && { echo "❌ VM name required"; return 1; }
    
    if command -v orb >/dev/null 2>&1; then
        echo "✅ OrbStack CLI found"
        if orb list | grep -q "$vm_name"; then
            if orb list | grep -q "$vm_name.*running"; then
                echo "✅ VM '$vm_name' is running"
            else
                echo "⚠️  VM '$vm_name' exists but not running"
            fi
        else
            echo "❌ VM '$vm_name' not found"
        fi
    else
        echo "❌ OrbStack CLI not found"
    fi
}

function debug_check_docker() {
    echo "🔍 Checking Docker daemon in VM..."
    local vm_name="${1:-dev-vm-docker-host}"
    
    [[ -z "$vm_name" ]] && { echo "❌ VM name required"; return 1; }
    
    # Check if VM is running first
    if ! orb list | grep -q "$vm_name.*running"; then
        echo "❌ VM '$vm_name' is not running"
        echo "💡 Fix: dev env up docker-host"
        return 1
    fi
    
    echo "✅ VM '$vm_name' is running"
    
    # Check Docker daemon in VM (using sudo as required)
    if orb -m "$vm_name" sudo docker version >/dev/null 2>&1; then
        echo "✅ Docker daemon accessible in VM"
        local version=$(orb -m "$vm_name" sudo docker version --format "{{.Server.Version}}" 2>/dev/null)
        [[ -n "$version" ]] && echo "  Docker version: $version"
    else
        echo "❌ Docker daemon not accessible in VM"
        echo "💡 Possible fixes:"
        echo "   • Wait a moment (Docker may still be starting)"
        echo "   • Restart VM: dev env down docker-host && dev env up docker-host"
        echo "   • Check if Docker service is running: orb -m $vm_name systemctl status docker"
    fi
    
    # Check docker-compose in VM
    if orb -m "$vm_name" command -v docker-compose >/dev/null 2>&1; then
        echo "✅ Docker Compose available in VM"
    elif orb -m "$vm_name" sudo docker compose version >/dev/null 2>&1; then
        echo "✅ Docker Compose (plugin) available in VM"
    else
        echo "⚠️  Docker Compose not found in VM"
        echo "💡 Note: Modern Docker includes 'docker compose' command"
    fi
}

function detect_architecture() {
    local arch=$(uname -m)
    case "$arch" in
        x86_64)
            echo "amd64"
            ;;
        arm64|aarch64)
            echo "arm64"
            ;;
        *)
            echo "$arch"
            ;;
    esac
}

function get_platform_flag() {
    local target_platform="$1"
    local current_arch=$(detect_architecture)
    
    if [[ -n "$target_platform" ]]; then
        echo "--platform $target_platform"
    elif [[ "$current_arch" == "arm64" ]]; then
        echo "--platform linux/arm64"
    else
        echo "--platform linux/amd64"
    fi
}

function detect_project_type() {
    if [[ -d "$LANGUAGES_DIR" ]]; then
        detect_project_type_from_languages "$LANGUAGES_DIR"
    else
        echo "::low"
    fi
}

function check_disk_space() {
    local min_free_gb="${DEV_MIN_DISK_SPACE:-$(get_config_value "min_disk_space")}"
    min_free_gb="${min_free_gb:-5}"  # Default 5GB minimum
    
    # Get available disk space in GB
    local available_gb
    if command -v df >/dev/null 2>&1; then
        # macOS and Linux compatible
        available_gb=$(df -BG . 2>/dev/null | awk 'NR==2 {gsub(/G/, "", $4); print $4}' || echo "0")
    else
        available_gb="0"
    fi
    
    if [[ $available_gb -lt $min_free_gb ]]; then
        echo "⚠️  Warning: Low disk space ($available_gb GB available, minimum $min_free_gb GB recommended)"
        echo "   Consider cleaning up Docker images: docker system prune -a"
        return 1
    fi
    
    return 0
}

function show_disk_usage() {
    echo "🐳 Development Environment Disk Usage:"
    echo ""
    
    # Show Docker space usage if available
    if command -v docker >/dev/null 2>&1; then
        echo "📦 Docker space usage:"
        if docker system df 2>/dev/null; then
            echo ""
            echo "💡 To free up Docker space: docker system prune -a"
        else
            echo "   Docker not running"
        fi
    else
        echo "📦 Docker not installed"
    fi
    
    # Show cache usage
    local cache_dir="$DEV_CACHE_DIR"
    if [[ -d "$cache_dir" ]]; then
        echo ""
        echo "🗂️  Template cache usage:"
        local cache_size=$(du -sh "$cache_dir" 2>/dev/null | cut -f1)
        echo "   $cache_size in $cache_dir"
        
        local file_count=$(find "$cache_dir" -type f 2>/dev/null | wc -l | tr -d ' ')
        if [[ $file_count -gt 0 ]]; then
            echo "   $file_count cached files"
            echo "💡 To clear cache: dev templates cleanup"
        fi
    else
        echo ""
        echo "🗂️  No template cache found"
    fi
    
    # Show OrbStack usage if available
    if command -v orb >/dev/null 2>&1; then
        echo ""
        echo "🌐 OrbStack VMs:"
        orb list 2>/dev/null || echo "   No VMs found"
    fi
}

function usage() {
    echo "Usage: $(basename "$0") [OPTIONS] [COMMAND]"
    echo ""
    echo "Build and run isolated development containers using OrbStack VMs."
    echo ""
    echo "Commands:"
    echo "  run, shell, build, clean     Container operations"
    echo "  new <lang> [--init]          Create Dockerfile from template"
    echo "  devcontainer [lang]          Generate VS Code devcontainer.json"
    echo "  list                         Show available templates"
    echo "  env <cmd>                    Manage VMs (new, up, down, status, rm)"
    echo "  config [--edit|--init]       Configuration management"
    echo "  templates <cmd>              Template management (update, prune, stats)"
    echo "  arch                         Architecture information"
    echo "  disk                         Show disk usage information"
    echo "  help [cmd], troubleshoot     Help and diagnostics"
    echo "  debug [--check-*]            Debug information and checks"
    echo ""
    echo "Options:"
    echo "  -f FILE        Dockerfile path    -t TAG         Custom image tag"
    echo "  -n NAME        Container name     --platform     Target architecture"
    echo "  -y, --yes      Skip prompts       -h, --help     Show help"
    echo "  --debug        Enable debug mode  --verbose, -v  Verbose output"
    echo "  --trace        Enable tracing"
    echo ""
    echo "Quick Start:"
    echo "  $(basename "$0") env new docker-host    # One-time setup"
    echo "  $(basename "$0") new python --init      # Create Python project"
    echo "  $(basename "$0") devcontainer           # Generate VS Code config"
    echo "  $(basename "$0")                       # Build and run"
    echo ""
    echo "Use '$(basename "$0") help <command>' for detailed help on specific commands."
    exit 0
}

function show_config_help() {
    echo "Usage: $(basename "$0") config [--edit|--init|validate]"
    echo ""
    echo "Manage configuration files."
    echo ""
    echo "Options:"
    echo "  (no options)   Show current configuration"
    echo "  --edit         Edit global configuration in editor"
    echo "  --init         Create project-local configuration file"
    echo "  validate       Validate configuration files and show errors"
    echo ""
    echo "Configuration Files:"
    echo "  Global:        ~/.dev-envs/config.yaml (YAML format)"
    echo "  Project-local: ./.devenv.yaml (YAML format)"
    echo ""
    echo "Environment Variables (override config):"
    echo "  DEV_VM_NAME, DEV_DEFAULT_TEMPLATE, DEV_AUTO_START_VM, DEV_CONTAINER_PREFIX"
    echo ""
    echo "Examples:"
    echo "  $(basename "$0") config               # Show current config"
    echo "  $(basename "$0") config --edit        # Edit global config"
    echo "  $(basename "$0") config --init        # Create local config"
    echo "  $(basename "$0") config validate      # Validate config files"
    echo "  DEV_VM_NAME=test-vm $(basename "$0")  # Override VM name"
}

function show_templates_help() {
    echo "Usage: $(basename "$0") templates <action>"
    echo ""
    echo "Manage Dockerfile templates."
    echo ""
    echo "Actions:"
    echo "  update         Update all templates to latest versions"
    echo "  check          Check for available updates without applying"
    echo "  prune          Remove old/unused templates (smart cleanup)"
    echo "  cleanup [days] Remove templates unused for X days (default: 60)"
    echo "  stats          Show template statistics and usage information"
    echo ""
    echo "Examples:"
    echo "  $(basename "$0") templates update     # Update all templates"
    echo "  $(basename "$0") templates check      # Check for updates"
    echo "  $(basename "$0") templates prune      # Smart cleanup of old templates"
    echo "  $(basename "$0") templates cleanup 30 # Remove templates unused 30+ days"
    echo "  $(basename "$0") templates stats      # Show detailed statistics"
}

function show_troubleshoot_help() {
    echo "Troubleshooting Guide:"
    echo ""
    echo "Common Issues:"
    echo ""
    echo "1. Container fails to start:"
    echo "   • Check if OrbStack is running: 'orb list'"
    echo "   • Verify VM is accessible: '$(basename "$0") env status docker-host'"
    echo "   • Try rebuilding: '$(basename "$0") clean && $(basename "$0") build'"
    echo ""
    echo "2. Port forwarding not working:"
    echo "   • Ensure your app binds to 0.0.0.0, not localhost"
    echo "   • Check if port is already in use on host"
    echo "   • Verify framework-specific files exist (package.json, requirements.txt)"
    echo ""
    echo "3. SSH keys not working:"
    echo "   • Check SSH agent is running: 'ssh-add -l'"
    echo "   • Verify SSH keys exist in ~/.ssh/"
    echo "   • Try adding keys to agent: 'ssh-add ~/.ssh/id_rsa'"
    echo ""
    echo "4. Template creation fails:"
    echo "   • Check internet connection (templates fetch from Docker Hub)"
    echo "   • Try updating templates: '$(basename "$0") templates update'"
    echo "   • Verify language name: '$(basename "$0") list'"
    echo ""
    echo "5. Performance issues:"
    echo "   • Package caches are automatically mounted for faster installs"
    echo "   • Consider using .dockerignore to exclude large directories"
    echo "   • Check disk space: 'df -h'"
    echo ""
    echo "For more help, visit: https://github.com/mwing/isolated-dev"
}

function show_env_help() {
    echo "Usage: $(basename "$0") env <command> [environment]"
    echo ""
    echo "Manage OrbStack VMs for isolated development environments."
    echo ""
    echo "Commands:"
    echo "  new <env>        Create and provision new environment VM"
    echo "  up <env>         Start existing environment VM and connect"
    echo "  down <env>       Stop running environment VM"
    echo "  status <env>     Show environment VM status"
    echo "  rm <env>         Delete environment VM permanently"
    echo "  list             List available environments"
    echo ""
    echo "Examples:"
    echo "  $(basename "$0") env new docker-host    # One-time setup"
    echo "  $(basename "$0") env up docker-host     # Start & connect"
    echo "  $(basename "$0") env down docker-host   # Stop to save battery"
    echo "  $(basename "$0") env status docker-host # Check if running"
    echo ""
    echo "Note: Most users only need the 'docker-host' environment."
}

function show_arch_help() {
    echo "Usage: $(basename "$0") arch"
    echo ""
    echo "Show architecture and platform information for multi-architecture development."
    echo ""
    echo "This command displays:"
    echo "  • Current host architecture (arm64/amd64)"
    echo "  • Default Docker platform"
    echo "  • Supported platforms and usage examples"
    echo ""
    echo "Examples:"
    echo "  $(basename "$0") arch                    # Show architecture info"
    echo "  $(basename "$0") --platform linux/arm64  # Use specific platform"
}

function show_command_help() {
    local command="$1"
    case "$command" in
        new) show_new_help ;;
        config) show_config_help ;;
        templates) show_templates_help ;;
        troubleshoot) show_troubleshoot_help ;;
        env) show_env_help ;;
        arch) show_arch_help ;;
        devcontainer) show_devcontainer_help ;;
        *)
            echo "No specific help available for command: $command"
            echo "Use --help for general usage information."
            ;;
    esac
}

function list_templates() {
    echo "Available language templates:"
    if [[ -d "$LANGUAGES_DIR" ]]; then
        local templates_found=false
        
        # Create a simple formatted list
        printf "  %-12s %s\\n" "Language" "Available Versions"
        printf "  %-12s %s\\n" "--------" "------------------"
        
        # Process language plugins
        for lang_dir in "$LANGUAGES_DIR"/*; do
            [[ ! -d "$lang_dir" ]] && continue
            [[ "$(basename "$lang_dir")" == "README.md" ]] && continue
            
            local lang_name=$(basename "$lang_dir")
            local versions=""
            
            # Read versions from language.yaml
            if [[ -f "$lang_dir/language.yaml" ]]; then
                local versions_line=$(grep "versions:" "$lang_dir/language.yaml" | head -1)
                if [[ "$versions_line" =~ versions:[[:space:]]*\[(.*)\] ]]; then
                    local versions_str="${BASH_REMATCH[1]}"
                    # Clean up the versions string
                    versions=$(echo "$versions_str" | sed 's/"//g' | sed 's/,/, /g' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
                    templates_found=true
                fi
            fi
            
            # Display language and versions
            if [[ -n "$versions" ]]; then
                printf "  %-12s %s\\n" "$lang_name" "$versions"
            else
                printf "  %-12s %s\\n" "$lang_name" "(no versions defined)"
            fi
        done
        
        if [[ "$templates_found" == false ]]; then
            echo "  (No language plugins found - run installer first)"
        fi
    else
        echo "  (Languages directory not found - run installer first)"
    fi
    
    echo ""
    echo "Usage:"
    echo "  dev new <language>         # Use default/latest version"
    echo "  dev new <language-version> # Use specific version"
    echo "  dev new python-3.13 --init # Create with project scaffolding"
    exit 0
}
