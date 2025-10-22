#!/bin/bash

# ==============================================================================
# TEMPLATE OPERATIONS AND VERSION DETECTION
# ==============================================================================

function create_dockerfile_from_skeleton() {
    local language="$1"
    local version="$2"
    local output_file="$3"
    local platform="$4"  # Optional platform override
    local skeleton_file="$DOCKERFILE_SKELETONS/$language.dockerfile"
    
    if [[ ! -f "$skeleton_file" ]]; then
        echo "❌ Error: Skeleton file not found: $skeleton_file"
        return 1
    fi
    
    # Determine architecture-specific base image
    local base_image_suffix=""
    if [[ -n "$platform" ]]; then
        case "$platform" in
            linux/arm64|arm64)
                # For ARM64, some images have specific variants
                case "$language" in
                    python|node|golang|rust)
                        base_image_suffix=""  # Official images support multi-arch
                        ;;
                    *)
                        base_image_suffix=""  # Default to official multi-arch
                        ;;
                esac
                ;;
            linux/amd64|amd64)
                base_image_suffix=""  # Default AMD64
                ;;
        esac
    fi
    
    # Replace placeholders in skeleton with actual values
    sed -e "s/{{VERSION}}/$version/g" \
        -e "s/{{BASE_IMAGE_SUFFIX}}/$base_image_suffix/g" \
        "$skeleton_file" > "$output_file"
    return 0
}

