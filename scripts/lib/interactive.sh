#!/bin/bash

# ==============================================================================
# INTERACTIVE MENU FUNCTIONS
# ==============================================================================

function show_interactive_menu() {
    local choice
    
    while true; do
        clear
        echo "================================================================"
        echo "   🚀 Isolated Development Environment (dev) - Main Menu"
        echo "================================================================"
        echo ""
        echo "   1) ✨ Start New Project      (Create Dockerfile from template)"
        echo "   2) 🔧 Configuration          (Manage local/global config)"
        echo "   3) 📦 Manage Environments    (OrbStack VM management)"
        echo "   4) 📚 List Templates         (Show available languages)"
        echo "   5) 🚑 Troubleshoot           (Run diagnostics)"
        echo "   0) 🚪 Exit"
        echo ""
        echo "================================================================"
        echo -n "   Select an option [0-5]: "
        if ! read -r choice; then
            echo ""
            exit 0
        fi
        
        case $choice in
            1)
                interactive_new_project
                ;;
            2)
                interactive_config
                ;;
            3)
                interactive_environments
                ;;
            4)
                list_templates
                echo ""
                read -n 1 -s -r -p "Press any key to return to menu..."
                ;;
            5)
                show_troubleshoot_help
                echo ""
                read -n 1 -s -r -p "Press any key to return to menu..."
                ;;
            0)
                echo "Goodbye! 👋"
                exit 0
                ;;
            *)
                echo "❌ Invalid option. Please try again."
                sleep 1
                ;;
        esac
    done
}

function interactive_new_project() {
    echo ""
    echo "--- Start New Project ---"
    echo ""
    
    # Check if Dockerfile already exists
    if [[ -f "Dockerfile" ]]; then
        echo "⚠️  A Dockerfile already exists in this directory."
        echo -n "Do you want to overwrite it? (y/N): "
        read -r response
        if [[ ! "$response" =~ ^[Yy]$ ]]; then
            return
        fi
    fi
    
    echo "Available languages:"
    echo "  1) Python"
    echo "  2) Node.js"
    echo "  3) Go (Golang)"
    echo "  4) Rust"
    echo "  5) Java"
    echo "  6) PHP"
    echo "  7) Ubuntu (Generic)"
    echo "  0) Cancel"
    echo ""
    echo -n "Select language [1-7]: "
    read -r lang_choice
    
    local lang=""
    case $lang_choice in
        1) lang="python" ;;
        2) lang="node" ;;
        3) lang="golang" ;;
        4) lang="rust" ;;
        5) lang="java" ;;
        6) lang="php" ;;
        7) lang="ubuntu" ;;
        0) return ;;
        *) echo "Invalid option"; sleep 1; return ;;
    esac
    
    # Version Selection Logic
    local selected_version=""
    local lang_dir="$LANGUAGES_DIR/$lang"
    
    if [[ -f "$lang_dir/language.yaml" ]]; then
        # Extract versions from language.yaml
        # Format in yaml is: versions: ["3.11", "3.12", "3.13"]
        local versions_line=$(grep "versions:" "$lang_dir/language.yaml" | head -1)
        if [[ "$versions_line" =~ versions:[[:space:]]*\[(.*)\] ]]; then
            local versions_str="${BASH_REMATCH[1]}"
            # Convert "3.11", "3.12" to array
            # Remove quotes and commas
            local clean_versions=$(echo "$versions_str" | sed 's/"//g' | sed 's/,/ /g')
            read -r -a versions_array <<< "$clean_versions"
            
            if [[ ${#versions_array[@]} -gt 1 ]]; then
                echo ""
                echo "Available versions for $lang:"
                local i=1
                for ver in "${versions_array[@]}"; do
                    echo "  $i) $ver"
                    ((i++))
                done
                echo ""
                echo -n "Select version [1-${#versions_array[@]}]: "
                read -r ver_choice
                
                if [[ "$ver_choice" -ge 1 && "$ver_choice" -le "${#versions_array[@]}" ]]; then
                    # Array is 0-indexed, choice is 1-indexed
                    selected_version="${versions_array[$((ver_choice-1))]}"
                else
                    echo "Invalid version selected. Using default."
                fi
            elif [[ ${#versions_array[@]} -eq 1 ]]; then
                selected_version="${versions_array[0]}"
            fi
        fi
    fi
    
    # Construct the full language string (e.g., python-3.12)
    local lang_arg="$lang"
    if [[ -n "$selected_version" ]]; then
        lang_arg="$lang-$selected_version"
    fi
    
    echo ""
    echo -n "Initialize project scaffolding (files like main.py, package.json)? (y/N): "
    read -r init_response
    local init_flag=""
    if [[ "$init_response" =~ ^[Yy]$ ]]; then
        init_flag="--init"
    fi
    
    echo -n "Generate VS Code devcontainer configuration? (y/N): "
    read -r devcontainer_response
    local devcontainer_flag=""
    if [[ "$devcontainer_response" =~ ^[Yy]$ ]]; then
        devcontainer_flag="--devcontainer"
    fi
    
    echo ""
    echo "🚀 Creating project..."
    
    # Construct args array
    local args=("$lang_arg")
    [[ -n "$init_flag" ]] && args+=("$init_flag")
    [[ -n "$devcontainer_flag" ]] && args+=("$devcontainer_flag")
    
    handle_new_command "${args[@]}"
    
    echo ""
    read -n 1 -s -r -p "Press any key to return to menu..."
}

function interactive_config() {
    while true; do
        clear
        echo "--- Configuration ---"
        echo "1) Show current config"
        echo "2) Edit global config"
        echo "3) Create local config (.devenv.yaml)"
        echo "4) Validate config"
        echo "0) Back"
        echo ""
        echo -n "Select option: "
        if ! read -r choice; then
            return
        fi
        
        case $choice in
            1)
                handle_config_command ""
                read -n 1 -s -r -p "Press any key to continue..."
                ;;
            2)
                handle_config_command "--edit"
                ;;
            3)
                handle_config_command "--init"
                read -n 1 -s -r -p "Press any key to continue..."
                ;;
            4)
                handle_config_command "validate"
                read -n 1 -s -r -p "Press any key to continue..."
                ;;
            0)
                return
                ;;
            *)
                echo "Invalid option"
                sleep 1
                ;;
        esac
    done
}

