#!/bin/bash

# ==============================================================================
# PROJECT INITIALIZATION FUNCTIONS
# ==============================================================================

# Source constants
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/constants.sh"
source "$SCRIPT_DIR/utils.sh"
source "$SCRIPT_DIR/templates.sh"

function show_new_help() {
    echo "Usage: $(basename "$0") new [<language>] [--init] [--devcontainer]"
    echo ""
    echo "Create a Dockerfile from a language template."
    echo ""
    echo "Arguments:"
    echo "  <language>     Language template (python, node, golang, rust, java, php, bash)"
    echo "                 Optionally specify version: python-3.12, node-22, etc."
    echo "                 If omitted, auto-detects project type from current directory"
    echo ""
    echo "Options:"
    echo "  --init         Also create project scaffolding for the language"
    echo "  --devcontainer Also generate VS Code devcontainer.json configuration"
    echo "  --yes, -y      Skip confirmation prompts"
    echo ""
    echo "Examples:"
    echo "  $(basename "$0") new                   # Auto-detect project type"
    echo "  $(basename "$0") new python           # Latest Python template"
    echo "  $(basename "$0") new python-3.11      # Specific Python version"
    echo "  $(basename "$0") new node --init       # Node.js with package.json"
    echo "  $(basename "$0") new rust --init --devcontainer  # Rust with VS Code config"
}

function show_devcontainer_help() {
    echo "Usage: $(basename "$0") devcontainer [<language>] [-f <dockerfile>]"
    echo ""
    echo "Generate VS Code devcontainer.json configuration for seamless IDE integration."
    echo ""
    echo "Arguments:"
    echo "  <language>     Language template (python, node, golang, rust, java, php, bash)"
    echo "                 Optionally specify version: python-3.12, node-22, etc."
    echo "                 If omitted, auto-detects project type from current directory"
    echo ""
    echo "Options:"
    echo "  -f FILE        Path to Dockerfile (default: ./Dockerfile)"
    echo "  --yes, -y      Skip confirmation prompts"
    echo ""
    echo "What it creates:"
    echo "  • .devcontainer/devcontainer.json with language-specific settings"
    echo "  • Automatic port forwarding based on detected project type"
    echo "  • SSH key and git configuration mounting"
    echo "  • Language-specific VS Code extensions and settings"
    echo ""
    echo "Examples:"
    echo "  $(basename "$0") devcontainer              # Auto-detect project type"
    echo "  $(basename "$0") devcontainer python       # Python devcontainer"
    echo "  $(basename "$0") devcontainer node-22      # Node.js v22 devcontainer"
    echo "  $(basename "$0") devcontainer rust -f Dockerfile.dev  # Custom Dockerfile"
    echo ""
    echo "After generation:"
    echo "  1. Open project in VS Code"
    echo "  2. Install 'Dev Containers' extension"
    echo "  3. Cmd+Shift+P → 'Dev Containers: Reopen in Container'"
}

