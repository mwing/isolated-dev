#!/bin/bash

# Bash completion for dev command
# Usage: source this file or install via install.sh

_dev_completion() {
    local cur prev words cword
    
    # Portable completion initialization
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    words=("${COMP_WORDS[@]}")
    cword=$COMP_CWORD

    # Get available language templates dynamically
    local languages_dir="$HOME/.dev-envs/languages"
    local templates=()
    
    if [[ -d "$languages_dir" ]]; then
        for lang_dir in "$languages_dir"/*; do
            if [[ -d "$lang_dir" && -f "$lang_dir/language.yaml" ]]; then
                local lang_name=$(basename "$lang_dir")
                local versions=$(grep "^versions:" "$lang_dir/language.yaml" 2>/dev/null | sed 's/versions: *\[\(.*\)\]/\1/' | tr -d '"' | tr ',' ' ')
                if [[ -n "$versions" ]]; then
                    for version in $versions; do
                        templates+=("$lang_name-$version")
                    done
                else
                    templates+=("$lang_name")
                fi
            fi
        done
    fi

    # Main commands
    local commands="new list config security templates env help troubleshoot arch devcontainer run shell build clean"
    
    # Global flags
    local global_flags="--help -h --yes -y --platform"
    
    case $prev in
        dev)
            COMPREPLY=($(compgen -W "$commands $global_flags" -- "$cur"))
            return 0
            ;;
        new)
            local new_flags="--init --devcontainer --yes --help"
            COMPREPLY=($(compgen -W "${templates[*]} $new_flags" -- "$cur"))
            return 0
            ;;
        config)
            local config_opts="--edit --init --yes validate --help"
            COMPREPLY=($(compgen -W "$config_opts" -- "$cur"))
            return 0
            ;;
        security)
            local security_opts="scan validate --help"
            COMPREPLY=($(compgen -W "$security_opts" -- "$cur"))
            return 0
            ;;
        templates)
            local template_opts="update check prune cleanup stats --help"
            COMPREPLY=($(compgen -W "$template_opts" -- "$cur"))
            return 0
            ;;
        env)
            local env_opts="list new up down status rm --help"
            COMPREPLY=($(compgen -W "$env_opts" -- "$cur"))
            return 0
            ;;
        devcontainer)
            local devcontainer_flags="--yes --help -f --file"
            COMPREPLY=($(compgen -W "${templates[*]} $devcontainer_flags" -- "$cur"))
            return 0
            ;;
        help)
            COMPREPLY=($(compgen -W "$commands" -- "$cur"))
            return 0
            ;;
        --platform)
            COMPREPLY=($(compgen -W "linux/amd64 linux/arm64" -- "$cur"))
            return 0
            ;;
        -f|--file)
            COMPREPLY=($(compgen -f -- "$cur"))
            return 0
            ;;
        *)
            # Handle subcommands
            if [[ ${#words[@]} -ge 3 ]]; then
                case "${words[1]}" in
                    env)
                        case "${words[2]}" in
                            new|up|down|status|rm)
                                local vm_names="docker-host"
                                # Try to get VM names from OrbStack
                                if command -v orb >/dev/null 2>&1; then
                                    local orb_vms=$(orb list 2>/dev/null | tail -n +2 | awk '{print $1}' 2>/dev/null || echo "")
                                    if [[ -n "$orb_vms" ]]; then
                                        vm_names="$orb_vms"
                                    fi
                                fi
                                COMPREPLY=($(compgen -W "$vm_names --yes --help" -- "$cur"))
                                ;;
                        esac
                        ;;
                    templates)
                        case "${words[2]}" in
                            cleanup)
                                COMPREPLY=($(compgen -W "--yes" -- "$cur"))
                                ;;
                        esac
                        ;;
                esac
            fi
            
            # Default to global flags
            COMPREPLY=($(compgen -W "$global_flags" -- "$cur"))
            ;;
    esac
}

# Register completion
complete -F _dev_completion dev