#!/bin/bash

# ==============================================================================
# TEMPLATE OPERATIONS AND VERSION DETECTION
# ==============================================================================

function create_dockerfile_from_language_plugin() {
    local language="$1"
    local version="$2"
    local output_file="$3"
    local platform="$4"  # Optional platform override
    local template_file="$LANGUAGES_DIR/$language/Dockerfile.template"
    
    if [[ ! -f "$template_file" ]]; then
        echo "❌ Error: Language template not found: $template_file"
        return 1
    fi
    
    # Replace placeholders in template with actual values
    sed -e "s/{{VERSION}}/$version/g" \
        "$template_file" > "$output_file"
    return 0
}

function copy_scaffolding_files_from_plugin() {
    local language="$1" 
    local project_name="$2"
    local go_version="$3"  # Optional, for Go language
    local language_dir="$LANGUAGES_DIR/$language"
    
    if [[ ! -d "$language_dir" ]]; then
        echo "❌ Error: Language plugin directory not found: $language_dir"
        return 1
    fi
    
    # Read language.yaml to get scaffolding files list
    local scaffolding_files=()
    if [[ -f "$language_dir/language.yaml" ]]; then
        # Extract scaffolding files from YAML array format: scaffolding: [file1, file2, file3]
        local scaffolding_line=$(grep "scaffolding:" "$language_dir/language.yaml" | head -1)
        if [[ "$scaffolding_line" =~ scaffolding:[[:space:]]*\[(.*)\] ]]; then
            local files_str="${BASH_REMATCH[1]}"
            # Split by comma and clean up
            IFS=',' read -ra files_array <<< "$files_str"
            for file in "${files_array[@]}"; do
                # Remove quotes and whitespace
                file=$(echo "$file" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^"//;s/"$//')
                [[ -n "$file" ]] && scaffolding_files+=("$file")
            done
        fi
    fi
    
    # Copy scaffolding files, replacing placeholders
    for file in "${scaffolding_files[@]}"; do
        local source_file="$language_dir/$file"
        [[ ! -f "$source_file" ]] && continue
        
        # Create directory structure if needed
        local target_dir=$(dirname "$file")
        [[ "$target_dir" != "." ]] && mkdir -p "$target_dir"
        
        # Replace placeholders in file content based on file type
        case "$file" in
            "package.json")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$source_file" > "$file"
                ;;
            "go.mod")
                sed -e "s/{{PROJECT_NAME}}/$project_name/g" -e "s/{{GO_VERSION}}/$go_version/g" "$source_file" > "$file"
                ;;
            "Cargo.toml")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$source_file" > "$file"
                ;;
            "pom.xml")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$source_file" > "$file"
                ;;
            "composer.json")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$source_file" > "$file"
                ;;
            "Main.java")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$source_file" > "$file"
                ;;
            "src/main.rs")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$source_file" > "$file"
                ;;
            "main.sh")
                sed "s/{{PROJECT_NAME}}/$project_name/g" "$source_file" > "$file"
                chmod +x "$file"
                ;;
            *)
                cp "$source_file" "$file"
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
        if create_dockerfile_from_language_plugin "python" "$python_version" "$templates_dir/Dockerfile-python-$python_version"; then
            echo "   ✅ Python $python_version template created"
            ((created_count++))
        else
            echo "   ❌ Failed to create Python $python_version template"
        fi
    fi
    
    # Node.js latest (if missing)  
    if [[ ! -f "$templates_dir/Dockerfile-node-$node_version" ]]; then
        echo "📦 Adding Node.js $node_version template..."
        if create_dockerfile_from_language_plugin "node" "$node_version" "$templates_dir/Dockerfile-node-$node_version"; then
            echo "   ✅ Node.js $node_version template created"
            ((created_count++))
        else
            echo "   ❌ Failed to create Node.js $node_version template"
        fi
    fi
    
    # Golang latest (if missing)
    if [[ ! -f "$templates_dir/Dockerfile-golang-$golang_version" ]]; then
        echo "📦 Adding Go $golang_version template..."
        if create_dockerfile_from_language_plugin "golang" "$golang_version" "$templates_dir/Dockerfile-golang-$golang_version"; then
            echo "   ✅ Go $golang_version template created"
            ((created_count++))
        else
            echo "   ❌ Failed to create Go $golang_version template"
        fi
    fi
    
    # Rust latest (if missing)
    if [[ ! -f "$templates_dir/Dockerfile-rust-$rust_version" ]]; then
        echo "📦 Adding Rust $rust_version template..."
        if create_dockerfile_from_language_plugin "rust" "$rust_version" "$templates_dir/Dockerfile-rust-$rust_version"; then
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
    local generate_devcontainer="${4:-false}"
    local target_file="./Dockerfile"
    
    if [[ ! -d "$LANGUAGES_DIR" ]]; then
        echo "❌ Error: Languages directory not found at $LANGUAGES_DIR"
        echo "Please run the installer first."
        exit 1
    fi
    
    # Parse language and version from input (e.g., "python-3.13" -> "python" + "3.13")
    local base_lang="${language%%-*}"
    local version="${language#*-}"
    
    # If no version specified, use the language name as version
    if [[ "$base_lang" == "$version" ]]; then
        version=""
    fi
    
    # Check if language plugin exists
    local language_dir="$LANGUAGES_DIR/$base_lang"
    if [[ ! -d "$language_dir" ]]; then
        echo "❌ Error: Language plugin not found: $base_lang"
        echo "Available languages:"
        ls "$LANGUAGES_DIR" 2>/dev/null | grep -v README.md || echo "  (none found)"
        exit 1
    fi
    
    # Get available versions from language.yaml
    local available_versions=()
    if [[ -f "$language_dir/language.yaml" ]]; then
        local versions_line=$(grep "versions:" "$language_dir/language.yaml" | head -1)
        if [[ "$versions_line" =~ versions:[[:space:]]*\[(.*)\] ]]; then
            local versions_str="${BASH_REMATCH[1]}"
            IFS=',' read -ra versions_array <<< "$versions_str"
            for v in "${versions_array[@]}"; do
                v=$(echo "$v" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^"//;s/"$//')
                [[ -n "$v" ]] && available_versions+=("$v")
            done
        fi
    fi
    
    # If no version specified and multiple available, prompt user
    if [[ -z "$version" && ${#available_versions[@]} -gt 1 ]]; then
        echo "❌ Error: Multiple versions available for '$base_lang'."
        echo "Please specify the version you want:"
        echo ""
        printf "  %-20s %s\\n" "Template Name" "Usage"
        printf "  %-20s %s\\n" "-------------" "-----"
        for v in "${available_versions[@]}"; do
            printf "  %-20s %s\\n" "$base_lang-$v" "dev new $base_lang-$v"
        done
        echo ""
        echo "Tip: Set a default with 'dev config --edit' to skip this prompt."
        exit 1
    elif [[ -z "$version" && ${#available_versions[@]} -eq 1 ]]; then
        version="${available_versions[0]}"
        echo "📋 Using version: $base_lang-$version"
    elif [[ -z "$version" ]]; then
        echo "❌ Error: No versions defined for language: $base_lang"
        exit 1
    else
        echo "📋 Using template: $base_lang-$version"
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
    
    # Generate Dockerfile directly from language plugin
    if ! create_dockerfile_from_language_plugin "$base_lang" "$version" "$target_file" "$platform"; then
        echo "❌ Error: Failed to create Dockerfile from language plugin"
        exit 1
    fi
    
    echo "✅ Created Dockerfile from $base_lang-$version template"
    
    # Initialize project scaffolding if requested
    if [[ "$init_project" == "true" ]]; then
        echo "🏗️  Initializing project scaffolding..."
        init_project_scaffolding "$base_lang"
    fi
    
    # Generate devcontainer configuration if requested
    if [[ "$generate_devcontainer" == "true" ]]; then
        echo "🔧 Generating VS Code devcontainer configuration..."
        if generate_devcontainer_config "$language" "$version" "$target_file"; then
            echo "   ✅ VS Code devcontainer.json created"
        else
            echo "   ⚠️  Failed to generate devcontainer configuration"
        fi
    fi
    
    echo "You can now run 'dev' to build and run your container."
    if [[ "$generate_devcontainer" == "true" ]]; then
        echo "Or open in VS Code and use 'Dev Containers: Reopen in Container'."
    else
        echo ""
        echo "💡 Tip: Add VS Code devcontainer support with 'dev devcontainer' or use --devcontainer flag next time."
    fi
    exit 0
}

function init_project_scaffolding() {
    local language="$1"
    local base_lang="${language%%-*}"  # Extract base language (python, node, etc.)
    
    case "$base_lang" in
        python)
            echo "   -> Creating Python project structure..."
            
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files_from_plugin "python" "$project_name"; then
                echo "   -> Created: requirements.txt, main.py, .gitignore"
            else
                echo "   -> ❌ Failed to create Python project structure"
            fi
            ;;
            
        node)
            echo "   -> Creating Node.js project structure..."
            
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files_from_plugin "node" "$project_name"; then
                echo "   -> Created: package.json, index.js, .gitignore"
            else
                echo "   -> ❌ Failed to create Node.js project structure"
            fi
            ;;
            
        golang)
            echo "   -> Creating Go project structure..."
            
            local project_name=$(basename "$(pwd)")
            local go_version=$(get_latest_golang_version)
            
            if copy_scaffolding_files_from_plugin "golang" "$project_name" "$go_version"; then
                echo "   -> Created: go.mod, main.go, .gitignore"
            else
                echo "   -> ❌ Failed to create Go project structure"
            fi
            ;;
            
        rust)
            echo "   -> Creating Rust project structure..."
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files_from_plugin "rust" "$project_name"; then
                echo "   -> Created: Cargo.toml, src/main.rs, .gitignore"
            else
                echo "   -> ❌ Failed to create Rust project structure"
            fi
            ;;
            
        java)
            echo "   -> Creating Java project structure..."
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files_from_plugin "java" "$project_name"; then
                echo "   -> Created: pom.xml, src/main/java/com/example/Main.java, .gitignore"
            else
                echo "   -> ❌ Failed to create Java project structure"
            fi
            ;;
            
        php)
            echo "   -> Creating PHP project structure..."
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files_from_plugin "php" "$project_name"; then
                echo "   -> Created: composer.json, index.php, src/, .gitignore"
            else
                echo "   -> ❌ Failed to create PHP project structure"
            fi
            ;;
            
        bash)
            echo "   -> Creating Bash project structure..."
            local project_name=$(basename "$(pwd)")
            if copy_scaffolding_files_from_plugin "bash" "$project_name"; then
                echo "   -> Created: $project_name.sh, .gitignore"
            else
                echo "   -> ❌ Failed to create Bash project structure"
            fi
            ;;
            
        *)
            echo "   -> No specific scaffolding available for $base_lang"
            echo "   -> Created basic .gitignore"
            # Create basic .gitignore
            cat > .gitignore << 'EOF'
