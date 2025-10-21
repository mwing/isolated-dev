#!/bin/bash
set -e

# Script version
VERSION="1.0.0"

# Define source and destination directories
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$HOME/.local/bin"
CONFIG_DIR="$HOME/.dev-envs/setups"
TEMPLATES_DIR="$HOME/.dev-envs/templates"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Help function
show_help() {
    cat << EOF
Isolated Development Environment Installer v$VERSION

Usage: $0 [OPTIONS]

Options:
    -h, --help     Show this help message
    -f, --force    Force installation, overwriting existing files
    -q, --quiet    Quiet mode, minimal output
    --version      Show version information

This script installs the isolated development environment tools to your system.
EOF
}

# Parse command line arguments
FORCE=false
QUIET=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_help
            exit 0
            ;;
        -f|--force)
            FORCE=true
            shift
            ;;
        -q|--quiet)
            QUIET=true
            shift
            ;;
        --version)
            echo "Isolated Development Environment Installer v$VERSION"
            exit 0
            ;;
        *)
            echo -e "${RED}Error: Unknown option $1${NC}" >&2
            show_help
            exit 1
            ;;
    esac
done

# Logging function
log() {
    if [[ "$QUIET" == false ]]; then
        echo -e "$1"
    fi
}

# Error handling function
error_exit() {
    echo -e "${RED}Error: $1${NC}" >&2
    exit 1
}

log "${BLUE}🚀 Installing isolated development environment scripts...${NC}"

# Validate source files exist
log "   -> Validating source files..."
[[ -f "$SRC_DIR/scripts/env-ctl" ]] || error_exit "Source file scripts/env-ctl not found"
[[ -f "$SRC_DIR/scripts/dev-container" ]] || error_exit "Source file scripts/dev-container not found"
[[ -f "$SRC_DIR/config/docker-host.yaml" ]] || error_exit "Source file config/docker-host.yaml not found"
[[ -d "$SRC_DIR/templates" ]] || error_exit "Source directory templates not found"

# Create destination directories if they don't exist
log "   -> Creating destination directories..."
mkdir -p "$BIN_DIR" || error_exit "Failed to create directory $BIN_DIR"
mkdir -p "$CONFIG_DIR" || error_exit "Failed to create directory $CONFIG_DIR"
mkdir -p "$TEMPLATES_DIR" || error_exit "Failed to create directory $TEMPLATES_DIR"

# Cleanup old backup files (keep only the 3 most recent)
cleanup_old_backups() {
    local file="$1"
    local backup_pattern="${file}.backup.*"
    
    # Find all backup files for this specific file, sort by modification time, keep newest 3
    find "$(dirname "$file")" -name "$(basename "$file").backup.*" -type f 2>/dev/null | \
        sort -t. -k3 -r | \
        tail -n +4 | \
        while IFS= read -r old_backup; do
            log "   -> Cleaning up old backup: $(basename "$old_backup")"
            rm -f "$old_backup"
        done
}

# Check for existing installations and backup if needed
backup_existing() {
    local file="$1"
    if [[ -f "$file" && "$FORCE" == false ]]; then
        # Clean up old backups before creating a new one
        cleanup_old_backups "$file"
        
        local backup="${file}.backup.$(date +%Y%m%d_%H%M%S)"
        log "   -> Backing up existing file: $(basename "$file") -> $(basename "$backup")"
        cp "$file" "$backup" || error_exit "Failed to backup $file"
    fi
}

# Install scripts
log "   -> Installing scripts to $BIN_DIR"
backup_existing "$BIN_DIR/env-ctl"
backup_existing "$BIN_DIR/dev-container"

cp "$SRC_DIR/scripts/env-ctl" "$BIN_DIR/" || error_exit "Failed to copy env-ctl"
cp "$SRC_DIR/scripts/dev-container" "$BIN_DIR/" || error_exit "Failed to copy dev-container"
chmod +x "$BIN_DIR/env-ctl" || error_exit "Failed to make env-ctl executable"
chmod +x "$BIN_DIR/dev-container" || error_exit "Failed to make dev-container executable"

