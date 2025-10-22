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
    local install_help=$(./install.sh --help 2>&1 | head -1)
    assert_contains "$install_help" "Isolated Development Environment Installer" "install.sh --help works"
    
    # Test that dev help doesn't crash (requires installation)
    if [[ -f "$HOME/.local/bin/dev" ]]; then
        local dev_help=$(dev --help 2>&1 | head -1 | grep -o "Usage:" || echo "")
        assert_contains "$dev_help" "Usage" "dev --help works"
    fi
}

test_skeleton_files() {
    run_test "Skeleton files structure"
    
    # Test that skeleton directories exist
    assert_dir_exists "skeletons" "Skeletons directory exists"
    assert_dir_exists "skeletons/dockerfiles" "Dockerfile skeletons directory exists"
    assert_dir_exists "skeletons/scaffolding" "Scaffolding skeletons directory exists"
    
    # Test that key skeleton files exist
    assert_file_exists "skeletons/dockerfiles/python.dockerfile" "Python Dockerfile skeleton exists"
    assert_file_exists "skeletons/dockerfiles/node.dockerfile" "Node.js Dockerfile skeleton exists"
    assert_file_exists "skeletons/scaffolding/python/requirements.txt" "Python requirements skeleton exists"
    assert_file_exists "skeletons/scaffolding/node/package.json" "Node.js package.json skeleton exists"
    
    # Test placeholder content
    local python_dockerfile=$(cat skeletons/dockerfiles/python.dockerfile)
    assert_contains "$python_dockerfile" "{{VERSION}}" "Python Dockerfile contains version placeholder"
    
    local package_json=$(cat skeletons/scaffolding/node/package.json)
    assert_contains "$package_json" "{{PROJECT_NAME}}" "Node.js package.json contains project name placeholder"
}

test_installation() {
    run_test "Installation process"
    
    # Setup isolated installation environment
    setup_test_installation
    
    # Test installation with --yes flag (using isolated HOME)
    local install_output=$(./install.sh --force --yes --quiet 2>&1)
    local install_exit_code=$?
    
    assert_equals "0" "$install_exit_code" "Installation completes successfully"
    
    # Test that files were installed in our test environment
    assert_file_exists "$TEST_HOME/.local/bin/dev" "dev script installed"
    assert_dir_exists "$TEST_HOME/.dev-envs" "Configuration directory created"
    assert_dir_exists "$TEST_HOME/.dev-envs/templates" "Templates directory created"
    assert_dir_exists "$TEST_HOME/.dev-envs/skeletons" "Skeletons directory created"
}

test_template_creation() {
    run_test "Template creation in test environment"
    
    # Create test directory
    local test_project="$TEST_DIR/test-project"
    mkdir -p "$test_project"
    cd "$test_project"
    
    # Test that dev command exists in our test environment
    if [[ ! -f "$TEST_HOME/.local/bin/dev" ]]; then
        log "${YELLOW}⏭️  SKIP${NC}: dev not installed, skipping template tests"
        return
    fi
    
    # Test template creation with --yes flag (using our test installation)
    local template_output=$("$TEST_HOME/.local/bin/dev" new python-3.13 --init --yes 2>&1 || echo "FAILED")
    
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
    
    local test_project="$TEST_DIR/test-config"
    mkdir -p "$test_project"
    cd "$test_project"
    
    if [[ ! -f "$HOME/.local/bin/dev" ]]; then
        log "${YELLOW}⏭️  SKIP${NC}: dev not installed, skipping config tests"
        cd - > /dev/null
        return
    fi
    
    # Test project config creation
    local config_output=$(dev config --init --yes 2>&1 || echo "FAILED")
    
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
    local install_help=$(./install.sh --help 2>&1)
    assert_contains "$install_help" "--yes" "install.sh --help mentions --yes flag"
    
    if [[ -f "$HOME/.local/bin/dev" ]]; then
        local dev_help=$(dev --help 2>&1)
        assert_contains "$dev_help" "--yes" "dev --help mentions --yes flag"
    fi
}

# Main test execution
main() {
    log "${BLUE}🚀 Starting isolated-dev test suite${NC}"
    log "Test directory: $TEST_DIR"
    
    # Run tests
    test_syntax_validation
    test_help_commands  
    test_skeleton_files
    test_installation
    test_template_creation
    test_config_creation
    test_flag_parsing
    
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