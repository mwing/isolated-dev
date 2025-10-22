#!/bin/bash

# ==============================================================================
# UTILITY FUNCTIONS
# ==============================================================================

function usage() {
    echo "Usage: $(basename "$0") [OPTIONS] [COMMAND]"
    echo ""
    echo "A script to build and run isolated development containers using OrbStack VMs."
    echo ""
    echo "Commands:"
    echo "  run              Build and run the container (default)"
    echo "  shell            Open interactive bash shell in container"
    echo "  build            Build the container image only"
    echo "  clean            Remove existing container and image"
    echo ""
    echo "Environment Commands:"
    echo "  env <command>    Manage OrbStack VMs (new, up, down, status, rm)"
    echo "  env list         Show available environments and help"
    echo ""
    echo "Help Commands:"
    echo "  help             Show this help message"
    echo "  help <command>   Show help for specific command"
    echo "  troubleshoot     Show troubleshooting guide"
    echo ""
    echo "Template Commands:"
    echo "  new <language>       Create a Dockerfile from template"
    echo "  new <language> --init Create Dockerfile and project scaffolding"
    echo "  list                 List available language templates"
    echo "  templates update     Update templates to latest versions"
    echo "  templates check      Check for available template updates"
    echo "  templates prune      Remove old/unused templates automatically"
    echo "  templates cleanup    Remove templates unused for specified days"
    echo "  templates stats      Show template statistics and usage information"
    echo ""
    echo "Configuration Commands:"
    echo "  config               Show current configuration"
    echo "  config --edit        Edit global configuration file"
    echo "  config --init        Create project-local configuration"
    echo ""
    echo "Options:"
    echo "  -h, --help     Show this help message"
    echo "  -f, --file     Specify Dockerfile path (default: ./Dockerfile)"
    echo "  -t, --tag      Specify custom image tag"
    echo "  -n, --name     Specify custom container name"
    echo "  -y, --yes      Automatically answer 'yes' to all prompts (for automation)"
    echo ""
    echo "Enhanced Developer Experience:"
    echo "  • Automatic port forwarding detection (Node.js 3000, Python 8000, etc.)"
    echo "  • SSH key mounting for seamless git operations"
    echo "  • Git configuration sharing for consistent commits"
    echo ""
    echo "Examples:"
    echo "  $(basename "$0") env new docker-host           # One-time VM setup"
    echo "  $(basename "$0")                              # Build and run with default Dockerfile"
    echo "  $(basename "$0") shell                        # Open interactive shell"
    echo "  $(basename "$0") new python                   # Create from Python template"
    echo "  $(basename "$0") new node-22 --init           # Create Node.js template with scaffolding"
    echo "  $(basename "$0") new python --yes             # Create template, auto-overwrite existing files"
    echo "  $(basename "$0") templates cleanup 30 --yes   # Remove old templates without prompting"
    echo "  $(basename "$0") env down docker-host         # Stop VM to save battery"
    echo "  $(basename "$0") list                         # Show available templates with versions"
    echo ""
    echo "Requirements:"
    echo "  - Dockerfile in current directory (or specified with -f)"
    echo "  - OrbStack VM '$VM_NAME' available"
    exit 0
}

function show_command_help() {
    local command="$1"
    case "$command" in
        new)
            echo "Usage: $(basename "$0") new <language> [--init]"
            echo ""
            echo "Create a Dockerfile from a language template."
            echo ""
            echo "Arguments:"
            echo "  <language>     Language template (python, node, golang, rust, java, php, bash)"
            echo "                 Optionally specify version: python-3.12, node-22, etc."
            echo ""
            echo "Options:"
            echo "  --init         Also create project scaffolding for the language"
            echo ""
            echo "Examples:"
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
        *)
            echo "No specific help available for command: $command"
            echo "Use --help for general usage information."
            ;;
    esac
}

function list_templates() {
    echo "Available Dockerfile templates:"
    if [[ -d "$TEMPLATES_DIR" ]]; then
        local templates_found=false
        
        # Create a simple formatted list
        printf "  %-12s %s\\n" "Language" "Available Versions"
        printf "  %-12s %s\\n" "--------" "------------------"
        
        # Process templates dynamically by discovering all languages
        local all_languages=$(ls "$TEMPLATES_DIR"/Dockerfile-* 2>/dev/null | grep -v '\\.backup\\.' | sed 's/.*Dockerfile-//' | sed 's/-.*$//' | sort -u)
        
        for lang in $all_languages; do
            local versions=""
            local version_count=0
            
            # Find all versions for this language
            for template_path in "$TEMPLATES_DIR/Dockerfile-$lang"-*; do
                if [[ -f "$template_path" ]] && [[ "$template_path" != *".backup."* ]]; then
                    local template_name=$(basename "$template_path" | sed 's/Dockerfile-//')
                    local version="${template_name#*-}"
                    
                    if [[ $version_count -eq 0 ]]; then
                        versions="$version"
                    else
                        versions="$versions, $version"
                    fi
                    ((version_count++))
                    found_templates=true
                    templates_found=true
                fi
            done
            
            # Display if we found templates for this language
            if [[ $version_count -gt 0 ]]; then
                printf "  %-12s %s\\n" "$lang" "$versions"
            fi
        done
        
        if [[ "$templates_found" == false ]]; then
            echo "  (No templates found - run installer first)"
        fi
    else
        echo "  (Templates directory not found - run installer first)"
    fi
    exit 0
}