#!/bin/bash
set -e

# Script version
VERSION="1.0.0"

# Define source and destination directories
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$HOME/.local/bin"
CONFIG_DIR="$HOME/.dev-envs/setups"
LANGUAGES_DIR="$HOME/.dev-envs/languages"

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
    -h, --help       Show this help message
    -f, --force      Force installation, overwriting existing files
    -q, --quiet      Quiet mode, minimal output
    -y, --yes        Automatically answer 'yes' to all prompts (for automation)
    --completion     Install bash completion (optional)
    --version        Show version information
    --uninstall      Remove all installed files and directories
    --uninstall-all  Remove everything including VMs (DESTRUCTIVE)

This script installs the isolated development environment tools to your system.
EOF
}

# Parse command line arguments
FORCE=false
QUIET=false
UNINSTALL=false
UNINSTALL_ALL=false
AUTO_YES=false
INSTALL_COMPLETION=false

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
        --uninstall)
            UNINSTALL=true
            shift
            ;;
        --uninstall-all)
            UNINSTALL_ALL=true
            shift
            ;;
        -y|--yes)
            AUTO_YES=true
            shift
            ;;
        --completion)
            INSTALL_COMPLETION=true
            shift
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

# Uninstall functions
uninstall_files() {
    log "${YELLOW}🗑️  Uninstalling isolated development environment...${NC}"
    
    # Remove scripts
    log "   -> Removing scripts from $BIN_DIR"
    rm -f "$BIN_DIR/devenv" "$BIN_DIR/dev"
    
    # Remove entire .dev-envs directory
    if [[ -d "$HOME/.dev-envs" ]]; then
        log "   -> Removing configuration directory ~/.dev-envs"
        rm -rf "$HOME/.dev-envs"
    fi
    
    log "${GREEN}✅ Files and directories removed successfully${NC}"
}

uninstall_with_vms() {
    log "${RED}🔥 DESTRUCTIVE UNINSTALL: Removing everything including VMs...${NC}"
    
    # First remove files
    uninstall_files
    
    # List and remove all dev VMs
    log "   -> Searching for development VMs..."
    local vms_found=false
    
    # Check for VMs that match our naming pattern
    if command -v orb >/dev/null 2>&1; then
        while IFS= read -r vm_name; do
            if [[ "$vm_name" =~ ^dev-vm- ]]; then
                log "   -> Found VM: $vm_name"
                vms_found=true
                if [[ "$AUTO_YES" == "true" ]]; then
                    log "     Auto-confirming VM deletion (--yes flag set)"
                    log "     -> Deleting VM: $vm_name"
                    orb delete "$vm_name" 2>/dev/null || log "     -> Warning: Failed to delete $vm_name"
                else
                    echo -n "     Delete VM '$vm_name'? (y/N): "
                    read -r response
                    if [[ "$response" =~ ^[Yy]$ ]]; then
                        log "     -> Deleting VM: $vm_name"
                        orb delete "$vm_name" 2>/dev/null || log "     -> Warning: Failed to delete $vm_name"
                    else
                        log "     -> Skipped: $vm_name"
                    fi
                fi
            fi
        done < <(orb list -q 2>/dev/null || echo "")
        
        if [[ "$vms_found" == false ]]; then
            log "   -> No development VMs found"
        fi
    else
        log "   -> OrbStack not found, skipping VM cleanup"
    fi
    
    log "${GREEN}✅ Complete uninstallation finished${NC}"
}

confirm_uninstall() {
    local uninstall_type="$1"
    
    echo -e "${YELLOW}⚠️  WARNING: This will remove the isolated development environment.${NC}"
    
    if [[ "$uninstall_type" == "all" ]]; then
        echo -e "${RED}⚠️  DESTRUCTIVE: This will also delete all development VMs and their data!${NC}"
        echo ""
        echo "This will remove:"
        echo "  - Script: dev"
        echo "  - Configuration directory: ~/.dev-envs"
        echo "  - All development VMs (dev-vm-*)"
        echo "  - All VM data and containers"
        echo ""
        echo -e "${RED}This action cannot be undone!${NC}"
    else
        echo ""
        echo "This will remove:"
        echo "  - Script: dev"  
        echo "  - Configuration directory: ~/.dev-envs"
        echo "  - Templates and setup files"
        echo ""
        echo "VMs will be left running and can be managed manually with 'orb' commands."
    fi
    
    echo ""
    if [[ "$AUTO_YES" == "true" ]]; then
        log "Auto-confirming uninstall (--yes flag set)"
    else
        echo -n "Are you sure you want to proceed? (y/N): "
        read -r response
        
        if [[ ! "$response" =~ ^[Yy]$ ]]; then
            log "Uninstall cancelled."
            exit 0
        fi
    fi
}

# Handle uninstall modes
if [[ "$UNINSTALL" == true ]] || [[ "$UNINSTALL_ALL" == true ]]; then
    if [[ "$UNINSTALL_ALL" == true ]]; then
        confirm_uninstall "all"
        uninstall_with_vms
    else
        confirm_uninstall "files"
        uninstall_files
    fi
    exit 0
