#!/bin/bash
set -euo pipefail

# Build script for creating self-contained installer
VERSION=${1:-"dev"}
VERSION_CLEAN=${VERSION#v}
DIST_DIR="dist"
BUILD_DIR="build"

echo "🔨 Building isolated-dev installer version $VERSION"

# Clean and create directories
rm -rf "$DIST_DIR" "$BUILD_DIR"
mkdir -p "$DIST_DIR" "$BUILD_DIR"

# Create clean copy of project (exclude build artifacts)
echo "📦 Preparing project files..."
rsync -av \
  --exclude='.git*' \
  --exclude='dist/' \
  --exclude='build/' \
  --exclude='*.log' \
  --exclude='.DS_Store' \
  --exclude='node_modules/' \
  --exclude='__pycache__/' \
  --exclude='*.pyc' \
  . "$BUILD_DIR/isolated-dev/"

# Create version file
echo "$VERSION_CLEAN" > "$BUILD_DIR/isolated-dev/VERSION"

# Create traditional tarball for manual installation
echo "📦 Creating tarball..."
cd "$BUILD_DIR"
tar czf "../$DIST_DIR/isolated-dev-$VERSION_CLEAN.tar.gz" isolated-dev/
cd ..

# Create self-extracting installer
echo "🚀 Building self-extracting installer..."
cat > "$DIST_DIR/install.sh" << 'INSTALLER_HEADER'
#!/bin/bash
# Isolated Development Environment Tools - Self-Extracting Installer
# Generated automatically - do not edit manually

set -euo pipefail

# Configuration
REPO_NAME="isolated-dev"
INSTALL_DIR="$HOME/.local/bin"
CONFIG_DIR="$HOME/.dev-envs"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() { echo -e "${BLUE}ℹ️  $1${NC}"; }
log_success() { echo -e "${GREEN}✅ $1${NC}"; }
log_warning() { echo -e "${YELLOW}⚠️  $1${NC}"; }
log_error() { echo -e "${RED}❌ $1${NC}"; }

# Parse command line arguments
FORCE=false
AUTO_YES=false
INSTALL_COMPLETION=false
UNINSTALL=false
UNINSTALL_ALL=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --force)
            FORCE=true
            shift
            ;;
        --yes|-y)
            AUTO_YES=true
            shift
            ;;
        --completion)
            INSTALL_COMPLETION=true
            shift
            ;;
        --uninstall)
            UNINSTALL=true
            shift
            ;;
        --uninstall-all)
            UNINSTALL_ALL=true
            shift
            ;;
        --help|-h)
            cat << 'EOF'
Isolated Development Environment Tools Installer

USAGE:
    curl -fsSL <url>/install.sh | bash [OPTIONS]

OPTIONS:
    --force         Force overwrite existing files
    --yes, -y       Skip all prompts (for automation)
    --completion    Install shell completion
    --uninstall     Remove installed files (preserve VMs)
    --uninstall-all Remove everything including VMs
    --help, -h      Show this help message

EXAMPLES:
    # Basic installation
    curl -fsSL <url>/install.sh | bash
    
    # Automated installation with completion
    curl -fsSL <url>/install.sh | bash -s -- --yes --completion
    
    # Force reinstall
    curl -fsSL <url>/install.sh | bash -s -- --force
EOF
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check macOS
    if [[ "$(uname)" != "Darwin" ]]; then
        log_error "This tool is designed for macOS only"
        exit 1
    fi
    
    # Check OrbStack
    if ! command -v orb >/dev/null 2>&1; then
        log_error "OrbStack is required but not installed"
        log_info "Please install OrbStack from: https://orbstack.dev/"
        exit 1
    fi
    
    # Check jq
    if ! command -v jq >/dev/null 2>&1; then
        log_warning "jq is required but not installed"
        if command -v brew >/dev/null 2>&1; then
            log_info "Installing jq via Homebrew..."
            brew install jq
        else
            log_error "Please install jq: brew install jq"
            exit 1
        fi
    fi
    
    log_success "Prerequisites check passed"
}

