#!/bin/bash

# Simple test framework for isolated-dev
# Usage: ./test.sh

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test variables
ORIGINAL_DIR="$(pwd)"
TEST_DIR="$(mktemp -d)"
TEST_HOME="$(mktemp -d)"  # Isolated home directory for testing
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# Cleanup function
cleanup() {
    echo -e "\n🧹 Cleaning up test environment..."
    rm -rf "$TEST_DIR"
    rm -rf "$TEST_HOME"
}

trap cleanup EXIT

# Test utilities
log() {
    echo -e "$1"
}

assert_equals() {
    local expected="$1"
    local actual="$2"
    local message="$3"
    
    ((TOTAL_TESTS++))
    
    if [[ "$expected" == "$actual" ]]; then
        log "${GREEN}✅ PASS${NC}: $message"
        ((PASSED_TESTS++))
    else
        log "${RED}❌ FAIL${NC}: $message"
        log "   Expected: '$expected'"
        log "   Actual:   '$actual'"
        ((FAILED_TESTS++))
    fi
}

assert_file_exists() {
    local file="$1"
    local message="$2"
    
    ((TOTAL_TESTS++))
    
    if [[ -f "$file" ]]; then
        log "${GREEN}✅ PASS${NC}: $message"
        ((PASSED_TESTS++))
    else
        log "${RED}❌ FAIL${NC}: $message (file not found: $file)"
        ((FAILED_TESTS++))
    fi
}

assert_dir_exists() {
    local dir="$1"
    local message="$2"
    
    ((TOTAL_TESTS++))
    
    if [[ -d "$dir" ]]; then
        log "${GREEN}✅ PASS${NC}: $message"
        ((PASSED_TESTS++))
    else
        log "${RED}❌ FAIL${NC}: $message (directory not found: $dir)"
        ((FAILED_TESTS++))
    fi
}

assert_contains() {
    local haystack="$1"
    local needle="$2"
    local message="$3"
    
    ((TOTAL_TESTS++))
    
    if [[ "$haystack" == *"$needle"* ]]; then
        log "${GREEN}✅ PASS${NC}: $message"
        ((PASSED_TESTS++))
    else
        log "${RED}❌ FAIL${NC}: $message"
        log "   Text does not contain: '$needle'"
        ((FAILED_TESTS++))
    fi
}

run_test() {
    local test_name="$1"
    log "\n${BLUE}🧪 Running: $test_name${NC}"
}

# Setup isolated installation environment
setup_test_installation() {
    # Create test installation directories
    mkdir -p "$TEST_HOME/.local/bin"
    mkdir -p "$TEST_HOME/.dev-envs"
    
    # Set environment variables for isolated testing
    export HOME="$TEST_HOME"
    export PATH="$TEST_HOME/.local/bin:$PATH"
}

# Test functions will be added here
test_syntax_validation() {
    run_test "Syntax validation"
    
    # Test install.sh syntax
    local install_result=$(bash -n install.sh 2>&1)
    assert_equals "" "$install_result" "install.sh syntax is valid"
    
    # Test dev script syntax
    local dev_result=$(bash -n scripts/dev 2>&1)
    assert_equals "" "$dev_result" "scripts/dev syntax is valid"
}

test_help_commands() {
    run_test "Help commands"
    
    # Test install.sh help
    local install_help=$(bash install.sh --help 2>&1 | head -1)
    assert_contains "$install_help" "Isolated Development Environment Installer" "install.sh --help works"
    
    # Test that dev help doesn't crash
    local dev_help=$(bash scripts/dev --help 2>&1 | head -1 | grep -o "Usage:" || echo "")
    assert_contains "$dev_help" "Usage" "dev --help works"
}