# IDE
.vscode/
.idea/
*.swp
*.swo
*~

# OS
.DS_Store
Thumbs.db
EOF
            ;;
    esac
}
function show_template_stats() {
    echo "📊 Language Plugin Statistics"
    echo ""
    
    local usage_file="$HOME/.dev-envs/template_usage.log"
    
    if [[ ! -d "$LANGUAGES_DIR" ]]; then
        echo "❌ Languages directory not found: $LANGUAGES_DIR"
        return 1
    fi
    
    echo "📦 Available Language Plugins:"
    printf "   %-12s %-8s %s\\n" "Language" "Versions" "Status"
    printf "   %-12s %-8s %s\\n" "--------" "--------" "------"
    
    local total_plugins=0
    
    # Process language plugins
    for lang_dir in "$LANGUAGES_DIR"/*; do
        [[ ! -d "$lang_dir" ]] && continue
        [[ "$(basename "$lang_dir")" == "README.md" ]] && continue
        
        local lang_name=$(basename "$lang_dir")
        ((total_plugins++))
        
        # Get versions from language.yaml
        local versions=""
        local version_count=0
        if [[ -f "$lang_dir/language.yaml" ]]; then
            local versions_line=$(grep "versions:" "$lang_dir/language.yaml" | head -1)
            if [[ "$versions_line" =~ versions:[[:space:]]*\[(.*)\] ]]; then
                local versions_str="${BASH_REMATCH[1]}"
                # Count versions
                version_count=$(echo "$versions_str" | tr ',' '\n' | wc -l)
                versions="$version_count available"
            fi
        fi
        
        [[ -z "$versions" ]] && versions="none defined"
        
        # Check if plugin has been used
        local status="✨ Ready"
        if [[ -f "$usage_file" ]] && grep -q "^$lang_name-" "$usage_file"; then
            local usage_count=$(grep "^$lang_name-" "$usage_file" | wc -l)
            status="🔥 Used ($usage_count times)"
        fi
        
        printf "   %-12s %-8s %s\\n" "$lang_name" "$versions" "$status"
    done
    
    echo ""
    echo "📈 Usage Statistics:"
    if [[ -f "$usage_file" ]]; then
        local total_usage=$(wc -l < "$usage_file" 2>/dev/null || echo 0)
        echo "   Total usage records: $total_usage"
        
        # Show most used language plugins
        if [[ $total_usage -gt 0 ]]; then
            echo ""
            echo "   🔥 Most Used Language Plugins (last 30 days):"
            local thirty_days_ago=$(($(date +%s) - 30 * 86400))
            
            awk -F: -v threshold="$thirty_days_ago" '
                $2 >= threshold { 
                    # Extract language from template name (e.g., python-3.13 -> python)
                    split($1, parts, "-")
                    lang = parts[1]
                    count[lang]++ 
                }
                END {
                    for (language in count) {
                        print count[language], language
                    }
                }
            ' "$usage_file" | sort -nr | head -5 | while read count language; do
                printf "      %-3s uses: %s\\n" "$count" "$language"
            done
        fi
    else
        echo "   No usage tracking data available"
        echo "   (Usage tracking starts when language plugins are first used)"
    fi
    
    echo ""
    echo "💾 Storage Information:"
    local total_size=""
    if command -v du >/dev/null 2>&1; then
        total_size=$(du -sh "$LANGUAGES_DIR" 2>/dev/null | cut -f1 || echo "Unknown")
        echo "   Language plugins storage: $total_size"
    fi
    echo "   Total language plugins: $total_plugins"
    
    echo ""
    echo "✨ System Benefits:"
    echo "   ✅ Always up-to-date (generated on-demand)"
    echo "   ✅ No template file cleanup needed"
    echo "   ✅ Single source of truth per language"
    echo "   ✅ Easy to add new languages"
    
    if [[ -f "$usage_file" ]]; then
        local old_entries=$(awk -F: -v threshold="$(($(date +%s) - 60 * 86400))" '$2 < threshold' "$usage_file" | wc -l)
        if [[ $old_entries -gt 0 ]]; then
            echo ""
            echo "🧹 Cleanup Opportunity:"
            echo "   📅 $old_entries usage log entries older than 60 days"
            echo "   💡 Run 'dev templates cleanup' to clean up old usage logs"
        fi
    fi
}

function prune_old_templates() {
    echo "📊 Language Plugin Usage Analysis"
    echo ""
    echo "ℹ️  Since we generate Dockerfiles directly from language plugins,"
    echo "   there are no old template files to clean up."
    echo ""
    echo "📋 Available language plugins:"
    
    local usage_file="$HOME/.dev-envs/template_usage.log"
    local plugin_count=0
    
    for lang_dir in "$LANGUAGES_DIR"/*; do
        [[ ! -d "$lang_dir" ]] && continue
        [[ "$(basename "$lang_dir")" == "README.md" ]] && continue
        
        local lang_name=$(basename "$lang_dir")
        ((plugin_count++))
        
        # Show usage stats if available
        if [[ -f "$usage_file" ]]; then
            local usage_count=$(grep "^$lang_name-" "$usage_file" 2>/dev/null | wc -l)
            local last_used=$(grep "^$lang_name-" "$usage_file" 2>/dev/null | tail -1 | cut -d: -f2)
            if [[ $usage_count -gt 0 && -n "$last_used" ]]; then
                local days_ago=$(( ($(date +%s) - last_used) / 86400 ))
                echo "   ✅ $lang_name (used $usage_count times, last used $days_ago days ago)"
            else
                echo "   📦 $lang_name (available, not yet used)"
            fi
        else
            echo "   📦 $lang_name (available)"
        fi
    done
    
    echo ""
    echo "📊 Summary:"
    echo "   Available language plugins: $plugin_count"
    echo "   All plugins are always current (generated on-demand)"
    echo ""
    echo "💡 Use 'dev templates stats' for detailed usage information."
}

function cleanup_unused_templates() {
    local days="${1:-60}"
    echo "🧹 Usage Log Cleanup (removing entries older than $days days)"
    echo ""
    
    local usage_file="$HOME/.dev-envs/template_usage.log"
    local current_date=$(date +%s)
    local threshold_date=$((current_date - days * 86400))
    
    if [[ ! -f "$usage_file" ]]; then
        echo "📝 No usage tracking file found - nothing to clean up."
        return
    fi
    
    local total_entries=$(wc -l < "$usage_file")
    local temp_file=$(mktemp)
    
    # Keep only entries newer than threshold
    awk -F: -v threshold="$threshold_date" '$2 >= threshold' "$usage_file" > "$temp_file"
    local kept_entries=$(wc -l < "$temp_file")
    local removed_entries=$((total_entries - kept_entries))
    
    if [[ $removed_entries -eq 0 ]]; then
        echo "✨ No old usage entries need cleanup!"
        echo "💡 All usage entries are from the last $days days."
        rm "$temp_file"
        return
    fi
    
    echo "📊 Usage log cleanup summary:"
    echo "   Total entries: $total_entries"
    echo "   Entries to remove: $removed_entries (older than $days days)"
    echo "   Entries to keep: $kept_entries"
    
    if [[ "$AUTO_YES" == "true" ]]; then
        echo "Auto-confirming cleanup (--yes flag set)"
    else
        echo ""
        read -p "Continue with cleanup? (y/N) " -n 1 -r
        echo
        
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            echo "Cleanup cancelled."
            rm "$temp_file"
            return
        fi
    fi
    
    # Apply the cleanup
    mv "$temp_file" "$usage_file"
    
    echo ""
    echo "✅ Usage log cleanup complete! Removed $removed_entries old entries."
    echo "💡 Language plugins remain available (they're never removed)."
}

function generate_devcontainer_config() {
    local language="$1"
    local version="$2"
    local dockerfile_path="${3:-./Dockerfile}"
    
    # Parse language and version
    local base_lang="${language%%-*}"
    local lang_version="${language#*-}"
    if [[ "$base_lang" == "$lang_version" ]]; then
        lang_version="$version"
    fi
    
    # Get language configuration
    local language_dir="$LANGUAGES_DIR/$base_lang"
    if [[ ! -f "$language_dir/language.yaml" ]]; then
        echo "❌ Error: Language configuration not found: $language_dir/language.yaml"
        return 1
    fi
    
    # Extract ports from language.yaml
    local ports=()
    local ports_line=$(grep "ports:" "$language_dir/language.yaml" | head -1)
    if [[ "$ports_line" =~ ports:[[:space:]]*\[(.*)\] ]]; then
        local ports_str="${BASH_REMATCH[1]}"
        IFS=',' read -ra ports_array <<< "$ports_str"
        for port in "${ports_array[@]}"; do
            port=$(echo "$port" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//;s/^"//;s/"$//')
            [[ -n "$port" ]] && ports+=("$port")
        done
    fi
    
    # Get display name
    local display_name=$(grep "display_name:" "$language_dir/language.yaml" | head -1 | sed 's/display_name:[[:space:]]*//' | sed 's/^"//;s/"$//')
    [[ -z "$display_name" ]] && display_name="$base_lang"
    
    # Create .devcontainer directory
    mkdir -p .devcontainer
    
    # Generate devcontainer.json
    local config_file=".devcontainer/devcontainer.json"
    
    cat > "$config_file" << EOF
{
  "name": "$display_name Development Environment",
  "dockerFile": "../$(basename "$dockerfile_path")",
  "workspaceFolder": "/workspace",
  "mounts": [
    "source=\${localWorkspaceFolder},target=/workspace,type=bind,consistency=cached",
    "source=\${localEnv:HOME}/.ssh,target=/home/dev/.ssh,type=bind,readonly",
    "source=\${localEnv:HOME}/.gitconfig,target=/home/dev/.gitconfig,type=bind,readonly"
  ],
EOF
    
    # Add port forwarding if ports are defined
    if [[ ${#ports[@]} -gt 0 ]]; then
        echo '  "forwardPorts": [' >> "$config_file"
        for i in "${!ports[@]}"; do
            if [[ $i -eq $((${#ports[@]} - 1)) ]]; then
                echo "    ${ports[i]}" >> "$config_file"
            else
                echo "    ${ports[i]}," >> "$config_file"
            fi
        done
        echo '  ],' >> "$config_file"
    fi
    
    # Add language-specific settings
    case "$base_lang" in
        python)
            cat >> "$config_file" << 'EOF'
  "customizations": {
    "vscode": {
      "extensions": [
        "ms-python.python",
        "ms-python.pylint",
        "ms-python.black-formatter"
      ],
      "settings": {
        "python.defaultInterpreterPath": "/usr/local/bin/python",
        "python.linting.enabled": true,
        "python.linting.pylintEnabled": true,
        "python.formatting.provider": "black"
      }
    }
  },
EOF
            ;;
        node)
            cat >> "$config_file" << 'EOF'
  "customizations": {
    "vscode": {
      "extensions": [
        "ms-vscode.vscode-typescript-next",
        "esbenp.prettier-vscode",
        "ms-vscode.vscode-eslint"
      ],
      "settings": {
        "editor.defaultFormatter": "esbenp.prettier-vscode",
        "editor.formatOnSave": true,
        "eslint.validate": ["javascript", "typescript"]
      }
    }
  },
EOF
            ;;
        golang)
            cat >> "$config_file" << 'EOF'
  "customizations": {
    "vscode": {
      "extensions": [
        "golang.go"
      ],
      "settings": {
        "go.toolsManagement.checkForUpdates": "local",
        "go.useLanguageServer": true,
        "go.gopath": "/go",
        "go.goroot": "/usr/local/go"
      }
    }
  },
EOF
            ;;
        rust)
            cat >> "$config_file" << 'EOF'
  "customizations": {
    "vscode": {
      "extensions": [
        "rust-lang.rust-analyzer",
        "vadimcn.vscode-lldb"
      ],
      "settings": {
        "rust-analyzer.checkOnSave.command": "clippy"
      }
    }
  },
EOF
            ;;
        java)
            cat >> "$config_file" << 'EOF'
  "customizations": {
    "vscode": {
      "extensions": [
        "redhat.java",
        "vscjava.vscode-java-debug",
        "vscjava.vscode-maven"
      ]
    }
  },
EOF
            ;;
        php)
            cat >> "$config_file" << 'EOF'
  "customizations": {
    "vscode": {
      "extensions": [
        "bmewburn.vscode-intelephense-client",
        "xdebug.php-debug"
      ]
    }
  },
EOF
            ;;
    esac
    
    # Close the JSON
    echo '  "remoteUser": "dev"' >> "$config_file"
    echo '}' >> "$config_file"
    
    return 0
}