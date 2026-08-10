#!/bin/bash

# ==============================================================================
# LANGUAGE PLUGIN SYSTEM
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/constants.sh"

function detect_project_type_from_languages() {
    local languages_dir="$1"
    local detected_lang=""
    local detected_version=""
    local confidence="low"
    
    if [[ ! -d "$languages_dir" ]]; then
        echo "::low"
        return
    fi
    
    # Check each language directory
    for lang_dir in "$languages_dir"/*; do
        if [[ -d "$lang_dir" && -f "$lang_dir/language.yaml" ]]; then
            local lang=$(basename "$lang_dir")
            
            # Get detection files for this language (hardcoded for now)
            local detection_files=""
            case "$lang" in
                python)
                    detection_files="requirements.txt pyproject.toml setup.py Pipfile"
                    ;;
                node)
                    detection_files="package.json"
                    ;;
                golang)
                    detection_files="go.mod main.go"
                    ;;
                rust)
                    detection_files="Cargo.toml"
                    ;;
                java)
                    detection_files="pom.xml build.gradle build.gradle.kts"
                    ;;
                php)
                    detection_files="composer.json index.php"
                    ;;
                bash)
                    detection_files="*.sh"
                    ;;
                kotlin)
                    detection_files="build.gradle.kts settings.gradle.kts"
                    ;;
            esac
            
            # Check if any detection files exist
            for file in $detection_files; do
                if [[ -f "$file" ]] || [[ "$file" == "*.sh" && -n "$(ls *.sh 2>/dev/null)" ]]; then
                    detected_lang="$lang"
                    confidence="high"
                    
                    # Try to extract version
                    detected_version=$(extract_version_from_files)
                    break 2
                fi
            done
        fi
    done
    
    echo "$detected_lang:$detected_version:$confidence"
}

function extract_version_from_files() {
    local version=""
    
    # Simple version extraction
    if [[ -f ".python-version" ]]; then
        version=$(cat .python-version | head -1 | cut -d. -f1,2)
    elif [[ -f ".nvmrc" ]]; then
        version=$(cat .nvmrc | head -1 | sed 's/v\?//' | cut -d. -f1)
    elif [[ -f "go.mod" ]]; then
        version=$(grep "^go " go.mod | sed 's/go //' | head -1)
    fi
    
    echo "$version"
}