# Install config
log "   -> Installing config to $CONFIG_DIR"
backup_existing "$CONFIG_DIR/docker-host.yaml"
cp "$SRC_DIR/config/docker-host.yaml" "$CONFIG_DIR/docker-host.yaml" || error_exit "Failed to copy config file"

# Install templates
log "   -> Installing templates to $TEMPLATES_DIR"
for template in "$SRC_DIR/templates"/*; do
    if [[ -f "$template" ]]; then
        template_name=$(basename "$template")
        backup_existing "$TEMPLATES_DIR/$template_name"
        cp "$template" "$TEMPLATES_DIR/$template_name" || error_exit "Failed to copy template $template_name"
    fi
done

# Copy the cloud-init config file
log "   -> Installing config to $CONFIG_DIR"
backup_existing "$CONFIG_DIR/docker-host.yaml"
cp "$SRC_DIR/config/docker-host.yaml" "$CONFIG_DIR/docker-host.yaml" || error_exit "Failed to copy config file"

# Detect shell and appropriate profile file
detect_shell_profile() {
    if [[ -n "$ZSH_VERSION" ]]; then
        echo "$HOME/.zshrc"
    elif [[ -n "$BASH_VERSION" ]]; then
        if [[ -f "$HOME/.bash_profile" ]]; then
            echo "$HOME/.bash_profile"
        else
            echo "$HOME/.bashrc"
        fi
    else
        # Fallback to checking what files exist
        if [[ -f "$HOME/.zshrc" ]]; then
            echo "$HOME/.zshrc"
        elif [[ -f "$HOME/.bash_profile" ]]; then
            echo "$HOME/.bash_profile"
        else
            echo "$HOME/.bashrc"
        fi
    fi
}

# Check and setup PATH
setup_path() {
    local shell_profile
    shell_profile=$(detect_shell_profile)
    
    if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
        log ""
        log "${YELLOW}⚠️  WARNING: Your PATH does not include $BIN_DIR.${NC}"
        
        if [[ "$QUIET" == false ]]; then
            echo -e "${BLUE}Would you like to automatically add it to your PATH? (y/N):${NC} \c"
            read -r response
            if [[ "$response" =~ ^[Yy]$ ]]; then
                echo "" >> "$shell_profile"
                echo "# Added by isolated-dev installer" >> "$shell_profile"
                echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$shell_profile"
                log "${GREEN}✅ Added $BIN_DIR to PATH in $shell_profile${NC}"
                log "${YELLOW}Please restart your terminal or run: source $shell_profile${NC}"
            else
                log "${YELLOW}Please manually add the following line to your $shell_profile:${NC}"
                log '    export PATH="$HOME/.local/bin:$PATH"'
                log "${YELLOW}Then restart your terminal for the changes to take effect.${NC}"
            fi
        else
            log "${YELLOW}Please add the following line to your $shell_profile:${NC}"
            log '    export PATH="$HOME/.local/bin:$PATH"'
        fi
    else
        log ""
        log "${GREEN}✅ $BIN_DIR is already in your PATH${NC}"
    fi
}

# Verify installation
verify_installation() {
    log "   -> Verifying installation..."
    [[ -x "$BIN_DIR/env-ctl" ]] || error_exit "env-ctl is not executable"
    [[ -x "$BIN_DIR/dev-container" ]] || error_exit "dev-container is not executable"
    [[ -f "$CONFIG_DIR/docker-host.yaml" ]] || error_exit "Config file not found"
    [[ -d "$TEMPLATES_DIR" ]] || error_exit "Templates directory not found"
    local template_count=$(find "$TEMPLATES_DIR" -name "Dockerfile-*" | wc -l)
    [[ $template_count -gt 0 ]] || error_exit "No Dockerfile templates found"
    log "${GREEN}✅ All files installed successfully (including $template_count templates)${NC}"
}

# Main installation flow
verify_installation
setup_path

log ""
log "${GREEN}🎉 Installation complete!${NC}"
log "${BLUE}You can now run 'env-ctl' and 'dev-container' from your terminal.${NC}"

# Show quick usage
if [[ "$QUIET" == false ]]; then
    log ""
    log "${BLUE}Quick start:${NC}"
    log "  env-ctl --help      # Show environment control options"
    log "  dev-container --help # Show container management options"
fi