# Handle uninstallation
handle_uninstall() {
    if [[ "$UNINSTALL_ALL" == "true" ]]; then
        log_info "Removing all isolated-dev files and VMs..."
        
        # Remove VMs
        if command -v orb >/dev/null 2>&1; then
            for vm in $(orb list -f name | grep "^dev-vm-"); do
                log_info "Removing VM: $vm"
                orb delete "$vm" --force 2>/dev/null || true
            done
        fi
        
        # Remove all files
        rm -rf "$CONFIG_DIR"
        rm -f "$INSTALL_DIR/dev" "$INSTALL_DIR/dev-env"
        
        log_success "Complete uninstallation finished"
        
    elif [[ "$UNINSTALL" == "true" ]]; then
        log_info "Removing isolated-dev files (preserving VMs)..."
        
        # Remove scripts and config
        rm -f "$INSTALL_DIR/dev" "$INSTALL_DIR/dev-env"
        rm -rf "$CONFIG_DIR/languages" "$CONFIG_DIR/templates" "$CONFIG_DIR/lib"
        
        log_success "Uninstallation finished (VMs preserved)"
        log_info "To remove VMs manually: orb delete <vm-name>"
    fi
    
    exit 0
}

# Extract embedded archive
extract_files() {
    log_info "Extracting installation files..."
    
    TEMP_DIR=$(mktemp -d)
    trap "rm -rf '$TEMP_DIR'" EXIT
    
    # Find the start of the embedded archive
    ARCHIVE_LINE=$(awk '/^__ARCHIVE_BELOW__/ {print NR + 1; exit 0; }' "$0")
    
    if [[ -z "$ARCHIVE_LINE" ]]; then
        log_error "Could not find embedded archive"
        exit 1
    fi
    
    # Extract the archive
    tail -n +"$ARCHIVE_LINE" "$0" | base64 -d | tar xz -C "$TEMP_DIR"
    
    if [[ ! -d "$TEMP_DIR/isolated-dev" ]]; then
        log_error "Failed to extract installation files"
        exit 1
    fi
    
    echo "$TEMP_DIR/isolated-dev"
}

# Main installation function
main() {
    log_info "🚀 Starting Isolated Development Environment Tools installation"
    
    # Handle uninstall requests
    if [[ "$UNINSTALL" == "true" || "$UNINSTALL_ALL" == "true" ]]; then
        handle_uninstall
    fi
    
    # Check prerequisites
    check_prerequisites
    
    # Extract files
    EXTRACTED_DIR=$(extract_files)
    
    # Change to extracted directory and run installer
    cd "$EXTRACTED_DIR"
    
    # Build installer arguments
    INSTALLER_ARGS=()
    [[ "$FORCE" == "true" ]] && INSTALLER_ARGS+=(--force)
    [[ "$AUTO_YES" == "true" ]] && INSTALLER_ARGS+=(--yes)
    [[ "$INSTALL_COMPLETION" == "true" ]] && INSTALLER_ARGS+=(--completion)
    
    # Run the actual installer
    log_info "Running installer..."
    ./install.sh "${INSTALLER_ARGS[@]}"
    
    log_success "🎉 Installation completed successfully!"
    log_info "Run 'dev --help' to get started"
}

# Run main function
main "$@"

# Archive marker - do not remove this line
__ARCHIVE_BELOW__
INSTALLER_HEADER

# Append the base64-encoded archive
echo "📦 Embedding project archive..."
cd "$BUILD_DIR"
tar czf - isolated-dev/ | base64 >> "../$DIST_DIR/install.sh"
cd ..

# Make installer executable
chmod +x "$DIST_DIR/install.sh"

# Verify the installer
echo "🔍 Verifying installer..."
INSTALLER_SIZE=$(wc -c < "$DIST_DIR/install.sh")
ARCHIVE_SIZE=$(wc -c < "$DIST_DIR/isolated-dev-$VERSION_CLEAN.tar.gz")

log_success() { echo -e "\033[0;32m✅ $1\033[0m"; }
log_info() { echo -e "\033[0;34mℹ️  $1\033[0m"; }

log_success "Build completed successfully!"
log_info "Self-extracting installer: $DIST_DIR/install.sh ($(numfmt --to=iec $INSTALLER_SIZE))"
log_info "Traditional tarball: $DIST_DIR/isolated-dev-$VERSION_CLEAN.tar.gz ($(numfmt --to=iec $ARCHIVE_SIZE))"
log_info ""
log_info "Test installation with:"
log_info "  $DIST_DIR/install.sh --help"
log_info ""
log_info "One-line installation command:"
log_info "  curl -fsSL <url>/install.sh | bash"

# Clean up build directory
rm -rf "$BUILD_DIR"

echo "🎉 Build process completed!"