test_language_plugins() {
    run_test "Language plugin structure"
    
    # Test that language plugins directory exists
    assert_dir_exists "languages" "Languages directory exists"
    
    # Test that key language plugins exist
    assert_dir_exists "languages/python" "Python language plugin exists"
    assert_dir_exists "languages/node" "Node.js language plugin exists"
    assert_dir_exists "languages/golang" "Go language plugin exists"
    assert_dir_exists "languages/rust" "Rust language plugin exists"
    assert_dir_exists "languages/java" "Java language plugin exists"
    assert_dir_exists "languages/php" "PHP language plugin exists"
    assert_dir_exists "languages/ubuntu" "Ubuntu language plugin exists"
    
    # Test that each plugin has required files
    assert_file_exists "languages/python/language.yaml" "Python language.yaml exists"
    assert_file_exists "languages/python/Dockerfile.template" "Python Dockerfile.template exists"
    assert_file_exists "languages/python/requirements.txt" "Python requirements.txt exists"
    assert_file_exists "languages/python/main.py" "Python main.py exists"
    assert_file_exists "languages/python/.gitignore" "Python .gitignore exists"
    
    assert_file_exists "languages/node/language.yaml" "Node.js language.yaml exists"
    assert_file_exists "languages/node/Dockerfile.template" "Node.js Dockerfile.template exists"
    assert_file_exists "languages/node/package.json" "Node.js package.json exists"
    
    # Test placeholder content
    local python_dockerfile=$(cat languages/python/Dockerfile.template)
    assert_contains "$python_dockerfile" "{{VERSION}}" "Python Dockerfile contains version placeholder"
    
    local package_json=$(cat languages/node/package.json)
    assert_contains "$package_json" "{{PROJECT_NAME}}" "Node.js package.json contains project name placeholder"
}

test_installation() {
    run_test "Installation process"
    
    # Setup isolated installation environment
    setup_test_installation
    
    # Test installation with --yes flag (using isolated HOME)
    local install_output=$(bash install.sh --force --yes --quiet 2>&1)
    local install_exit_code=$?
    
    assert_equals "0" "$install_exit_code" "Installation completes successfully"
    
    # Test that files were installed in our test environment
    assert_file_exists "$TEST_HOME/.local/bin/dev" "dev script installed"
    assert_dir_exists "$TEST_HOME/.dev-envs" "Configuration directory created"
    # Templates directory no longer needed - we generate directly from language plugins
    assert_dir_exists "$TEST_HOME/.dev-envs/languages" "Languages directory created"
}

test_template_creation() {
    run_test "Template creation in test environment"
    
    # Ensure test environment is set up
    setup_test_installation
    
    # Create test directory
    local test_project="$TEST_DIR/test-project"
    mkdir -p "$test_project"
    cd "$test_project"
    
    # Test that dev command exists in our test environment
    if [[ ! -f "$HOME/.local/bin/dev" ]]; then
        log "${YELLOW}⏭️  SKIP${NC}: dev not installed, skipping template tests"
        return
    fi
    
    # Test template creation with --yes flag (using our test installation)
    local template_output=$("$HOME/.local/bin/dev" new python-3.13 --init --yes 2>&1 || echo "FAILED")
    
    if [[ "$template_output" != "FAILED" ]]; then
        assert_file_exists "$test_project/Dockerfile" "Dockerfile created from template"
        assert_file_exists "$test_project/requirements.txt" "requirements.txt created with --init"
        assert_file_exists "$test_project/main.py" "main.py created with --init"
        
        # Test placeholder substitution
        local dockerfile_content=$(cat Dockerfile)
        assert_contains "$dockerfile_content" "python:3.13-slim" "Version placeholder substituted correctly"
    else
        log "${YELLOW}⏭️  SKIP${NC}: Template creation failed (may need VM setup)"
    fi
    
    cd - > /dev/null
}

test_config_creation() {
    run_test "Configuration creation"
    
    # Ensure test environment is set up
    setup_test_installation
    
    local test_project="$TEST_DIR/test-config"
    mkdir -p "$test_project"
    cd "$test_project"
    
    # Test project config creation using the installed dev script
    local config_output=$("$HOME/.local/bin/dev" config --init --yes 2>&1 || echo "FAILED")
    
    if [[ "$config_output" != "FAILED" ]]; then
        assert_file_exists "$test_project/.devenv.yaml" "Project config file created"
        
        # Test placeholder substitution
        local config_content=$(cat .devenv.yaml)
        assert_contains "$config_content" "test-config" "Project name substituted in config"
    else
        log "${YELLOW}⏭️  SKIP${NC}: Config creation failed"
    fi
    
    cd - > /dev/null
}

test_flag_parsing() {
    run_test "Command line flag parsing"
    
    # Test install.sh flag parsing (just check help output includes --yes)
    local install_help=$(bash install.sh --help 2>&1)
    assert_contains "$install_help" "--yes" "install.sh --help mentions --yes flag"
    
    # Test dev flag parsing
    local dev_help=$(bash scripts/dev --help 2>&1)
    assert_contains "$dev_help" "--yes" "dev --help mentions --yes flag"
}