function copy_scaffolding_files() {
    local language="$1" 
    local project_name="$2"
    local go_version="$3"  # Optional, for Go language
    local scaffolding_dir="$SCAFFOLDING_SKELETONS/$language"
    
    if [[ ! -d "$scaffolding_dir" ]]; then
        echo "❌ Error: Scaffolding directory not found: $scaffolding_dir"
        return 1
    fi
    
    # Copy all files from scaffolding directory, replacing placeholders
    for file in "$scaffolding_dir"/*; do
        [[ ! -f "$file" ]] && continue
        
        local filename=$(basename "$file")
        
        # Replace placeholders in file content based on file type
        case "$filename" in
            "package.json")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$file" > "$filename"
                ;;
            "go.mod")
                sed -e "s/{{PROJECT_NAME}}/$project_name/g" -e "s/{{GO_VERSION}}/$go_version/g" "$file" > "$filename"
                ;;
            "Cargo.toml")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$file" > "$filename"
                ;;
            "pom.xml")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$file" > "$filename"
                ;;
            "composer.json")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$file" > "$filename"
                ;;
            *)
                cp "$file" "$filename"
                ;;
        esac
    done
    
    return 0
}

function get_latest_python_version() {
    # Fetch latest stable Python version from Docker Hub API
    local latest=$(curl -s --max-time 10 "https://registry.hub.docker.com/v2/repositories/library/python/tags/?page_size=100" 2>/dev/null | \
        jq -r '.results[].name' 2>/dev/null | \
        grep -E '^3\.[0-9]+$' | \
        sort -V | tail -1 2>/dev/null)
    echo "${latest:-3.14}"
}

function get_latest_node_version() {
    # Fetch latest stable Node.js version from Docker Hub API
    local latest=$(curl -s --max-time 10 "https://registry.hub.docker.com/v2/repositories/library/node/tags/?page_size=100" 2>/dev/null | \
        jq -r '.results[].name' 2>/dev/null | \
        grep -E '^[0-9]+$' | \
        sort -n | tail -1 2>/dev/null)
    echo "${latest:-22}"
}

function get_latest_golang_version() {
    # Fetch latest stable Go version from Docker Hub API
    local latest=$(curl -s --max-time 10 "https://registry.hub.docker.com/v2/repositories/library/golang/tags/?page_size=100" 2>/dev/null | \
        jq -r '.results[].name' 2>/dev/null | \
        grep -E '^1\.[0-9]+$' | \
        sort -V | tail -1 2>/dev/null)
    echo "${latest:-1.23}"
}

function get_latest_rust_version() {
    # Fetch latest stable Rust version from Docker Hub API
    local latest=$(curl -s --max-time 10 "https://registry.hub.docker.com/v2/repositories/library/rust/tags/?page_size=100" 2>/dev/null | \
        jq -r '.results[].name' 2>/dev/null | \
        grep -E '^1\.[0-9]+$' | \
        sort -V | tail -1 2>/dev/null)
    echo "${latest:-1.82}"
}

function check_template_updates() {
    echo "🔍 Checking for latest template versions..."
    echo "⏳ Fetching current versions from official sources..."
    
    local templates_dir="$TEMPLATES_DIR"
    local missing_templates=()
    
    # Fetch current latest versions dynamically
    local python_latest="python-$(get_latest_python_version)"
    local node_latest="node-$(get_latest_node_version)"
    local golang_latest="golang-$(get_latest_golang_version)"
    local rust_latest="rust-$(get_latest_rust_version)"
    
    # Only check for languages where we can fetch latest versions
    local recommended=(
        "$python_latest"
        "$node_latest"
        "$golang_latest"
        "$rust_latest"
    )
    
    echo "📋 Latest versions detected:"
    for template in "${recommended[@]}"; do
        echo "  - $template"
    done
    echo ""
    
    # Check which templates are missing
    for template in "${recommended[@]}"; do
        if [[ ! -f "$templates_dir/Dockerfile-$template" ]]; then
            missing_templates+=("$template")
        fi
    done
    
    if [[ ${#missing_templates[@]} -eq 0 ]]; then
        echo "✅ All latest template versions are available!"
    else
        echo "📦 Missing latest templates:"
        for template in "${missing_templates[@]}"; do
            echo "  - $template"
        done
        echo ""
        echo "💡 Run 'dev templates update' to create missing versions."
    fi
}

function update_templates() {
    echo "🔄 Creating missing template versions..."
    echo "⏳ Fetching latest versions..."
    echo ""
    
    local templates_dir="$TEMPLATES_DIR"
    local created_count=0
    
    # Get latest versions dynamically
    local python_version=$(get_latest_python_version)
    local node_version=$(get_latest_node_version)
    local golang_version=$(get_latest_golang_version)
    local rust_version=$(get_latest_rust_version)
    
    # Python latest (if missing)
    if [[ ! -f "$templates_dir/Dockerfile-python-$python_version" ]]; then
        echo "📦 Adding Python $python_version template..."
        if create_dockerfile_from_skeleton "python" "$python_version" "$templates_dir/Dockerfile-python-$python_version"; then
            echo "   ✅ Python $python_version template created"
            ((created_count++))
        else
            echo "   ❌ Failed to create Python $python_version template"
        fi
    fi
    
    # Node.js latest (if missing)  
    if [[ ! -f "$templates_dir/Dockerfile-node-$node_version" ]]; then
        echo "📦 Adding Node.js $node_version template..."
        if create_dockerfile_from_skeleton "node" "$node_version" "$templates_dir/Dockerfile-node-$node_version"; then
            echo "   ✅ Node.js $node_version template created"
            ((created_count++))
        else
            echo "   ❌ Failed to create Node.js $node_version template"
        fi
    fi
    
    # Golang latest (if missing)
    if [[ ! -f "$templates_dir/Dockerfile-golang-$golang_version" ]]; then
        echo "📦 Adding Go $golang_version template..."
        if create_dockerfile_from_skeleton "golang" "$golang_version" "$templates_dir/Dockerfile-golang-$golang_version"; then
            echo "   ✅ Go $golang_version template created"
            ((created_count++))
        else
            echo "   ❌ Failed to create Go $golang_version template"
        fi
    fi
    
    # Rust latest (if missing)
    if [[ ! -f "$templates_dir/Dockerfile-rust-$rust_version" ]]; then
        echo "📦 Adding Rust $rust_version template..."
        if create_dockerfile_from_skeleton "rust" "$rust_version" "$templates_dir/Dockerfile-rust-$rust_version"; then
            echo "   ✅ Rust $rust_version template created"
            ((created_count++))
        else
            echo "   ❌ Failed to create Rust $rust_version template"
        fi
    fi
    
    echo ""
    if [[ $created_count -gt 0 ]]; then
        echo "✅ Template updates completed! Created $created_count new templates."
    else
        echo "✅ All templates were already up to date."
    fi
    echo ""
    echo "💡 Tip: Use 'dev list' to see all available templates"
}

function track_template_usage() {
    local template_name="$1"
    local usage_file="$HOME/.dev-envs/template_usage.log"
    local timestamp=$(date +%s)
    
    # Create usage directory if it doesn't exist
    mkdir -p "$(dirname "$usage_file")"
    
    # Log usage
    echo "$template_name:$timestamp" >> "$usage_file"
    
    # Keep only last 100 entries per template to prevent log bloat
    local temp_file=$(mktemp)
    awk -F: -v template="$template_name" '
        $1 == template { entries[template]++; if(entries[template] <= 100) print }
        $1 != template { print }
    ' "$usage_file" > "$temp_file"
    mv "$temp_file" "$usage_file"
}

function create_from_template() {
    local language="$1"
    local init_project="${2:-false}"
    local platform="${3:-}"
    local template_file=""
    local target_file="./Dockerfile"
    
    if [[ ! -d "$TEMPLATES_DIR" ]]; then
        echo "❌ Error: Templates directory not found at $TEMPLATES_DIR"
        echo "Please run the installer first."
        exit 1
    fi
    
    # Smart template matching logic
    # 1. Try exact match first (e.g., "python-3.13" -> "Dockerfile-python-3.13")
    if [[ -f "$TEMPLATES_DIR/Dockerfile-$language" ]]; then
        template_file="$TEMPLATES_DIR/Dockerfile-$language"
        echo "📋 Using template: $language"
    else
        # 2. Look for versioned templates that start with the language name
        local matching_templates=($(find "$TEMPLATES_DIR" -name "Dockerfile-${language}-*" -type f | grep -v '\\.backup\\.' 2>/dev/null))
        
        if [[ ${#matching_templates[@]} -eq 1 ]]; then
            # Found exactly one versioned template for this language
            template_file="${matching_templates[0]}"
            local template_name=$(basename "$template_file" | sed 's/Dockerfile-//')
            echo "📋 Using template: $template_name"
        elif [[ ${#matching_templates[@]} -gt 1 ]]; then
            # Multiple versions available - check if we have a default configured
            if [[ -n "$DEFAULT_TEMPLATE" ]]; then
                # Check if the default template matches this language
                if [[ "$DEFAULT_TEMPLATE" == "$language"* ]]; then
                    for template in "${matching_templates[@]}"; do
                        local template_name=$(basename "$template" | sed 's/Dockerfile-//')
                        if [[ "$template_name" == "$DEFAULT_TEMPLATE" ]]; then
                            template_file="$template"
                            echo "📋 Using default template: $template_name"
                            break
                        fi
                    done
                fi
            fi
            
            # If no default found, ask user to be specific
            if [[ -z "$template_file" ]]; then
                echo "❌ Error: Multiple versions available for '$language'."
                echo "Please specify the version you want:"
                echo ""
                printf "  %-20s %s\\n" "Template Name" "Usage"
                printf "  %-20s %s\\n" "-------------" "-----"
                for template in "${matching_templates[@]}"; do
                    local template_name=$(basename "$template" | sed 's/Dockerfile-//')
                    printf "  %-20s %s\\n" "$template_name" "dev new $template_name"
                done
                echo ""
                echo "Or run 'dev list' to see all available options."
                echo "Tip: Set a default with 'dev config --edit' to skip this prompt."
                exit 1
            fi
        else
            # No templates found for this language
            echo "❌ Error: No template found for '$language'."
            echo "Available templates:"
            list_templates
            exit 1
        fi
    fi
    
    if [[ -f "$target_file" ]]; then
        echo "⚠️  Warning: Dockerfile already exists in current directory."
        if [[ "$AUTO_YES" == "true" ]]; then
            echo "Auto-confirming overwrite (--yes flag set)"
        else
            echo -n "Do you want to overwrite it? (y/N): "
            read -r response
            if [[ ! "$response" =~ ^[Yy]$ ]]; then
                echo "Operation cancelled."
                exit 0
            fi
        fi
    fi
    
    cp "$template_file" "$target_file" || {
        echo "❌ Error: Failed to copy template"
        exit 1
    }
    
    # Track template usage for cleanup purposes
    local template_name=$(basename "$template_file" | sed 's/Dockerfile-//')
    track_template_usage "$template_name"
    
    echo "✅ Created Dockerfile from $language template"
    
    # Initialize project scaffolding if requested
    if [[ "$init_project" == "true" ]]; then
        echo "🏗️  Initializing project scaffolding..."
        init_project_scaffolding "$language"
    fi
    
    echo "You can now run 'dev' to build and run your container."
    exit 0
}

function init_project_scaffolding() {
    local language="$1"
    local base_lang="${language%%-*}"  # Extract base language (python, node, etc.)
    
    case "$base_lang" in
        python)
            echo "   -> Creating Python project structure..."
            
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files "python" "$project_name"; then
                echo "   -> Created: requirements.txt, main.py, .gitignore"
            else
                echo "   -> ❌ Failed to create Python project structure"
            fi
            ;;
            
        node)
            echo "   -> Creating Node.js project structure..."
            
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files "node" "$project_name"; then
                echo "   -> Created: package.json, index.js, .gitignore"
            else
                echo "   -> ❌ Failed to create Node.js project structure"
            fi
            ;;
            
        golang)
            echo "   -> Creating Go project structure..."
            
            local project_name=$(basename "$(pwd)")
            local go_version=$(get_latest_golang_version)
            
            if copy_scaffolding_files "golang" "$project_name" "$go_version"; then
                echo "   -> Created: go.mod, main.go, .gitignore"
            else
                echo "   -> ❌ Failed to create Go project structure"
            fi
            ;;
            
        rust)
            echo "   -> Creating Rust project structure..."
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files "rust" "$project_name"; then
                mkdir -p src
                cp "$SCAFFOLDING_SKELETONS/rust/main.rs" src/main.rs
                echo "   -> Created: Cargo.toml, src/main.rs, .gitignore"
            else
                echo "   -> ❌ Failed to create Rust project structure"
            fi
            ;;
            
        java)
            echo "   -> Creating Java project structure..."
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files "java" "$project_name"; then
                mkdir -p src/main/java/com/example
                cp "$SCAFFOLDING_SKELETONS/java/Main.java" src/main/java/com/example/Main.java
                echo "   -> Created: pom.xml, src/main/java/com/example/Main.java, .gitignore"
            else
                echo "   -> ❌ Failed to create Java project structure"
            fi
            ;;
            
        php)
            echo "   -> Creating PHP project structure..."
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files "php" "$project_name"; then
                mkdir -p src
                cp "$SCAFFOLDING_SKELETONS/php/index.php" index.php
                echo "   -> Created: composer.json, index.php, src/, .gitignore"
            else
                echo "   -> ❌ Failed to create PHP project structure"
            fi
            ;;
            
        bash)
            echo "   -> Creating Bash project structure..."
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files "bash" "$project_name"; then
                cp "$SCAFFOLDING_SKELETONS/bash/main.sh" "$project_name.sh"
                chmod +x "$project_name.sh"
                echo "   -> Created: $project_name.sh, .gitignore"
            else
                echo "   -> ❌ Failed to create Bash project structure"
            fi
            ;;
            
        *)
            echo "   -> No specific scaffolding available for $base_lang"
            echo "   -> Created basic .gitignore"
            if [[ -f "$SCAFFOLDING_SKELETONS/basic/.gitignore" ]]; then
                cp "$SCAFFOLDING_SKELETONS/basic/.gitignore" .gitignore
            else
                echo "   -> ❌ Basic .gitignore skeleton not found"
            fi
            ;;
    esac
}