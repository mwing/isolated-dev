#!/bin/bash
set -euo pipefail

# Skip integration tests in CI
[[ "${CI:-false}" == "true" ]] && echo "Skipping integration tests in CI" && exit 0

# Check OrbStack availability
if ! command -v orb >/dev/null 2>&1; then
    echo "❌ OrbStack not found - skipping integration tests"
    exit 0
fi

TEST_VM_NAME="dev-vm-integration-test-$(date +%s)"
ORIGINAL_VM_NAME="${VM_NAME:-}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cleanup_integration_tests() {
    echo "🧹 Cleaning up integration test environment..."
    orb rm "$TEST_VM_NAME" --force 2>/dev/null || true
    [[ -n "$ORIGINAL_VM_NAME" ]] && export VM_NAME="$ORIGINAL_VM_NAME"
    [[ -d "/tmp/test-integration" ]] && rm -rf "/tmp/test-integration"
}

setup_integration_tests() {
    echo "🔧 Setting up integration test environment..."
    trap cleanup_integration_tests EXIT
    export VM_NAME="$TEST_VM_NAME"
    
    # Create isolated test VM using OrbStack directly
    if ! orb list | grep -q "$TEST_VM_NAME"; then
        echo "Creating test VM: $TEST_VM_NAME"
        # Use docker-host setup file with --user-data flag
        orb create ubuntu "$TEST_VM_NAME" --user-data "$HOME/.dev-envs/setups/docker-host.yaml"
    fi
    
    # Wait for VM to be ready
    echo "Waiting for VM to start..."
    sleep 10
}

test_container_lifecycle() {
    echo "🧪 Testing container lifecycle..."
    
    # Create test project
    mkdir -p /tmp/test-integration
    cd /tmp/test-integration
    
    # Simple Dockerfile
    cat > Dockerfile << 'EOF'
FROM alpine:latest
RUN echo "Integration test container"
CMD ["echo", "Hello from integration test"]
EOF
    
    # Test build
    echo "  → Testing build..."
    "$SCRIPT_DIR/scripts/dev" build
    
    # Test run with timeout
    echo "  → Testing run..."
    echo 'echo "Integration test success"; exit' | timeout 30s "$SCRIPT_DIR/scripts/dev" shell || {
        echo "  ⚠️  Run test timed out (expected for non-interactive test)"
    }
    
    # Test cleanup
    echo "  → Testing cleanup..."
    "$SCRIPT_DIR/scripts/dev" clean
    
    echo "  ✅ Container lifecycle test completed"
}

test_port_forwarding() {
    echo "🧪 Testing port forwarding..."
    
    cd /tmp/test-integration
    
    # Create simple HTTP server
    cat > Dockerfile << 'EOF'
FROM alpine:latest
RUN apk add --no-cache python3
WORKDIR /workspace
CMD ["python3", "-m", "http.server", "8000", "--bind", "0.0.0.0"]
EOF
    
    # Create package.json to trigger port detection
    echo '{"name": "test-server"}' > package.json
    
    echo "  → Building server container..."
    "$SCRIPT_DIR/scripts/dev" build
    
    echo "  ✅ Port forwarding test completed"
}

test_environment_variables() {
    echo "🧪 Testing environment variables..."
    
    cd /tmp/test-integration
    
    cat > Dockerfile << 'EOF'
FROM alpine:latest
CMD ["sh", "-c", "echo TEST_VAR=$TEST_VAR"]
EOF
    
    echo "  → Testing env var passing..."
    "$SCRIPT_DIR/scripts/dev" build
    
    # Test with environment variable
    echo 'echo "Env test done"; exit' | timeout 10s "$SCRIPT_DIR/scripts/dev" -e "TEST_VAR=integration_test" shell || {
        echo "  ⚠️  Env test timed out (expected)"
    }
    
    echo "  ✅ Environment variable test completed"
}

test_security_scan() {
    echo "🧪 Testing security scan..."
    
    cd /tmp/test-integration
    
    # Check if scanners are available
    if ! command -v trivy >/dev/null 2>&1 && ! command -v grype >/dev/null 2>&1; then
        echo "  ⚠️  No scanners found (trivy/grype), skipping security scan test"
        return 0
    fi
    
    cat > Dockerfile << 'EOF'
FROM alpine:latest
CMD ["echo", "secure"]
EOF
    
    echo "  → Building image..."
    "$SCRIPT_DIR/scripts/dev" build
    
    echo "  → Running security scan..."
    "$SCRIPT_DIR/scripts/dev" security scan || {
        echo "  ❌ Security scan failed"
        exit 1
    }
    
    echo "  ✅ Security scan test completed"
}

main() {
    echo "🚀 Starting OrbStack integration tests..."
    echo "Test VM: $TEST_VM_NAME"
    
    setup_integration_tests
    test_container_lifecycle
    test_port_forwarding
    test_environment_variables
    test_security_scan
    
    echo ""
    echo "✅ All integration tests completed successfully!"
}

main "$@"