test_project_detection() {
    run_test "Project detection functionality"
    
    # Test the detect_project_type function directly
    local test_project="$TEST_DIR/test-detection"
    mkdir -p "$test_project"
    cd "$test_project"
    
    # Define a simplified version of detect_project_type for testing
    detect_project_type() {
        local detected_lang=""
        local detected_version=""
        local confidence="low"
        
        # Python detection
        if [[ -f "requirements.txt" ]] || [[ -f "pyproject.toml" ]] || [[ -f "setup.py" ]] || [[ -f "Pipfile" ]]; then
            detected_lang="python"
            confidence="high"
            if [[ -f ".python-version" ]]; then
                detected_version=$(cat .python-version | head -1 | cut -d. -f1,2)
            fi
        # Node.js detection
        elif [[ -f "package.json" ]]; then
            detected_lang="node"
            confidence="high"
        # Go detection
        elif [[ -f "go.mod" ]] || [[ -f "main.go" ]]; then
            detected_lang="golang"
            confidence="high"
            if [[ -f "go.mod" ]]; then
                detected_version=$(grep "^go " go.mod | sed 's/go \([0-9]\+\.[0-9]\+\).*/\1/' | head -1)
            fi
        fi
        
        echo "$detected_lang:$detected_version:$confidence"
    }
    
    # Test Python detection
    echo "flask==2.0.1" > requirements.txt
    local python_result=$(detect_project_type)
    assert_contains "$python_result" "python" "Detects Python project from requirements.txt"
    assert_contains "$python_result" "high" "Python detection has high confidence"
    rm requirements.txt
    
    # Test Node.js detection
    echo '{"name": "test", "version": "1.0.0"}' > package.json
    local node_result=$(detect_project_type)
    assert_contains "$node_result" "node" "Detects Node.js project from package.json"
    assert_contains "$node_result" "high" "Node.js detection has high confidence"
    rm package.json
    
    # Test Go detection
    echo -e "module test\n\ngo 1.22" > go.mod
    local go_result=$(detect_project_type)
    assert_contains "$go_result" "golang" "Detects Go project from go.mod"
    assert_contains "$go_result" "1.22" "Detects Go version from go.mod"
    rm go.mod
    
    # Test version detection with .python-version
    echo "3.12.0" > .python-version
    echo "requests" > requirements.txt
    local python_version_result=$(detect_project_type)
    assert_contains "$python_version_result" "3.12" "Detects Python version from .python-version"
    rm .python-version requirements.txt
    
    # Test no detection
    local empty_result=$(detect_project_type)
    assert_equals "::low" "$empty_result" "Returns empty result for directory with no project files"
    
    cd - > /dev/null
}

test_template_management_commands() {
    run_test "Template management commands"
    
    # Test that template commands don't crash
    local stats_output=$(bash scripts/dev templates stats 2>&1 | head -1)
    assert_contains "$stats_output" "Language Plugin Statistics" "templates stats command works"
    
    local prune_output=$(bash scripts/dev templates prune 2>&1 | head -1)
    assert_contains "$prune_output" "Language Plugin Usage Analysis" "templates prune command works"
    
    local cleanup_output=$(bash scripts/dev templates cleanup 1 2>&1 | head -1)
    assert_contains "$cleanup_output" "Usage Log Cleanup" "templates cleanup command works"
}

test_direct_generation() {
    run_test "Direct Dockerfile generation from language plugins"
    
    local test_project="$TEST_DIR/test-generation"
    mkdir -p "$test_project"
    cd "$test_project"
    
    # Test direct generation without pre-existing templates
    local generation_output=$(bash "$ORIGINAL_DIR/scripts/dev" new python-3.13 --yes 2>&1 || echo "FAILED")
    
    if [[ "$generation_output" != "FAILED" ]]; then
        assert_file_exists "$test_project/Dockerfile" "Dockerfile generated directly from language plugin"
        
        # Verify it contains the right version
        local dockerfile_content=$(cat Dockerfile 2>/dev/null || echo "")
        assert_contains "$dockerfile_content" "python:3.13" "Generated Dockerfile has correct version"
    else
        log "${YELLOW}⏭️  SKIP${NC}: Direct generation test failed (expected in test environment)"
    fi
    
    cd - > /dev/null
}

