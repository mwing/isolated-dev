#!/bin/bash

# Install bash completion for dev command
# This script is called by install.sh

install_completion() {
    local src_dir="$1"
    local bash_completion="$src_dir/completions/dev-completion.bash"
    local zsh_completion="$src_dir/completions/dev-completion.zsh"
    
    # Detect current shell
    local current_shell=$(basename "$SHELL")
    
    if [[ "$current_shell" == "zsh" && -f "$zsh_completion" ]]; then
        # Install native zsh completion
        local zsh_completion_dir="$HOME/.zsh/completions"
        mkdir -p "$zsh_completion_dir"
        
        if cp "$zsh_completion" "$zsh_completion_dir/_dev" 2>/dev/null; then
            echo "✅ Zsh completion installed: $zsh_completion_dir/_dev"
            
            # Add to zshrc if not already present
            local zshrc="$HOME/.zshrc"
            if [[ -f "$zshrc" ]] && ! grep -q "fpath.*zsh/completions" "$zshrc"; then
                echo "" >> "$zshrc"
                echo "# Enable dev command completion" >> "$zshrc"
                echo "fpath=(~/.zsh/completions \$fpath)" >> "$zshrc"
                echo "autoload -U compinit && compinit" >> "$zshrc"
            fi
            
            echo "💡 Restart your shell or run: exec zsh"
            return 0
        fi
    fi
    
    # Fallback to bash completion
    if [[ ! -f "$bash_completion" ]]; then
        echo "❌ Completion files not found"
        return 1
    fi
    
    # Determine completion directory
    local completion_dir
    if [[ -d "$HOME/.local/share/bash-completion/completions" ]]; then
        completion_dir="$HOME/.local/share/bash-completion/completions"
    elif [[ -d "/usr/local/etc/bash_completion.d" ]]; then
        completion_dir="/usr/local/etc/bash_completion.d"
    elif [[ -d "/etc/bash_completion.d" ]]; then
        completion_dir="/etc/bash_completion.d"
    else
        # Create user completion directory
        completion_dir="$HOME/.local/share/bash-completion/completions"
        mkdir -p "$completion_dir"
    fi
    
    # Install bash completion
    local target_file="$completion_dir/dev"
    
    if cp "$bash_completion" "$target_file" 2>/dev/null; then
        echo "✅ Bash completion installed: $target_file"
        
        # Configure shell-specific setup
        if [[ "$current_shell" == "zsh" ]]; then
            # Zsh using bash completion
            local zshrc="$HOME/.zshrc"
            if [[ -f "$zshrc" ]] && ! grep -q "bashcompinit" "$zshrc"; then
                echo "" >> "$zshrc"
                echo "# Enable bash completion in zsh" >> "$zshrc"
                echo "autoload -U +X bashcompinit && bashcompinit" >> "$zshrc"
                echo "source $target_file" >> "$zshrc"
            fi
        else
            # Bash setup
            local bashrc="$HOME/.bashrc"
            if [[ -f "$bashrc" ]] && ! grep -q "bash-completion" "$bashrc"; then
                echo "" >> "$bashrc"
                echo "# Enable bash completion" >> "$bashrc"
                echo "[[ -r ~/.local/share/bash-completion/completions ]] && . ~/.local/share/bash-completion/completions/*" >> "$bashrc"
            fi
        fi
        
        echo "💡 Restart your shell or source your shell config"
        return 0
    else
        echo "⚠️  Could not install completion (permission denied)"
        echo "💡 Manual installation:"
        echo "   source $bash_completion"
        return 1
    fi
}

# Run if called directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    install_completion "$(dirname "$(dirname "${BASH_SOURCE[0]}")")"
fi