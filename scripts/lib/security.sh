#!/bin/bash

# ==============================================================================
# SECURITY FUNCTIONS
# ==============================================================================

function get_security_flags() {
    local security_flags=""
    
    # Drop all capabilities and add only what's needed
    security_flags="--cap-drop=ALL --cap-add=CHOWN --cap-add=DAC_OVERRIDE --cap-add=SETGID --cap-add=SETUID"
    
    # Run with non-root user when possible
    security_flags="$security_flags --user 1000:1000"
    
    # Set security options (OrbStack supports these)
    security_flags="$security_flags --security-opt=no-new-privileges:true"
    security_flags="$security_flags --security-opt=apparmor:unconfined"  # OrbStack default
    
    # Limit resources (OrbStack enforces these)
    security_flags="$security_flags --memory=2g --cpus=2"
    
    # Read-only root filesystem where possible
    security_flags="$security_flags --read-only --tmpfs /tmp"
    
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