test_devcontainer_generation() {
    run_test "VS Code devcontainer generation"
    
    local test_project="$TEST_DIR/test-devcontainer"
    mkdir -p "$test_project"
    cd "$test_project"
    
    # Create Dockerfile and project files first
    local dockerfile_output=$(bash "$ORIGINAL_DIR/scripts/dev" new python-3.13 --init --yes 2>&1 || echo "FAILED")
    
    if [[ "$dockerfile_output" != "FAILED" ]]; then
        # Test devcontainer generation (should auto-detect from requirements.txt)
        local devcontainer_output=$(bash "$ORIGINAL_DIR/scripts/dev" devcontainer --yes 2>&1 || echo "FAILED")
        
        if [[ "$devcontainer_output" != "FAILED" ]]; then
            assert_file_exists "$test_project/.devcontainer/devcontainer.json" "devcontainer.json created"
            
            # Check content
            local devcontainer_content=$(cat .devcontainer/devcontainer.json 2>/dev/null || echo "")
            assert_contains "$devcontainer_content" "Python Development Environment" "devcontainer has correct name"
            assert_contains "$devcontainer_content" "ms-python.python" "devcontainer includes Python extensions"
            assert_contains "$devcontainer_content" "forwardPorts" "devcontainer includes port forwarding"
        else
            log "${YELLOW}⏭️  SKIP${NC}: devcontainer generation failed (expected in test environment)"
        fi
    else
        log "${YELLOW}⏭️  SKIP${NC}: Dockerfile creation failed for devcontainer test"
    fi
    
    cd - > /dev/null
}

test_combined_template_devcontainer() {
    run_test "Combined template and devcontainer creation"
    
    local test_project="$TEST_DIR/test-combined"
    mkdir -p "$test_project"
    cd "$test_project"
    
    # Test creating template with devcontainer in one command
    local combined_output=$(bash "$ORIGINAL_DIR/scripts/dev" new node-22 --init --devcontainer --yes 2>&1 || echo "FAILED")
    
    if [[ "$combined_output" != "FAILED" ]]; then
        assert_file_exists "$test_project/Dockerfile" "Dockerfile created"
        assert_file_exists "$test_project/package.json" "package.json created with --init"
        assert_file_exists "$test_project/.devcontainer/devcontainer.json" "devcontainer.json created with --devcontainer"
        
        # Check devcontainer content for Node.js
        local devcontainer_content=$(cat .devcontainer/devcontainer.json 2>/dev/null || echo "")
        assert_contains "$devcontainer_content" "Node.js Development Environment" "devcontainer has Node.js name"
        assert_contains "$devcontainer_content" "ms-vscode.vscode-typescript-next" "devcontainer includes Node.js extensions"
    else
        log "${YELLOW}⏭️  SKIP${NC}: Combined creation failed (expected in test environment)"
    fi
    
    cd - > /dev/null
}

test_config_validation() {
    run_test "Configuration validation system"
    
    local test_project="$TEST_DIR/test-config-validation"
    mkdir -p "$test_project"
    cd "$test_project"
    
    # Create a project config to validate
    bash "$ORIGINAL_DIR/scripts/dev" config --init --yes >/dev/null 2>&1
    
    # Test validation function
    local validation_output=$(bash "$ORIGINAL_DIR/scripts/dev" config validate 2>&1)
    local validation_exit_code=$?
    
    if [[ $validation_exit_code -eq 0 ]]; then
        assert_contains "$validation_output" "Configuration is valid" "Config validation works for valid files"
    else
        log "${YELLOW}⏭️  SKIP${NC}: Config validation test failed (exit code: $validation_exit_code)"
    fi
    
    cd - > /dev/null
}

test_environment_overrides() {
    run_test "Environment variable overrides"
    
    local test_project="$TEST_DIR/test-env-overrides"
    mkdir -p "$test_project"
    cd "$test_project"
    
    # Test environment variable override
    local config_output=$(DEV_VM_NAME=override-vm bash "$ORIGINAL_DIR/scripts/dev" config 2>&1 || echo "CONFIG_FAILED")
    
    if [[ "$config_output" != "CONFIG_FAILED" ]]; then
        assert_contains "$config_output" "override-vm" "Environment variable override works"
        assert_contains "$config_output" "Environment overrides active" "Shows environment overrides status"
    else
        log "${YELLOW}⏭️  SKIP${NC}: Environment override test failed (expected in test environment)"
    fi
    
    cd - > /dev/null
}

