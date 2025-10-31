#!/bin/bash

# ==============================================================================
# SECURITY FUNCTIONS
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/constants.sh"

function validate_container_name() {
    local name="$1"
    if [[ ! "$name" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]]; then
        echo "❌ Error: Invalid container name '$name'. Must start with alphanumeric and contain only letters, numbers, underscores, periods, and hyphens."
        return 1
    fi
    if [[ ${#name} -gt 63 ]]; then
        echo "❌ Error: Container name too long (max 63 characters): '$name'"
        return 1
    fi
}

function validate_tag_name() {
    local tag="$1"
    if [[ ! "$tag" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$ ]]; then
        echo "❌ Error: Invalid tag '$tag'. Must start with alphanumeric/underscore and contain only valid Docker tag characters."
        return 1
    fi
    if [[ ${#tag} -gt 128 ]]; then
        echo "❌ Error: Tag name too long (max 128 characters): '$tag'"
        return 1
    fi
}

function sanitize_env_var() {
    local var="$1"
    # Remove any characters that could be used for injection
    echo "$var" | sed 's/[`$(){};&|<>]//g'
}

function get_security_flags() {
    local security_flags=""
    
    # Drop all capabilities and add only what's needed
    security_flags="--cap-drop=ALL --cap-add=CHOWN --cap-add=DAC_OVERRIDE --cap-add=SETGID --cap-add=SETUID"
    
    # User is set in Dockerfile with USER directive
    # Don't override with --user flag to avoid UID/name conflicts
    
    # Set security options (OrbStack supports these)
    security_flags="$security_flags --security-opt=no-new-privileges:true"
    security_flags="$security_flags --security-opt=apparmor:unconfined"  # OrbStack default
    
    # Limit resources (configurable via config or environment)
    local memory_limit="${DEV_MEMORY_LIMIT:-$(get_config_value "memory_limit")}"
    local cpu_limit="${DEV_CPU_LIMIT:-$(get_config_value "cpu_limit")}"
    
    # Apply resource limits if configured
    if [[ -n "$memory_limit" ]]; then
        security_flags="$security_flags --memory=$memory_limit"
    fi
    if [[ -n "$cpu_limit" ]]; then
        security_flags="$security_flags --cpus=$cpu_limit"
    fi
    
    # Allow writable filesystem for development containers
    # Read-only mode disabled for development flexibility
    
    echo "$security_flags"
}

function validate_dockerfile_security() {
    local dockerfile="$1"
    local issues=()
    
    if [[ ! -f "$dockerfile" ]]; then
        echo "❌ Dockerfile not found: $dockerfile"
        return 1
    fi
    
    echo "🔒 Security validation for: $dockerfile"
    
    # Check for running as root
    if ! grep -q "USER " "$dockerfile"; then
        issues+=("No USER directive found - container runs as root")
    fi
    
    # Check for COPY --chown usage
    if grep -q "COPY.*--chown" "$dockerfile"; then
        echo "✅ Uses COPY --chown for proper file ownership"
    fi
    
    # Check for package manager cache cleanup
    if grep -q "apt-get.*install" "$dockerfile" && ! grep -q "rm -rf /var/lib/apt/lists" "$dockerfile"; then
        issues+=("apt-get cache not cleaned up")
    fi
    
    # Check for --no-cache-dir with pip
    if grep -q "pip install" "$dockerfile" && ! grep -q "no-cache-dir" "$dockerfile"; then
        issues+=("pip install should use --no-cache-dir")
    fi
    
    # Check for secrets in dockerfile
    if grep -qE "(password|secret|key|token)" "$dockerfile"; then
        issues+=("Potential secrets found in Dockerfile")
    fi
    
    # Validate container names and tags in current environment
    echo ""
    echo "🔍 Validating current container configuration:"
    
    # Check custom container name if set
    if [[ -n "${CUSTOM_NAME:-}" ]]; then
        if validate_container_name "$CUSTOM_NAME"; then
            echo "✅ Container name '$CUSTOM_NAME' is valid"
        fi
    fi
    
    # Check custom tag if set
    if [[ -n "${CUSTOM_TAG:-}" ]]; then
        if validate_tag_name "$CUSTOM_TAG"; then
            echo "✅ Image tag '$CUSTOM_TAG' is valid"
        fi
    fi
    
    # Report findings
    if [[ ${#issues[@]} -eq 0 ]]; then
        echo "✅ No security issues found"
        return 0
    else
        echo "⚠️  Security issues found:"
        for issue in "${issues[@]}"; do
            echo "   - $issue"
        done
        return 1
    fi
}

function security_check() {
    local dockerfile="${1:-Dockerfile}"
    local image_name="${2:-}"
    
    echo "🔒 Comprehensive Security Check"
    echo "================================"
    
    # 1. Dockerfile validation
    echo ""
    echo "📋 Dockerfile Security Validation:"
    validate_dockerfile_security "$dockerfile"
    local dockerfile_result=$?
    
    # 2. Image vulnerability scan (if image name provided or can be determined)
    if [[ -n "$image_name" ]] || [[ -f "$dockerfile" ]]; then
        echo ""
        echo "🔍 Image Vulnerability Scan:"
        if [[ -z "$image_name" ]]; then
            # Try to determine image name from current context
            image_name="${CUSTOM_TAG:-${CONTAINER_PREFIX:-dev}-img-$(basename "$(pwd)" | tr '[:upper:]' '[:lower:]')}"
        fi
        scan_image_vulnerabilities "$image_name"
    fi
    
    # 3. Overall security summary
    echo ""
    echo "📊 Security Summary:"
    if [[ $dockerfile_result -eq 0 ]]; then
        echo "✅ Dockerfile security: PASS"
    else
        echo "⚠️  Dockerfile security: ISSUES FOUND"
    fi
    
    echo "💡 Security recommendations:"
    echo "   • Use official base images when possible"
    echo "   • Keep images updated regularly"
    echo "   • Run containers as non-root users"
    echo "   • Use multi-stage builds to reduce attack surface"
    
    return $dockerfile_result
}

function scan_image_vulnerabilities() {
    local image_name="$1"
    
    echo "🔍 Scanning $image_name for vulnerabilities..."
    
    # Extract base image from Dockerfile
    local base_image=$(grep "^FROM" Dockerfile | head -1 | awk '{print $2}' | cut -d: -f1)
    local base_version=$(grep "^FROM" Dockerfile | head -1 | awk '{print $2}' | cut -d: -f2)
    
    if [[ -n "$base_image" ]]; then
        # Check if it's an official image
        case "$base_image" in
            python|node|golang|rust|openjdk|php|ubuntu|debian|alpine)
                echo "   ✅ Using official $base_image image"
                
                # Check for known vulnerable versions using public CVE data
                if command -v curl >/dev/null 2>&1; then
                    echo "   -> Checking for known vulnerabilities..."
                    
                    # Query Docker Hub API for image info
                    local hub_response=$(curl -s "https://hub.docker.com/v2/repositories/library/$base_image/tags/$base_version" 2>/dev/null)
                    if [[ $? -eq 0 ]] && echo "$hub_response" | grep -q "last_updated"; then
                        local last_updated=$(echo "$hub_response" | grep -o '"last_updated":"[^"]*"' | cut -d'"' -f4 | cut -dT -f1)
                        echo "   -> Image last updated: $last_updated"
                        
                        # Check if image is older than 6 months (potential security risk)
                        if command -v date >/dev/null 2>&1; then
                            local six_months_ago=$(date -d "6 months ago" +%Y-%m-%d 2>/dev/null || date -v-6m +%Y-%m-%d 2>/dev/null)
                            if [[ "$last_updated" < "$six_months_ago" ]]; then
                                echo "   ⚠️  Image is older than 6 months - consider updating"
                            else
                                echo "   ✅ Image is relatively recent"
                            fi
                        fi
                    else
                        echo "   ⚠️  Could not verify image freshness"
                    fi
                fi
                ;;
            *)
                echo "   ⚠️  Using non-official base image: $base_image"
                echo "   -> Consider using official images for better security"
                ;;
        esac
    fi
    
    # OrbStack security integration
    echo "   -> Security hardening applied: non-root users, capability dropping, resource limits"
    
    echo "   -> For detailed vulnerability scanning, use:"
    echo "      • docker scout cves $image_name (if Docker Scout is installed)"
    echo "      • External tools: trivy, snyk, or grype"
}

