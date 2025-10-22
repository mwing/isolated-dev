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
    local detected_lang=""
    local detected_version=""
    local confidence="low"
    
    # Python detection
    if [[ -f "requirements.txt" ]] || [[ -f "pyproject.toml" ]] || [[ -f "setup.py" ]] || [[ -f "Pipfile" ]]; then
        detected_lang="python"
        confidence="high"
        # Try to detect Python version from files
        if [[ -f "pyproject.toml" ]] && grep -q "python_requires" pyproject.toml; then
            detected_version=$(grep "python_requires" pyproject.toml | sed 's/.*>=\s*"\([0-9]\+\.[0-9]\+\).*/\1/' | head -1)
        elif [[ -f ".python-version" ]]; then
            detected_version=$(cat .python-version | head -1 | sed 's/\([0-9]\+\.[0-9]\+\).*/\1/')
        fi
    # Node.js detection
    elif [[ -f "package.json" ]]; then
        detected_lang="node"
        confidence="high"
        # Try to detect Node version
        if [[ -f ".nvmrc" ]]; then
            detected_version=$(cat .nvmrc | head -1 | sed 's/v\?\([0-9]\+\).*/\1/')
        elif [[ -f "package.json" ]] && grep -q ""engines"" package.json; then
            detected_version=$(grep -A2 ""engines"" package.json | grep ""node"" | sed 's/.*">=\?\s*\([0-9]\+\).*/\1/' | head -1)
        fi
    # Go detection
    elif [[ -f "go.mod" ]] || [[ -f "main.go" ]]; then
        detected_lang="golang"
        confidence="high"
        if [[ -f "go.mod" ]]; then
            detected_version=$(grep "^go " go.mod | sed 's/go \([0-9]\+\.[0-9]\+\).*/\1/' | head -1)
        fi
    # Rust detection
    elif [[ -f "Cargo.toml" ]]; then
        detected_lang="rust"
        confidence="high"
    # Java detection
    elif [[ -f "pom.xml" ]] || [[ -f "build.gradle" ]] || [[ -f "build.gradle.kts" ]]; then
        detected_lang="java"
        confidence="high"
    # PHP detection
    elif [[ -f "composer.json" ]] || [[ -f "index.php" ]]; then
        detected_lang="php"
        confidence="high"
    # Shell script detection
    elif ls *.sh >/dev/null 2>&1; then
        detected_lang="bash"
        confidence="medium"
    fi
    
    echo "$detected_lang:$detected_version:$confidence"
}

function usage() {
    echo "Usage: $(basename "$0") [OPTIONS] [COMMAND]"
    echo ""
    echo "Build and run isolated development containers using OrbStack VMs."
    echo ""
    echo "Commands:"
    echo "  run, shell, build, clean     Container operations"
    echo "  new <lang> [--init]          Create Dockerfile from template"
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
    echo "  $(basename "$0")                       # Build and run"
    echo ""
    echo "Use '$(basename "$0") help <command>' for detailed help on specific commands."
    exit 0
}

function show_command_help() {
    local command="$1"
    case "$command" in
        new)
            echo "Usage: $(basename "$0") new [<language>] [--init]"
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
            echo ""
            echo "Examples:"
            echo "  $(basename "$0") new                   # Auto-detect project type"
            echo "  $(basename "$0") new python           # Latest Python template"
            echo "  $(basename "$0") new python-3.11      # Specific Python version"
            echo "  $(basename "$0") new node --init       # Node.js with package.json"
            echo "  $(basename "$0") new rust --init       # Rust with Cargo.toml"
            ;;
        config)
            echo "Usage: $(basename "$0") config [--edit|--init]"
            echo ""
            echo "Manage configuration files."
            echo ""
            echo "Options:"
            echo "  (no options)   Show current configuration"
            echo "  --edit         Edit global configuration in editor"
            echo "  --init         Create project-local configuration file"
            echo ""
            echo "Configuration Files:"
            echo "  Global:        ~/.dev-envs/config.yaml"
            echo "  Project-local: ./.devenv.yaml"
            echo ""
            echo "Examples:"
            echo "  $(basename "$0") config               # Show current config"
            echo "  $(basename "$0") config --edit        # Edit global config"
            echo "  $(basename "$0") config --init        # Create local config"
            ;;
        templates)
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
            ;;
        troubleshoot)
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
            ;;
        env)
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
            ;;
        arch)
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
            ;;
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