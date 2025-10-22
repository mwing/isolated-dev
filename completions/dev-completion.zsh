#compdef dev

# Zsh completion for dev command

_dev() {
    local context state line
    typeset -A opt_args

    # Get available language templates dynamically
    local languages_dir="$HOME/.dev-envs/languages"
    local templates=()
    
    if [[ -d "$languages_dir" ]]; then
        for lang_dir in "$languages_dir"/*; do
            if [[ -d "$lang_dir" && -f "$lang_dir/language.yaml" ]]; then
                local lang_name=$(basename "$lang_dir")
                local versions=$(grep "^versions:" "$lang_dir/language.yaml" 2>/dev/null | sed 's/versions: *\[\(.*\)\]/\1/' | tr -d '"' | tr ',' ' ')
                if [[ -n "$versions" ]]; then
                    for version in ${=versions}; do
                        templates+=("$lang_name-$version")
                    done
                else
                    templates+=("$lang_name")
                fi
            fi
        done
    fi

    _arguments -C \
        '1: :->commands' \
        '*: :->args' && return 0

    case $state in
        commands)
            local commands=(
                'new:Create new project from template'
                'list:List available templates'
                'config:Configuration management'
                'security:Security commands'
                'templates:Template management'
                'env:Environment management'
                'devcontainer:VS Code devcontainer generation'
                'run:Build and run container'
                'shell:Open interactive shell'
                'build:Build image only'
                'clean:Remove containers and images'
                'help:Show help'
                'troubleshoot:Show troubleshooting guide'
                'arch:Show architecture information'
            )
            _describe 'commands' commands
            _arguments \
                '--help[Show help]' \
                '--yes[Skip prompts]' \
                '--platform[Target platform]:platform:(linux/amd64 linux/arm64)'
            ;;
        args)
            case $words[2] in
                new)
                    _arguments \
                        '--init[Create project scaffolding]' \
                        '--devcontainer[Generate VS Code config]' \
                        '--yes[Skip prompts]' \
                        '--help[Show help]' \
                        "*:template:(${templates[@]})"
                    ;;
                config)
                    _arguments \
                        '--edit[Edit global configuration]' \
                        '--init[Create project configuration]' \
                        '--yes[Skip prompts]' \
                        '--help[Show help]' \
                        '1:action:(validate)'
                    ;;
                security)
                    _arguments \
                        '--help[Show help]' \
                        '1:action:(scan validate)'
                    ;;
                templates)
                    _arguments \
                        '--help[Show help]' \
                        '1:action:(update check prune cleanup stats)' \
                        '2:days:(30 60 90)'
                    ;;
                env)
                    local vm_names=("docker-host")
                    # Try to get VM names from OrbStack
                    if (( $+commands[orb] )); then
                        local orb_vms=(${(f)"$(orb list 2>/dev/null | tail -n +2 | awk '{print $1}' 2>/dev/null)"})
                        if [[ ${#orb_vms[@]} -gt 0 ]]; then
                            vm_names=($orb_vms)
                        fi
                    fi
                    
                    _arguments \
                        '--help[Show help]' \
                        '--yes[Skip prompts]' \
                        '1:action:(list new up down status rm)' \
                        "*:vm_name:(${vm_names[@]})"
                    ;;
                devcontainer)
                    _arguments \
                        '--yes[Skip prompts]' \
                        '--help[Show help]' \
                        '--file[Dockerfile path]:file:_files' \
                        "*:template:(${templates[@]})"
                    ;;
                help)
                    local help_topics=(new config security templates env devcontainer)
                    _arguments "1:topic:(${help_topics[@]})"
                    ;;
                *)
                    _arguments \
                        '--help[Show help]' \
                        '--yes[Skip prompts]' \
                        '--platform[Target platform]:platform:(linux/amd64 linux/arm64)' \
                        '--file[Dockerfile path]:file:_files' \
                        '--tag[Image tag]:tag:' \
                        '--name[Container name]:name:'
                    ;;
            esac
            ;;
    esac
}

_dev "$@"