fi

log "${BLUE}🚀 Installing isolated development environment scripts...${NC}"

# Validate source files exist
log "   -> Validating source files..."
# Source files validation
[[ -f "$SRC_DIR/scripts/dev" ]] || error_exit "Source file scripts/dev not found"
[[ -f "$SRC_DIR/config/docker-host.yaml" ]] || error_exit "Source file config/docker-host.yaml not found"
[[ -d "$SRC_DIR/languages" ]] || error_exit "Source directory languages not found"

# Create destination directories if they don't exist
log "   -> Creating destination directories..."
mkdir -p "$BIN_DIR" || error_exit "Failed to create directory $BIN_DIR"
mkdir -p "$CONFIG_DIR" || error_exit "Failed to create directory $CONFIG_DIR"

mkdir -p "$LANGUAGES_DIR" || error_exit "Failed to create directory $LANGUAGES_DIR"

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
backup_existing "$BIN_DIR/dev"
# Clean up any old devenv script
rm -f "$BIN_DIR/devenv"

cp "$SRC_DIR/scripts/dev" "$BIN_DIR/" || error_exit "Failed to copy dev"
chmod +x "$BIN_DIR/dev" || error_exit "Failed to make dev executable"

# Install lib directory
log "   -> Installing lib directory to $BIN_DIR"
mkdir -p "$BIN_DIR/lib" || error_exit "Failed to create lib directory"
cp "$SRC_DIR/scripts/lib/"*.sh "$BIN_DIR/lib/" || error_exit "Failed to copy lib files"

# Install config
log "   -> Installing config to $CONFIG_DIR"
backup_existing "$CONFIG_DIR/docker-host.yaml"
cp "$SRC_DIR/config/docker-host.yaml" "$CONFIG_DIR/docker-host.yaml" || error_exit "Failed to copy config file"



# Install language plugins
log "   -> Installing language plugins to $LANGUAGES_DIR"
if [[ -d "$SRC_DIR/languages" ]]; then
    cp -r "$SRC_DIR/languages"/* "$LANGUAGES_DIR/" || error_exit "Failed to copy language plugins"
else
    log "   -> Warning: No languages directory found, skipping language plugins"
fi

# Create global configuration file with defaults
log "   -> Creating global configuration file..."
GLOBAL_CONFIG="$HOME/.dev-envs/config.yaml"
if [[ ! -f "$GLOBAL_CONFIG" ]] || [[ "$FORCE" == "true" ]]; then
    if [[ -f "$GLOBAL_CONFIG" ]]; then
        backup_existing "$GLOBAL_CONFIG"
    fi
    
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

# Environment variables to pass to containers
pass_env_vars:
  # Patterns to match (supports wildcards with *)
  patterns:
    - AWS_*
    - SNYK_*
    - GITHUB_*
    - NODE_ENV
    - DEBUG
  # Explicit variable names (no wildcards)
  explicit: []
EOF
    log "   -> Created global config: $GLOBAL_CONFIG"
else
    log "   -> Global config already exists: $GLOBAL_CONFIG"
fi

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
            if [[ "$AUTO_YES" == "true" ]]; then
                log "Auto-adding to PATH (--yes flag set)"
                echo "" >> "$shell_profile"
                echo "# Added by isolated-dev installer" >> "$shell_profile"
                echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$shell_profile"
                log "${GREEN}✅ Added $BIN_DIR to PATH in $shell_profile${NC}"
                log "${YELLOW}Please restart your terminal or run: source $shell_profile${NC}"
            else
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
    [[ -x "$BIN_DIR/dev" ]] || error_exit "dev is not executable"
    [[ -f "$CONFIG_DIR/docker-host.yaml" ]] || error_exit "Config file not found"
    [[ -d "$LANGUAGES_DIR" ]] || error_exit "Languages directory not found"
    language_count=$(find "$LANGUAGES_DIR" -name "language.yaml" | wc -l)
    [[ $language_count -gt 0 ]] || error_exit "No language plugins found"
    log "${GREEN}✅ All files installed successfully (including $language_count language plugins)${NC}"
}

# Install shell completion if requested
if [[ "$INSTALL_COMPLETION" == "true" && -f "$SRC_DIR/completions/install-completion.sh" ]]; then
    current_shell=$(basename "$SHELL")
    log "   -> Installing $current_shell completion..."
    bash "$SRC_DIR/completions/install-completion.sh" "$SRC_DIR"
fi

# Main installation flow
verify_installation
setup_path

log ""
log "${GREEN}🎉 Installation complete!${NC}"
log "${BLUE}You can now run 'dev' from your terminal.${NC}"

# Show quick usage
if [[ "$QUIET" == false ]]; then
    log ""
    log "${BLUE}Quick start:${NC}"
    log "  dev env new docker-host # Create Docker host VM (one-time setup)"
    log "  dev --help             # Show all available commands"
    log "  dev env list           # Show environment management help"
    log ""
    log "${BLUE}Optional:${NC}"
    current_shell=$(basename "$SHELL")
    log "  ./install.sh --completion  # Install $current_shell completion for tab completion"
fi
