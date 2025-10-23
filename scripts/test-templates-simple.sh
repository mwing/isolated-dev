#!/bin/bash
set -euo pipefail

# Simple template validation test

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}ℹ️  $1${NC}"; }
log_success() { echo -e "${GREEN}✅ $1${NC}"; }
log_error() { echo -e "${RED}❌ $1${NC}"; }

cd "$(dirname "$0")/.."

# Test projects
TEST_PROJECTS=("test-python" "test-node" "test-golang" "test-rust" "test-java" "test-php")

ensure_vm_running() {
    if ! orb status "dev-vm-docker-host" 2>/dev/null | grep -q "running"; then
        log_info "Starting VM..."
        orb start "dev-vm-docker-host"
    fi
}

test_project() {
    local project="$1"
    log_info "Testing $project"
    
    cd "test/$project"
    
    # Determine language version to use
    local lang_version
    case "$project" in
        "test-python") lang_version="python-3.13" ;;
        "test-node") lang_version="node-22" ;;
        "test-golang") lang_version="golang-1.22" ;;
        "test-rust") lang_version="rust-1.90" ;;
        "test-java") lang_version="java-21" ;;
        "test-php") lang_version="php-8.3" ;;
        *) lang_version="ubuntu-22.04" ;;
    esac
    
    # Generate Dockerfile
    if ! bash ../../scripts/dev new "$lang_version" --yes >/dev/null 2>&1; then
        log_error "$project: Dockerfile generation failed"
        cd ../..
        return 1
    fi
    
    # Check user setup (skip for Node.js which uses existing node user)
    if [[ "$project" != "test-node" ]]; then
        if ! grep -q "useradd.*-u 1000.*appuser" Dockerfile && ! grep -q "adduser.*-u 1000.*appuser" Dockerfile; then
            log_error "$project: Missing proper user setup"
            cd ../..
            return 1
        fi
    fi
    
    # Build container
    if ! orb -m "dev-vm-docker-host" sudo docker build -t "test-$project" . >/dev/null 2>&1; then
        log_error "$project: Build failed"
        cd ../..
        return 1
    fi
    
    # Test user in container
    local user_test
    user_test=$(orb -m "dev-vm-docker-host" sudo docker run --rm "test-$project" bash -c "whoami && id -u" 2>/dev/null || echo "FAILED")
    
    if ! echo "$user_test" | grep -q "1000" || ! (echo "$user_test" | grep -q "appuser\|node"); then
        log_error "$project: User test failed - got: $user_test"
        cd ../..
        return 1
    fi
    
    # Cleanup
    orb -m "dev-vm-docker-host" sudo docker rmi "test-$project" >/dev/null 2>&1 || true
    rm -f Dockerfile
    
    log_success "$project: All tests passed"
    cd ../..
    return 0
}

main() {
    log_info "🧪 Testing language templates"
    
    ensure_vm_running
    
    local passed=0
    local failed=0
    
    for project in "${TEST_PROJECTS[@]}"; do
        if test_project "$project"; then
            ((passed++))
        else
            ((failed++))
        fi
        echo ""
    done
    
    log_info "Results: $passed passed, $failed failed"
    
    if [[ $failed -eq 0 ]]; then
        log_success "🎉 All tests passed!"
        exit 0
    else
        log_error "Some tests failed"
        exit 1
    fi
}

main "$@"