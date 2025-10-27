#!/bin/bash

# ==============================================================================
# VM MANAGEMENT FUNCTIONS
# ==============================================================================

# Source constants
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/constants.sh"

function handle_env_command() {
    local env_command="$1"
    local env_name="$2"
    shift 2  # Remove command and env name
    
    # Process additional flags
    while [[ $# -gt 0 ]]; do
        case $1 in
            --yes|-y)
                AUTO_YES=true
                shift
                ;;
            *)
                echo "❌ Error: Unknown flag for 'env' command: $1"
                exit 1
                ;;
        esac
    done
    
    local setup_dir="$DEV_HOME/setups"
    
    case "$env_command" in
        new|up|down|rm|status)
            if [[ -z "$env_name" ]]; then
                echo "❌ Error: Environment name required"
                echo "Usage: $(basename "$0") env $env_command <environment>"
                echo ""
                echo "Available environments:"
                if [[ -d "$setup_dir" ]]; then
                    ls -1 "$setup_dir" | grep -v '\\.backup\\.' | sed 's/\\..*$//' | sort -u | sed 's/^/  /'
                else
                    echo "  (No environments found - run installer first)"
                fi
                exit 1
            fi
            
            # Handle VM name construction - if env_name already starts with dev-vm-, use it as-is
            local vm_name
            if [[ "$env_name" == dev-vm-* ]]; then
                vm_name="$env_name"
                # Extract the actual environment name for setup file lookup
                local actual_env_name="${env_name#dev-vm-}"
                local setup_file=$(find "$setup_dir" -type f -name "${actual_env_name}.*" 2>/dev/null | head -n 1)
            else
                vm_name="dev-vm-${env_name}"
                local setup_file=$(find "$setup_dir" -type f -name "${env_name}.*" 2>/dev/null | head -n 1)
            fi
            
            case "$env_command" in
                new)
                    echo "🚀 Creating and provisioning '$env_name' using cloud-init..."
                    if [[ ! -f "$setup_file" ]]; then
                        echo "❌ Error: Setup file for '$env_name' not found in $setup_dir"
                        echo ""
                        echo "Available environments:"
                        if [[ -d "$setup_dir" ]]; then
                            ls -1 "$setup_dir" | grep -v '\\.backup\\.' | sed 's/\\..*$//' | sort -u | sed 's/^/  /'
                        fi
                        exit 1
                    fi
                    
                    orb create --user-data "$setup_file" ubuntu "$vm_name"
                    echo "✅ Environment '$env_name' is ready. Connecting..."
                    orb -m "$vm_name"
                    ;;
                up)
                    echo "🚀 Starting environment '$env_name' and connecting..."
                    orb -m "$vm_name"
                    ;;
                down)
                    echo "🛑 Stopping environment '$env_name'..."
                    orb stop "$vm_name"
                    echo "✅ VM '$vm_name' stopped."
                    ;;
                status)
                    echo "📊 Status of environment '$env_name':"
                    echo "   VM Name: $vm_name"
                    
                    # Check if VM exists in orb list and get its status
                    if orb list | grep -q "^$vm_name"; then
                        local vm_info=$(orb list | grep "^$vm_name")
                        local state=$(echo "$vm_info" | awk '{print $2}')
                        local distro=$(echo "$vm_info" | awk '{print $3}')
                        local ip=$(echo "$vm_info" | awk '{print $NF}')
                        
                        case "$state" in
                            running)
                                echo "   Status: ✅ Running"
                                echo "   IP: $ip"
                                echo "   Distro: $distro"
                                ;;
                            stopped)
                                echo "   Status: ⏸️  Stopped"
                                echo "   Distro: $distro"
                                ;;
                            *)
                                echo "   Status: $state"
                                ;;
                        esac
                    else
                        echo "   Status: ❌ VM not found"
                        echo ""
                        echo "💡 Available VMs:"
                        orb list | grep "^dev-vm-" | awk '{print "   " $1 " (" $2 ")"}'
                        if ! orb list | grep -q "^dev-vm-"; then
                            echo "   (No development VMs found)"
                            echo ""
                            echo "💡 Create one with: $(basename "$0") env new docker-host"
                        fi
                    fi
                    ;;
                rm)
                    echo "🔥 Deleting environment '$env_name'..."
                    if [[ "$AUTO_YES" == "true" ]]; then
                        echo "Auto-confirming deletion (--yes flag set)"
                        orb delete "$vm_name"
                        echo "✅ VM '$vm_name' deleted."
                    else
                        read -p "Are you sure you want to permanently delete VM '$vm_name'? (y/N) " -n 1 -r
                        echo
                        if [[ $REPLY =~ ^[Yy]$ ]]; then
                            orb delete "$vm_name"
                            echo "✅ VM '$vm_name' deleted."
                        else
                            echo "Deletion cancelled."
                        fi
                    fi
                    ;;
            esac
            ;;
        list|--help|-h|help)
            echo "Usage: $(basename "$0") env <command> [environment]"
            echo ""
            echo "Environment Management Commands:"
            echo "  new <env>        Create and provision a new environment VM"
            echo "  up <env>         Start an existing environment VM and connect"
            echo "  down <env>       Stop a running environment VM"
            echo "  status <env>     Show status of an environment VM"
            echo "  rm <env>         Delete an environment VM permanently"
            echo "  list             Show this help and available environments"
            echo ""
            echo "Available environments:"
            if [[ -d "$setup_dir" ]]; then
                ls -1 "$setup_dir" | grep -v '\\.backup\\.' | sed 's/\\..*$//' | sort -u | sed 's/^/  /'
            else
                echo "  (No environments found - run installer first)"
            fi
            echo ""
            echo "Examples:"
            echo "  $(basename "$0") env new docker-host    # Create Docker host VM"
            echo "  $(basename "$0") env up docker-host     # Start and connect to VM"
            echo "  $(basename "$0") env status docker-host # Check VM status"
            echo "  $(basename "$0") env down docker-host   # Stop VM to save resources"
            ;;
        *)
            echo "❌ Error: Unknown env command '$env_command'"
            echo "Use '$(basename "$0") env help' for usage information."
            exit 1
            ;;
    esac
}