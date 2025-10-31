#!/bin/bash

# ==============================================================================
# CONSTANTS AND DEFAULTS
# ==============================================================================

# Guard against multiple sourcing
[[ -n "${_CONSTANTS_LOADED:-}" ]] && return 0
readonly _CONSTANTS_LOADED=1

# --- Path Constants ---
readonly DEV_HOME="${HOME}/.dev-envs"
readonly DEV_CONFIG_DIR="${DEV_HOME}"
readonly DEV_GLOBAL_CONFIG="${DEV_CONFIG_DIR}/config.yaml"
readonly DEV_PROJECT_CONFIG=".devenv.yaml"
readonly DEV_LANGUAGES_DIR="${DEV_HOME}/languages"
readonly DEV_TEMPLATES_DIR="${DEV_HOME}/templates"
readonly DEV_CACHE_DIR="${DEV_HOME}/cache"

# --- Configuration Defaults ---
readonly CFG_DEFAULT_VM_NAME="dev-vm-docker-host"
readonly CFG_DEFAULT_TEMPLATE=""
readonly CFG_DEFAULT_AUTO_START_VM="true"
readonly CFG_DEFAULT_CONTAINER_PREFIX="dev"
readonly CFG_DEFAULT_NETWORK_MODE="bridge"
readonly CFG_DEFAULT_AUTO_HOST_NETWORKING="false"
readonly CFG_DEFAULT_PORT_RANGE="3000-9000"
readonly CFG_DEFAULT_ENABLE_PORT_HEALTH_CHECK="true"
readonly CFG_DEFAULT_PORT_HEALTH_TIMEOUT="5"
readonly CFG_DEFAULT_MEMORY_LIMIT=""
readonly CFG_DEFAULT_CPU_LIMIT=""
readonly CFG_DEFAULT_CACHE_TTL="86400"
readonly CFG_DEFAULT_CACHE_MAX_SIZE="100"
readonly CFG_DEFAULT_MIN_DISK_SPACE="5"
readonly CFG_DEFAULT_MOUNT_SSH_KEYS="false"
readonly CFG_DEFAULT_MOUNT_GIT_CONFIG="false"

# --- Configuration Schema ---
# Format: key|type|pattern|max_length_or_range
# Bash 3 compatible - no associative arrays
CONFIG_SCHEMA_KEYS="vm_name default_template container_prefix network_mode port_range memory_limit cpu_limit auto_start_vm auto_host_networking enable_port_health_check port_health_timeout cache_ttl cache_max_size min_disk_space pass_env_vars mount_ssh_keys mount_git_config"

# --- Helper Functions ---

# Get default value for a configuration key
function get_default_value() {
    local key="$1"
    case "$key" in
        vm_name) echo "$CFG_DEFAULT_VM_NAME" ;;
        default_template) echo "$CFG_DEFAULT_TEMPLATE" ;;
        auto_start_vm) echo "$CFG_DEFAULT_AUTO_START_VM" ;;
        container_prefix) echo "$CFG_DEFAULT_CONTAINER_PREFIX" ;;
        network_mode) echo "$CFG_DEFAULT_NETWORK_MODE" ;;
        auto_host_networking) echo "$CFG_DEFAULT_AUTO_HOST_NETWORKING" ;;
        port_range) echo "$CFG_DEFAULT_PORT_RANGE" ;;
        enable_port_health_check) echo "$CFG_DEFAULT_ENABLE_PORT_HEALTH_CHECK" ;;
        port_health_timeout) echo "$CFG_DEFAULT_PORT_HEALTH_TIMEOUT" ;;
        memory_limit) echo "$CFG_DEFAULT_MEMORY_LIMIT" ;;
        cpu_limit) echo "$CFG_DEFAULT_CPU_LIMIT" ;;
        cache_ttl) echo "$CFG_DEFAULT_CACHE_TTL" ;;
        cache_max_size) echo "$CFG_DEFAULT_CACHE_MAX_SIZE" ;;
        min_disk_space) echo "$CFG_DEFAULT_MIN_DISK_SPACE" ;;
        mount_ssh_keys) echo "$CFG_DEFAULT_MOUNT_SSH_KEYS" ;;
        mount_git_config) echo "$CFG_DEFAULT_MOUNT_GIT_CONFIG" ;;
        *) echo "" ;;
    esac
}

# Get schema type for a configuration key (Bash 3 compatible)
function get_schema_type() {
    local key="$1"
    case "$key" in
        vm_name|default_template|container_prefix|network_mode|port_range|memory_limit|cpu_limit) echo "string" ;;
        auto_start_vm|auto_host_networking|enable_port_health_check|mount_ssh_keys|mount_git_config) echo "boolean" ;;
        port_health_timeout|cache_ttl|cache_max_size|min_disk_space) echo "number" ;;
        pass_env_vars) echo "nested" ;;
        *) echo "unknown" ;;
    esac
}

# Get validation pattern for a configuration key
function get_validation_pattern() {
    local key="$1"
    case "$key" in
        vm_name|container_prefix) echo "^[a-zA-Z0-9_-]+$" ;;
        network_mode) echo "^(bridge|host|none|[a-zA-Z0-9_-]+)$" ;;
        port_range) echo "^[0-9]+-[0-9]+$" ;;
        *) echo "" ;;
    esac
}

# Get max length for a configuration key
function get_max_length() {
    local key="$1"
    case "$key" in
        vm_name|default_template|container_prefix) echo "100" ;;
        network_mode) echo "50" ;;
        port_range|memory_limit|cpu_limit) echo "20" ;;
        *) echo "" ;;
    esac
}

# Get min/max values for number types
function get_number_range() {
    local key="$1"
    case "$key" in
        port_health_timeout) echo "1:300" ;;
        cache_ttl) echo "0:31536000" ;;
        cache_max_size) echo "1:10000" ;;
        min_disk_space) echo "1:1000" ;;
        *) echo "" ;;
    esac
}

# Get environment variable name for a config key
function get_env_var_name() {
    local key="$1"
    case "$key" in
        vm_name) echo "DEV_VM_NAME" ;;
        default_template) echo "DEV_DEFAULT_TEMPLATE" ;;
        auto_start_vm) echo "DEV_AUTO_START_VM" ;;
        container_prefix) echo "DEV_CONTAINER_PREFIX" ;;
        network_mode) echo "DEV_NETWORK_MODE" ;;
        auto_host_networking) echo "DEV_AUTO_HOST_NETWORKING" ;;
        port_range) echo "DEV_PORT_RANGE" ;;
        enable_port_health_check) echo "DEV_ENABLE_PORT_HEALTH_CHECK" ;;
        port_health_timeout) echo "DEV_PORT_HEALTH_TIMEOUT" ;;
        memory_limit) echo "DEV_MEMORY_LIMIT" ;;
        cpu_limit) echo "DEV_CPU_LIMIT" ;;
        cache_ttl) echo "DEV_CACHE_TTL" ;;
        cache_max_size) echo "DEV_CACHE_MAX_SIZE" ;;
        min_disk_space) echo "DEV_MIN_DISK_SPACE" ;;
        mount_ssh_keys) echo "DEV_MOUNT_SSH_KEYS" ;;
        mount_git_config) echo "DEV_MOUNT_GIT_CONFIG" ;;
        *) echo "" ;;
    esac
}

# Check if a configuration key is valid
function is_valid_config_key() {
    local key="$1"
    echo "$CONFIG_SCHEMA_KEYS" | grep -qw "$key"
}

# Get all valid configuration keys
function get_all_config_keys() {
    echo "$CONFIG_SCHEMA_KEYS"
}