function interactive_environments() {
    while true; do
        clear
        echo "--- Manage Environments ---"
        echo "1) List environments"
        echo "2) Status of docker-host"
        echo "3) Start docker-host"
        echo "4) Stop docker-host"
        echo "5) Remove environment"
        
        # Check for integration test VMs
        local test_vms=$(orb list 2>/dev/null | grep "^dev-vm-integration-test-" | awk '{print $1}')
        if [[ -n "$test_vms" ]]; then
            echo "6) 🧹 Cleanup test environments ($(echo "$test_vms" | wc -l | tr -d ' ') found)"
        fi
        
        echo "0) Back"
        echo ""
        echo -n "Select option: "
        if ! read -r choice; then
            return
        fi
        
        case $choice in
            1)
                handle_env_command "list"
                read -n 1 -s -r -p "Press any key to continue..."
                ;;
            2)
                handle_env_command "status" "docker-host"
                read -n 1 -s -r -p "Press any key to continue..."
                ;;
            3)
                handle_env_command "up" "docker-host"
                read -n 1 -s -r -p "Press any key to continue..."
                ;;
            4)
                handle_env_command "down" "docker-host"
                read -n 1 -s -r -p "Press any key to continue..."
                ;;
            5)
                interactive_remove_vm
                ;;
            6)
                if [[ -n "$test_vms" ]]; then
                    interactive_cleanup_test_vms
                else
                    echo "Invalid option"
                    sleep 1
                fi
                ;;
            0)
                return
                ;;
            *)
                echo "Invalid option"
                sleep 1
                ;;
        esac
    done
}

function interactive_remove_vm() {
    echo ""
    echo "Fetching available VMs..."
    local vms=$(orb list 2>/dev/null | grep "^dev-vm-" | awk '{print $1}')
    
    if [[ -z "$vms" ]]; then
        echo "No development VMs found."
        read -n 1 -s -r -p "Press any key to continue..."
        return
    fi
    
    echo "Select VM to remove:"
    local i=1
    local vm_array=()
    while IFS= read -r vm; do
        echo "  $i) $vm"
        vm_array+=("$vm")
        ((i++))
    done <<< "$vms"
    echo "  0) Cancel"
    
    echo ""
    echo -n "Select VM [1-${#vm_array[@]}]: "
    read -r vm_choice
    
    if [[ "$vm_choice" -ge 1 && "$vm_choice" -le "${#vm_array[@]}" ]]; then
        local selected_vm="${vm_array[$((vm_choice-1))]}"
        # handle_env_command expects the suffix if it starts with dev-vm-, 
        # but looking at vm.sh logic:
        # if [[ "$env_name" == dev-vm-* ]]; then vm_name="$env_name" ...
        # So we can pass the full name.
        handle_env_command "rm" "$selected_vm"
        read -n 1 -s -r -p "Press any key to continue..."
    elif [[ "$vm_choice" == "0" ]]; then
        return
    else
        echo "Invalid selection."
        sleep 1
    fi
}

function interactive_cleanup_test_vms() {
    echo ""
    local test_vms=$(orb list 2>/dev/null | grep "^dev-vm-integration-test-" | awk '{print $1}')
    local count=$(echo "$test_vms" | wc -l | tr -d ' ')
    
    echo "Found $count integration test VMs:"
    echo "$test_vms"
    echo ""
    echo "⚠️  This will permanently delete all these VMs."
    echo -n "Are you sure? (y/N): "
    read -r confirm
    
    if [[ "$confirm" =~ ^[Yy]$ ]]; then
        echo ""
        while IFS= read -r vm; do
            echo "Deleting $vm..."
            orb delete "$vm"
        done <<< "$test_vms"
        echo ""
        echo "✅ Cleanup complete."
        read -n 1 -s -r -p "Press any key to continue..."
    else
        echo "Cancelled."
        sleep 1
    fi
}
