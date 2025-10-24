#!/bin/bash

# ==============================================================================
# UTILITY FUNCTIONS
# ==============================================================================

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
    echo "  help [cmd], troubleshoot     Help and diagnostics"
    echo ""
    echo "Options:"
    echo "  -f FILE        Dockerfile path    -t TAG         Custom image tag"
    echo "  -n NAME        Container name     --platform     Target architecture"
    echo "  -y, --yes      Skip prompts       -h, --help     Show help"
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

function show_new_help() {
    echo "Usage: $(basename "$0") new [<language>] [--init] [--devcontainer]"
    echo ""
    echo "Create a Dockerfile from a language template."
    echo ""
    echo "Arguments:"
    echo "  <language>     Language template (python, node, golang, rust, java, php, bash)"
    echo "                 Optionally specify version: python-3.12, node-22, etc."
    echo "                 If omitted, auto-detects project type from current directory"
    echo ""
    echo "Options:"
    echo "  --init         Also create project scaffolding for the language"
    echo "  --devcontainer Also generate VS Code devcontainer.json configuration"
    echo "  --yes, -y      Skip confirmation prompts"
    echo ""
    echo "Examples:"
    echo "  $(basename "$0") new                   # Auto-detect project type"
    echo "  $(basename "$0") new python           # Latest Python template"
    echo "  $(basename "$0") new python-3.11      # Specific Python version"
    echo "  $(basename "$0") new node --init       # Node.js with package.json"
    echo "  $(basename "$0") new rust --init --devcontainer  # Rust with VS Code config"
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

function show_devcontainer_help() {
    echo "Usage: $(basename "$0") devcontainer [<language>] [-f <dockerfile>]"
    echo ""
    echo "Generate VS Code devcontainer.json configuration for seamless IDE integration."
    echo ""
    echo "Arguments:"
    echo "  <language>     Language template (python, node, golang, rust, java, php, bash)"
    echo "                 Optionally specify version: python-3.12, node-22, etc."
    echo "                 If omitted, auto-detects project type from current directory"
    echo ""
    echo "Options:"
    echo "  -f FILE        Path to Dockerfile (default: ./Dockerfile)"
    echo "  --yes, -y      Skip confirmation prompts"
    echo ""
    echo "What it creates:"
    echo "  • .devcontainer/devcontainer.json with language-specific settings"
    echo "  • Automatic port forwarding based on detected project type"
    echo "  • SSH key and git configuration mounting"
    echo "  • Language-specific VS Code extensions and settings"
    echo ""
    echo "Examples:"
    echo "  $(basename "$0") devcontainer              # Auto-detect project type"
    echo "  $(basename "$0") devcontainer python       # Python devcontainer"
    echo "  $(basename "$0") devcontainer node-22      # Node.js v22 devcontainer"
    echo "  $(basename "$0") devcontainer rust -f Dockerfile.dev  # Custom Dockerfile"
    echo ""
    echo "After generation:"
    echo "  1. Open project in VS Code"
    echo "  2. Install 'Dev Containers' extension"
    echo "  3. Cmd+Shift+P → 'Dev Containers: Reopen in Container'"
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

function handle_devcontainer_command() {
    local language="$1"
    local dockerfile_path="$2"
    
    # Check if Dockerfile exists
    if [[ ! -f "$dockerfile_path" ]]; then
        echo "❌ Error: Dockerfile not found at '$dockerfile_path'"
        echo ""
        echo "💡 Suggestions:"
        echo "   • Create from template: 'dev new <language>'"
        echo "   • Use different file: 'dev devcontainer <language> -f /path/to/Dockerfile'"
        echo "   • See available templates: 'dev list'"
        exit 1
    fi
    
    # Auto-detect language if not provided
    if [[ -z "$language" ]]; then
        echo "🔍 Auto-detecting project type..."
        local detection_result=$(detect_project_type)
        local detected_lang=$(echo "$detection_result" | cut -d: -f1)
        local detected_version=$(echo "$detection_result" | cut -d: -f2)
        
        if [[ -n "$detected_lang" ]]; then
            if [[ -n "$detected_version" ]]; then
                language="$detected_lang-$detected_version"
            else
                language="$detected_lang"
            fi
            echo "   Detected: $language"
        else
            echo "❌ Error: Could not auto-detect project type."
            echo "Please specify the language: 'dev devcontainer <language>'"
            echo "Available languages: python, node, golang, rust, java, php, bash"
            exit 1
        fi
    fi
    
    # Check if .devcontainer already exists
    if [[ -d ".devcontainer" ]]; then
        echo "⚠️  Warning: .devcontainer directory already exists."
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
    
    # Parse language and version
    local base_lang="${language%%-*}"
    local version="${language#*-}"
    if [[ "$base_lang" == "$version" ]]; then
        version=""
    fi
    
    echo "🔧 Generating VS Code devcontainer configuration..."
    echo "   Language: $base_lang"
    [[ -n "$version" ]] && echo "   Version: $version"
    echo "   Dockerfile: $dockerfile_path"
    
    # Generate the devcontainer configuration
    if generate_devcontainer_config "$language" "$version" "$dockerfile_path"; then
        echo ""
        echo "✅ VS Code devcontainer configuration created!"
        echo ""
        echo "📁 Generated files:"
        echo "   .devcontainer/devcontainer.json"
        echo ""
        echo "🚀 Next steps:"
        echo "   1. Open this project in VS Code"
        echo "   2. Install the 'Dev Containers' extension if not already installed"
        echo "   3. Press Cmd+Shift+P and select 'Dev Containers: Reopen in Container'"
        echo "   4. VS Code will build and open your project in the isolated container"
        echo ""
        echo "💡 Benefits:"
        echo "   • Full IDE features (IntelliSense, debugging, extensions)"
        echo "   • Automatic port forwarding and SSH key mounting"
        echo "   • Consistent development environment across your team"
    else
        echo "❌ Error: Failed to generate devcontainer configuration"
        exit 1
    fi
    
    exit 0
}