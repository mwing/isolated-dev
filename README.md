# Isolated Development Environment Tools for Mac using OrbStack

Secure, isolated development environments using OrbStack VMs and Docker containers. Run all development work in sandboxed environments while maintaining seamless integration with your local workflow.

## Table of Contents

- [Use Cases](#use-cases)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Available Templates](#available-templates)
- [Examples](#examples)
- [Commands](#commands)
- [Configuration](#configuration)
- [VS Code Integration](#vs-code-integration)
- [Multi-Architecture Support](#multi-architecture-support)
- [Troubleshooting](#troubleshooting)

## Use Cases

- **Experimentation**: Try new languages without polluting your system
- **Security**: Run untrusted code in isolated environments
- **Team Consistency**: Standardize development environments across team members
- **CI/CD Integration**: Test locally in production-like containers

## Prerequisites

- [OrbStack](https://orbstack.dev/) installed and running
- macOS with Terminal access
- `jq` installed (`brew install jq`)

## Installation

```bash
# Clone the repository
git clone https://github.com/mwing/isolated-dev.git
cd isolated-dev

# Run the installer
./install.sh
```

### Installation Options
```bash
./install.sh --help     # Show all options
./install.sh --force    # Force overwrite existing files
./install.sh --yes      # Skip prompts (for automation)
```

The installer creates:
- Scripts in `~/.local/bin/`
- Global config with defaults (`~/.dev-envs/config.yaml`)
- Language plugins for all supported languages
- VM setup files

### Uninstallation
```bash
./install.sh --uninstall        # Remove files (VMs preserved)
./install.sh --uninstall-all    # Remove everything including VMs
```

## Quick Start

### 1. Create Docker Host VM (one-time setup)
```bash
dev env new docker-host
```

### 2. Create a Project
```bash
# New project with template
mkdir my-app && cd my-app
dev new python-3.13 --init

# Existing project
cd existing-project
dev new python  # Creates Dockerfile
```

### 3. Start Development
```bash
dev        # Build and run container
# OR
dev shell  # Open interactive shell
```

Your project is mounted at `/workspace` in the container with automatic:
- Port forwarding based on project type
- SSH key mounting for git operations
- Git configuration sharing

## Available Templates

| Language | Versions | Usage |
|----------|----------|-------|
| **Python** | 3.11, 3.12, 3.13, 3.14 | `dev new python-3.13` |
| **Node.js** | 20, 22, 25 | `dev new node-22` |
| **Go** | 1.21, 1.22, 1.25 | `dev new golang-1.22` |
| **Rust** | 1.75, 1.90 | `dev new rust-1.90` |
| **Java** | 21 | `dev new java-21` |
| **PHP** | 8.3 | `dev new php-8.3` |
| **Ubuntu** | 22.04, 20.04, 24.04 | `dev new ubuntu-22.04` |

### Template Options
```bash
dev list                          # Show all available templates
dev new python                    # Auto-select version (prompts if multiple)
dev new python-3.13 --init        # Create with starter files
dev new node-22 --devcontainer    # Include VS Code config
```

## Examples

### Python Web Development
```bash
# Create a Flask project with VS Code support
mkdir flask-api && cd flask-api
dev new python-3.13 --init --devcontainer --yes

# Add Flask to requirements.txt
echo "flask" >> requirements.txt

# Start development
dev shell
# Inside container:
pip install -r requirements.txt
python -c "from flask import Flask; app=Flask(__name__); app.run(host='0.0.0.0', port=8000)"
# App accessible at http://localhost:8000
```

### Node.js API Development
```bash
# Create Express API with scaffolding
mkdir express-api && cd express-api
dev new node-22 --init --yes

# Start development
dev
# Inside container:
npm install express
node -e "require('express')().get('/', (req,res) => res.send('Hello')).listen(3000, '0.0.0.0')"
# API accessible at http://localhost:3000
```

### Go Microservice
```bash
# Create Go service
mkdir go-service && cd go-service
dev new golang-1.22 --init --yes

# Development with hot reload
dev shell
# Inside container:
go mod init myservice
go run main.go  # Auto-accessible on port 8080
```

### Existing Project Integration
```bash
# Add containerized development to existing project
cd my-existing-project
dev new python  # Creates Dockerfile based on detected files
dev devcontainer  # Add VS Code support
dev  # Start developing in container
```

### Multi-Architecture Development
```bash
# Develop on Apple Silicon, deploy to Intel servers
dev new rust-1.90 --init
dev --platform linux/amd64 build  # Build for Intel deployment
dev --platform linux/arm64 shell  # Develop natively on Apple Silicon
```

### Team Workflow
```bash
# Team lead sets up project template
dev new node-22 --init --devcontainer --yes
git add . && git commit -m "Add containerized dev environment"
git push

# Team members with isolated-dev installed:
git clone <repo>
cd <project>
dev  # Everything just works

# Team members without isolated-dev (using standard Docker):
git clone <repo>
cd <project>
docker build -t myproject .
docker run -it --rm -v "$(pwd):/workspace" -p 3000:3000 myproject
```

### CI/CD Integration
```bash
# Automated pipeline setup
dev new python-3.13 --init --yes  # No prompts for automation
dev build  # Build container for testing
dev shell -c "python -m pytest"  # Run tests in container
```

## 🎛️ Command Reference

### Environment Management
```bash
dev env list                      # Show help and available environments  
dev env new docker-host           # Create Docker host VM (one-time setup)
dev env up docker-host            # Start the VM
dev env down docker-host          # Stop the VM (save battery)
dev env status docker-host        # Check VM status
dev env rm docker-host            # Permanently delete VM
dev env rm docker-host --yes      # Delete VM without confirmation
```

### Container Development (`dev`)
```bash
dev --help                        # Show comprehensive help
dev list                          # List available language templates
dev new <language>                # Create Dockerfile from template
dev new <language> --init         # Create template with project scaffolding
dev new <language> --devcontainer # Create template with VS Code devcontainer.json
dev new <language> --init --devcontainer --yes  # Full setup with scaffolding and VS Code
dev devcontainer [language]       # Generate VS Code devcontainer.json for existing project
dev                               # Build and run container (default)
dev shell                         # Open interactive bash shell in container
dev build                         # Build image only
dev clean                         # Remove containers and images
dev -f Dockerfile.dev             # Use custom Dockerfile
dev --platform linux/arm64        # Build for specific architecture
dev -y, --yes                     # Automatically answer 'yes' to all prompts
dev arch                          # Show architecture and platform information
dev help <command>                # Get help for specific command
dev troubleshoot                  # Show troubleshooting guide
```

### Configuration Management
```bash
dev config                        # Show current configuration
dev config --edit                 # Edit global configuration
dev config --init                 # Create project-local configuration
dev config --init --yes           # Create project config, auto-overwrite if exists
dev config validate               # Validate configuration files
```

### Security Commands
```bash
dev security scan                 # Scan Dockerfile for security issues
dev security validate             # Validate Dockerfile security best practices
```

### Template Management
```bash
dev templates update              # Update all templates to latest versions
dev templates check               # Check for available template updates
dev templates prune               # Smart cleanup of old/unused templates
dev templates cleanup [days]      # Remove templates unused for X days (default: 60)
dev templates cleanup 30 --yes    # Remove old templates without prompting
dev templates stats               # Show template statistics and usage information
```

### Advanced Options
```bash
# Custom container names and tags
dev -t my-custom-tag -n my-container

# Different Dockerfile locations
dev -f docker/Dockerfile.production

# Multi-architecture builds
dev --platform linux/arm64         # Build for ARM64 (Apple Silicon)
dev --platform linux/amd64         # Build for x86_64 (Intel)
dev arch                           # Show current architecture info

# Using custom environments (instead of default docker-host)
VM_NAME="dev-vm-k8s-dev" dev
VM_NAME="dev-vm-ml-gpu" dev build

# Combining custom environment with other options
VM_NAME="dev-vm-secure" dev -f Dockerfile.secure -t secure-app --platform linux/arm64
```

### Environment Configuration
To permanently use a custom environment, you can modify the `dev` script or set an environment variable:

```bash
# Option 1: Set environment variable for session
export VM_NAME="dev-vm-k8s-dev"
dev

# Option 2: Create environment-specific wrapper script
cat > k8s-container << 'EOF'
#!/bin/bash
VM_NAME="dev-vm-k8s-dev" dev "$@"
EOF
chmod +x k8s-container
./k8s-container new golang
```

## Configuration

### Global Configuration (`~/.dev-envs/config.yaml`)
```yaml
# YAML format (recommended)
vm_name: dev-vm-docker-host
default_template: python-3.13
auto_start_vm: true
container_prefix: dev
```

### Project Configuration (`.devenv.yaml`)
```bash
dev config --init    # Creates project-specific config
```

Example project config:
```yaml
vm_name: dev-vm-my-project
default_template: node-22
container_prefix: myproject
```

### Environment Variable Overrides
```bash
# Override any config value with environment variables
DEV_VM_NAME=custom-vm dev
DEV_DEFAULT_TEMPLATE=python-3.14 dev new python
DEV_CONTAINER_PREFIX=myapp dev build
```

### Configuration Validation
```bash
dev config validate              # Validate all config files
# Checks for:
# - Valid YAML syntax
# - Known configuration keys
# - Correct value types (boolean, string)
# - Valid characters in names
```

## VS Code Integration

Generate VS Code devcontainer configurations for seamless IDE development:

```bash
# Auto-detect project type
dev devcontainer

# Specify language
dev devcontainer python-3.13

# Create new project with VS Code config
dev new node-22 --init --devcontainer
```

**Generated features:**
- Language-specific extensions (Python, Node.js, Go, Rust, Java, PHP)
- Automatic port forwarding
- SSH key and git configuration mounting
- Optimized development settings

**Usage in VS Code:**
1. Open project folder
2. Cmd+Shift+P → "Dev Containers: Reopen in Container"
3. Full IDE features in isolated container

## Multi-Architecture Support

### Architecture Detection
```bash
dev arch    # Show current architecture and platform info
```

### Platform-Specific Builds
```bash
dev --platform linux/arm64     # ARM64 (Apple Silicon)
dev --platform linux/amd64     # x86_64 (Intel)
```

**Performance:**
- Native builds (ARM64 on Apple Silicon, x86_64 on Intel): Fastest
- Cross-platform builds: Slower due to emulation

## Troubleshooting

### Quick Diagnostics
```bash
dev troubleshoot                  # Comprehensive troubleshooting guide
dev help <command>                # Command-specific help
```

### Common Issues

**No Dockerfile found:**
```bash
dev new python                    # Create from template
dev list                          # See available templates
```

**VM not running:**
```bash
dev env status docker-host        # Check status
dev env up docker-host            # Start VM
```

**Port forwarding issues:**
- Ensure app binds to `0.0.0.0`, not `localhost`
- Check for port conflicts on host
- Verify project files exist (package.json, requirements.txt, etc.)

**SSH keys not working:**
```bash
ssh-add -l                        # Check SSH agent
ssh-add ~/.ssh/id_rsa             # Add keys to agent
```

**Architecture issues:**
```bash
dev arch                          # Check current architecture
dev --platform linux/arm64       # Force specific platform
dev clean && dev build            # Rebuild with correct architecture
```

For more help: [https://github.com/mwing/isolated-dev](https://github.com/mwing/isolated-dev)

## Features

- **Multiple Languages**: Python, Node.js, Go, Rust, Java, PHP, Ubuntu templates
- **Security Hardening**: Non-root containers, capability dropping, resource limits, vulnerability scanning
- **OrbStack Integration**: VM isolation, security enforcement, Docker engine integration
- **Auto Port Forwarding**: Detects common development ports
- **Git Integration**: SSH keys and configuration automatically mounted
- **VS Code Support**: Generate devcontainer.json configurations
- **Multi-Architecture**: ARM64 and x86_64 support with auto-detection
- **Smart Templates**: Auto-updating versions from Docker Hub
- **Project Scaffolding**: Language-specific starter files
- **Automation Support**: `--yes` flag for CI/CD pipelines

## License

MIT License - see [LICENSE](LICENSE) file for details.