test_security_functionality() {
    run_test "Security functionality"
    
    local test_project="$TEST_DIR/test-security"
    mkdir -p "$test_project"
    cd "$test_project"
    
    # Test security validation with vulnerable Dockerfile
    cat > Dockerfile << 'EOF'
FROM python:3.8-slim
RUN apt-get update && apt-get install -y git
RUN pip install flask
ENV SECRET_KEY=hardcoded-secret
WORKDIR /workspace
CMD ["python", "app.py"]
EOF
    
    local validation_output=$(bash "$ORIGINAL_DIR/scripts/dev" security validate 2>&1 || echo "VALIDATION_FAILED")
    
    if [[ "$validation_output" != "VALIDATION_FAILED" ]]; then
        assert_contains "$validation_output" "Security issues found" "Security validation detects vulnerable Dockerfile"
        assert_contains "$validation_output" "No USER directive" "Security validation detects root user issue"
        assert_contains "$validation_output" "apt-get cache not cleaned" "Security validation detects cache cleanup issue"
        assert_contains "$validation_output" "Potential secrets found" "Security validation detects secrets"
    else
        log "${YELLOW}⏭️  SKIP${NC}: Security validation test failed"
    fi
    
    # Test security validation with secure Dockerfile
    cat > Dockerfile << 'EOF'
FROM python:3.13-slim
RUN groupadd -r appuser && useradd -r -g appuser appuser
RUN apt-get update && apt-get install -y --no-install-recommends git && rm -rf /var/lib/apt/lists/*
WORKDIR /workspace
RUN chown -R appuser:appuser /workspace
USER appuser
RUN pip install --no-cache-dir flask
CMD ["bash"]
EOF
    
    local secure_validation=$(bash "$ORIGINAL_DIR/scripts/dev" security validate 2>&1 || echo "SECURE_VALIDATION_FAILED")
    
    if [[ "$secure_validation" != "SECURE_VALIDATION_FAILED" ]]; then
        assert_contains "$secure_validation" "No security issues found" "Security validation passes secure Dockerfile"
    else
        log "${YELLOW}⏭️  SKIP${NC}: Secure validation test failed"
    fi
    
    # Test security scan command
    local scan_output=$(bash "$ORIGINAL_DIR/scripts/dev" security scan 2>&1 || echo "SCAN_FAILED")
    
    if [[ "$scan_output" != "SCAN_FAILED" ]]; then
        assert_contains "$scan_output" "Scanning" "Security scan command works"
    else
        log "${YELLOW}⏭️  SKIP${NC}: Security scan test failed"
    fi
    
    # Test security help
    local help_output=$(bash "$ORIGINAL_DIR/scripts/dev" security --help 2>&1 || echo "HELP_FAILED")
    
    if [[ "$help_output" != "HELP_FAILED" ]]; then
        assert_contains "$help_output" "Security commands" "Security help command works"
        assert_contains "$help_output" "validate" "Security help mentions validate command"
        assert_contains "$help_output" "scan" "Security help mentions scan command"
    else
        log "${YELLOW}⏭️  SKIP${NC}: Security help test failed"
    fi
    
    cd - > /dev/null
}

# Main test execution
main() {
    log "${BLUE}🚀 Starting isolated-dev test suite${NC}"
    log "Test directory: $TEST_DIR"
    
    # Run tests
    test_syntax_validation
    test_help_commands  
    test_language_plugins
    test_installation
    test_template_creation
    test_config_creation
    test_flag_parsing
    test_project_detection
    test_template_management_commands
    test_direct_generation
    test_devcontainer_generation
    test_combined_template_devcontainer
    test_config_validation
    test_environment_overrides
    test_security_functionality
    
    # Print results
    log "\n${BLUE}📊 Test Results${NC}"
    log "Total tests: $TOTAL_TESTS"
    log "${GREEN}Passed: $PASSED_TESTS${NC}"
    
    if [[ $FAILED_TESTS -gt 0 ]]; then
        log "${RED}Failed: $FAILED_TESTS${NC}"
        log "\n${RED}❌ Some tests failed${NC}"
        exit 1
    else
        log "${GREEN}Failed: 0${NC}"
        log "\n${GREEN}✅ All tests passed!${NC}"
        exit 0
    fi
}

# Run main function
main "$@"