function handle_new_command() {
    # Parse flags first to handle cases like "dev new --yes"
    local init_project=false
    local generate_devcontainer=false
    local language=""
    
    # Process all arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            --help|-h)
                show_new_help
                exit 0
                ;;
            --init)
                init_project=true
                shift
                ;;
            --devcontainer)
                generate_devcontainer=true
                shift
                ;;
            --yes|-y)
                AUTO_YES=true
                shift
                ;;
            -*)
                echo "❌ Error: Unknown flag for 'new' command: $1"
                exit 1
                ;;
            *)
                if [[ -z "$language" ]]; then
                    language="$1"
                else
                    echo "❌ Error: Multiple language arguments provided: $language and $1"
                    exit 1
                fi
                shift
                ;;
        esac
    done
    
    # If no language specified, auto-detect
    if [[ -z "$language" ]]; then
        # Try new language system first, fallback to old system
        if [[ -d "$LANGUAGES_DIR" ]]; then
            detection_result=$(detect_project_type_from_languages "$LANGUAGES_DIR")
        else
            detection_result=$(detect_project_type)
        fi
        detected_lang=$(echo "$detection_result" | cut -d: -f1)
        detected_version=$(echo "$detection_result" | cut -d: -f2)
        confidence=$(echo "$detection_result" | cut -d: -f3)
        
        if [[ -n "$detected_lang" ]]; then
            current_arch=$(detect_architecture)
            suggested_template="$detected_lang"
            
            # Add version if detected and available, otherwise find best match
            if [[ -n "$detected_version" ]]; then
                # Try exact match first
                if [[ -f "$TEMPLATES_DIR/Dockerfile-$detected_lang-$detected_version" ]]; then
                    suggested_template="$detected_lang-$detected_version"
                else
                    # Try major.minor match (e.g., 3.12.0 -> 3.12)
                    major_minor=$(echo "$detected_version" | cut -d. -f1,2)
                    if [[ -f "$TEMPLATES_DIR/Dockerfile-$detected_lang-$major_minor" ]]; then
                        suggested_template="$detected_lang-$major_minor"
                    else
                        # Fall back to latest available
                        latest_template=$(find "$TEMPLATES_DIR" -name "Dockerfile-$detected_lang-*" -type f 2>/dev/null | \
                            sed "s/.*Dockerfile-$detected_lang-//" | \
                            sort -V | tail -1)
                        if [[ -n "$latest_template" ]]; then
                            suggested_template="$detected_lang-$latest_template"
                        fi
                    fi
                fi
            else
                # Find the latest version available locally
                latest_template=$(find "$TEMPLATES_DIR" -name "Dockerfile-$detected_lang-*" -type f 2>/dev/null | \
                    sed "s/.*Dockerfile-$detected_lang-//" | \
                    sort -V | tail -1)
                if [[ -n "$latest_template" ]]; then
                    suggested_template="$detected_lang-$latest_template"
                fi
            fi
            
            echo "🔍 Project Detection Results:"
            echo "   Language: $detected_lang"
            [[ -n "$detected_version" ]] && echo "   Version: $detected_version"
            echo "   Architecture: $current_arch"
            echo "   Confidence: $confidence"
            echo ""
            echo "💡 Suggested template: $suggested_template"
            
            if [[ "$AUTO_YES" == "true" ]]; then
                echo "Auto-confirming suggestion (--yes flag set)"
                create_from_template "$suggested_template" "$init_project" "$TARGET_PLATFORM" "$generate_devcontainer"
            else
                echo -n "Use this template? (Y/n): "
                read -r response
                if [[ -z "$response" ]] || [[ "$response" =~ ^[Yy]$ ]]; then
                    create_from_template "$suggested_template" "$init_project" "$TARGET_PLATFORM" "$generate_devcontainer"
                else
                    echo "Template creation cancelled."
                    echo "Use 'dev list' to see available options or 'dev new <language>' to specify manually."
                    exit 0
                fi
            fi
        else
            echo "🔍 No project files detected in current directory."
            echo "Use 'dev new <language>' to create a template manually."
            echo "Available languages: python, node, golang, rust, java, php, bash"
            echo "Example: dev new python-3.13 --init"
            exit 1
        fi
    else
        # Language specified manually
        create_from_template "$language" "$init_project" "$TARGET_PLATFORM" "$generate_devcontainer"
    fi
}

function handle_devcontainer_command() {
    local language="$1"
    local dockerfile_path="$2"
    
    # Check if Dockerfile exists
    if [[ ! -f "$dockerfile_path" ]]; then
        echo "❌ Error: Dockerfile not found at '$dockerfile_path'"
        echo ""
        echo "💡 Suggestions:"
        echo "   • Create from template: 'dev new <language>'"
        echo "   • Use different file: 'dev devcontainer <language> -f /path/to/Dockerfile'"
        echo "   • See available templates: 'dev list'"
        exit 1
    fi
    
    # Auto-detect language if not provided
    if [[ -z "$language" ]]; then
        echo "🔍 Auto-detecting project type..."
        local detection_result=$(detect_project_type)
        local detected_lang=$(echo "$detection_result" | cut -d: -f1)
        local detected_version=$(echo "$detection_result" | cut -d: -f2)
        
        if [[ -n "$detected_lang" ]]; then
            if [[ -n "$detected_version" ]]; then
                language="$detected_lang-$detected_version"
            else
                language="$detected_lang"
            fi
            echo "   Detected: $language"
        else
            echo "❌ Error: Could not auto-detect project type."
            echo "Please specify the language: 'dev devcontainer <language>'"
            echo "Available languages: python, node, golang, rust, java, php, bash"
            exit 1
        fi
    fi
    
    # Check if .devcontainer already exists
    if [[ -d ".devcontainer" ]]; then
        echo "⚠️  Warning: .devcontainer directory already exists."
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
    
    # Parse language and version
    local base_lang="${language%%-*}"
    local version="${language#*-}"
    if [[ "$base_lang" == "$version" ]]; then
        version=""
    fi
    
    echo "🔧 Generating VS Code devcontainer configuration..."
    echo "   Language: $base_lang"
    [[ -n "$version" ]] && echo "   Version: $version"
    echo "   Dockerfile: $dockerfile_path"
    
    # Generate the devcontainer configuration
    if generate_devcontainer_config "$language" "$version" "$dockerfile_path"; then
        echo ""
        echo "✅ VS Code devcontainer configuration created!"
        echo ""
        echo "📁 Generated files:"
        echo "   .devcontainer/devcontainer.json"
        echo ""
        echo "🚀 Next steps:"
        echo "   1. Open project in VS Code"
        echo "   2. Install the 'Dev Containers' extension if not already installed"
        echo "   3. Press Cmd+Shift+P and select 'Dev Containers: Reopen in Container'"
        echo "   4. VS Code will build and open your project in the isolated container"
        echo ""
        echo "💡 Benefits:"
        echo "   • Full IDE features (IntelliSense, debugging, extensions)"
        echo "   • Automatic port forwarding and SSH key mounting"
        echo "   • Consistent development environment across your team"
    else
        echo "❌ Error: Failed to generate devcontainer configuration"
        exit 1
    fi
    
    exit 0
}
