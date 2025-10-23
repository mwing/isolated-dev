#!/bin/bash
# Test script for the self-extracting installer

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}ℹ️  $1${NC}"; }
log_success() { echo -e "${GREEN}✅ $1${NC}"; }
log_warning() { echo -e "${YELLOW}⚠️  $1${NC}"; }
log_error() { echo -e "${RED}❌ $1${NC}"; }

# Test configuration
TEST_DIR=$(mktemp -d)
INSTALLER_PATH="dist/install.sh"

cleanup() {
    log_info "Cleaning up test environment..."
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT

run_test() {
    local test_name="$1"
    local test_command="$2"
    
    log_info "Running test: $test_name"
    
    if eval "$test_command"; then
        log_success "Test passed: $test_name"
        return 0
    else
        log_error "Test failed: $test_name"
        return 1
    fi
}

# Main test suite
main() {
    log_info "🧪 Testing isolated-dev installer"
    
    # Check if installer exists
    if [[ ! -f "$INSTALLER_PATH" ]]; then
        log_error "Installer not found at $INSTALLER_PATH"
        log_info "Run './scripts/build-installer.sh' first"
        exit 1
    fi
    
    # Test 1: Installer is executable
    run_test "Installer executable" "[[ -x '$INSTALLER_PATH' ]]"
    
    # Test 2: Help command works
    run_test "Help command" "$INSTALLER_PATH --help | grep -q 'Isolated Development Environment Tools Installer'"
    
    # Test 3: Version extraction works
    run_test "Archive marker exists" "grep -q '__ARCHIVE_BELOW__' '$INSTALLER_PATH'"
    
    # Test 4: Base64 content exists after marker
    run_test "Base64 content exists" "tail -n +\$(awk '/^__ARCHIVE_BELOW__/ {print NR + 1; exit 0; }' '$INSTALLER_PATH') '$INSTALLER_PATH' | head -1 | base64 -d >/dev/null 2>&1"
    
    # Test 5: Dry run extraction (without actual installation)
    log_info "Testing archive extraction..."
    ARCHIVE_LINE=$(awk '/^__ARCHIVE_BELOW__/ {print NR + 1; exit 0; }' "$INSTALLER_PATH")
    if [[ -n "$ARCHIVE_LINE" ]]; then
        tail -n +"$ARCHIVE_LINE" "$INSTALLER_PATH" | base64 -d | tar tz | head -5
        log_success "Archive extraction test passed"
    else
        log_error "Could not find archive marker"
        exit 1
    fi
    
    # Test 6: Check installer size is reasonable
    INSTALLER_SIZE=$(wc -c < "$INSTALLER_PATH")
    if [[ $INSTALLER_SIZE -gt 1000000 ]]; then  # > 1MB
        log_warning "Installer is quite large: $(numfmt --to=iec $INSTALLER_SIZE)"
    else
        log_success "Installer size is reasonable: $(numfmt --to=iec $INSTALLER_SIZE)"
    fi
    
    log_success "🎉 All installer tests passed!"
    log_info ""
    log_info "To test installation manually:"
    log_info "  $INSTALLER_PATH --help"
    log_info ""
    log_info "To simulate one-line installation:"
    log_info "  cat $INSTALLER_PATH | bash -s -- --help"
}

main "$@"