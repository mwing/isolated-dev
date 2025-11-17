# Isolated Development Environment Tools for Mac using OrbStack

**Secure, lightweight, Mac-native development environments** that protect against supply chain attacks and dependency conflicts. Zero-configuration containerized development with automatic image management - just code, don't manage Docker.

## Why Use Isolated Development?

**Security & Isolation:**
- **Supply chain protection** - Run untrusted dependencies in sandboxed containers
- **Project isolation** - Each project runs in its own secure environment
- **Zero host pollution** - No language runtimes or packages installed on your Mac
- **Attack surface reduction** - Malicious code can't access your system

**Lightweight & Fast:**
- **Quick startup** - Containers boot in seconds, not minutes
- **Mac-native performance** - OrbStack's optimized virtualization
- **Automatic management** - No Docker image maintenance required
- **Minimal overhead** - Efficient resource usage

**Developer Experience:**
- **Zero configuration** - Templates handle all setup automatically
- **Multiple projects** - Switch between isolated environments instantly
- **Seamless integration** - SSH keys, git config, and ports work transparently

## Table of Contents

- [Why Use Isolated Development?](#why-use-isolated-development)
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
- [Network Optimization](#network-optimization)
- [Troubleshooting](#troubleshooting)
- [📚 Development Cookbook](COOKBOOK.md) - Practical recipes and examples

## Use Cases

- **Security-first development** - Protect against supply chain attacks and malicious dependencies
- **Multi-project workflows** - Handle dozens of projects without version conflicts
- **Safe experimentation** - Try new languages, frameworks, and tools risk-free
- **Team standardization** - Consistent environments across all developers
- **Dependency testing** - Safely evaluate new packages and versions
- **Legacy project maintenance** - Run old projects without installing outdated runtimes

> **For complex setups:** Use VS Code Dev Containers, Docker Compose, or Podman for multi-service architectures, custom networking, or advanced orchestration needs. Or add a pull request that supports the features you need.

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
./install.sh --help        # Show all options
./install.sh --force       # Force overwrite existing files
./install.sh --yes         # Skip prompts (for automation)
./install.sh --completion  # Install zsh/bash completion (optional)
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
dev -e VAR_NAME                   # Pass environment variable from host
dev -e VAR=value                  # Pass environment variable with value
dev -e 'VAR=value with spaces'    # Pass environment variable with spaces (quote entire argument)
dev --env-file .env               # Load environment variables from file
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
dev security check                # Comprehensive security check (Dockerfile + vulnerabilities)
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
# YAML format only
vm_name: dev-vm-docker-host
default_template: python-3.13
auto_start_vm: true
container_prefix: dev

# Network optimization settings
network_mode: bridge                    # bridge, host, none, or custom network name
auto_host_networking: false             # Auto-use host networking for single services
port_range: "3000-9000"                 # Port range for auto-detection
enable_port_health_check: true          # Check if ports are accessible
port_health_timeout: 5                  # Timeout for port health checks (seconds)

# Resource limits (applied at container runtime)
memory_limit: ""                        # Memory limit (e.g., "512m", "1g")
cpu_limit: ""                           # CPU limit (e.g., "0.5", "1.0")

# File mounting (security: disabled by default)
mount_ssh_keys: false                   # Mount ~/.ssh for git operations
mount_git_config: false                 # Mount ~/.gitconfig

# Port forwarding (optional)
forward_ports: ""                        # Override auto-detected ports (e.g., "8080,9000")

# Cache and performance settings
cache_ttl: 86400                         # API cache TTL in seconds (default: 1 day)
cache_max_size: 100                      # Max cache size in MB (default: 100MB)
min_disk_space: 5                        # Minimum free disk space in GB (default: 5GB)

# Environment variables to pass to containers
pass_env_vars:
  # Patterns to match (supports wildcards with *)
  patterns:
    - AWS_*
    - SNYK_*
    - GITHUB_*
    - NODE_ENV
    - DEBUG
  # Explicit variable names (no wildcards)
  explicit: []
```

> **Note**: Only YAML format is supported for configuration files. The legacy `key=value` format has been removed for better consistency and validation.

### Project Configuration (`.devenv.yaml`)
```bash
dev config --init    # Creates project-specific config
```

Example project config:
```yaml
vm_name: dev-vm-my-project
default_template: node-22
container_prefix: myproject

# Port forwarding (optional)
forward_ports: "8080,9000"        # Override auto-detected ports

# File mounting (optional, security: disabled by default)
mount_ssh_keys: true              # Mount ~/.ssh for git operations
mount_git_config: true            # Mount ~/.gitconfig

# Environment variables (optional)
pass_env_vars:
  patterns:
    - DATABASE_*
  explicit:
    - API_KEY
```

### Environment Variable Overrides
```bash
# Override any config value with environment variables
DEV_VM_NAME=custom-vm dev
DEV_DEFAULT_TEMPLATE=python-3.14 dev new python
DEV_CONTAINER_PREFIX=myapp dev build

# Network configuration overrides
DEV_NETWORK_MODE=host dev                           # Use host networking
DEV_AUTO_HOST_NETWORKING=true dev                   # Enable auto host networking
DEV_PORT_RANGE="8000-8999" dev                      # Custom port range
DEV_ENABLE_PORT_HEALTH_CHECK=false dev              # Disable port health checks
DEV_PORT_HEALTH_TIMEOUT=10 dev                      # Custom health check timeout

# Resource limit overrides
DEV_MEMORY_LIMIT="512m" dev                         # Limit container memory
DEV_CPU_LIMIT="0.5" dev                             # Limit container CPU usage

# File mounting overrides (security: disabled by default)
DEV_MOUNT_SSH_KEYS=true dev                         # Mount SSH keys for one command
DEV_MOUNT_GIT_CONFIG=true dev                       # Mount git config for one command

# Port forwarding override
DEV_FORWARD_PORTS="8080,9000" dev                   # Override auto-detected ports

# Cache and disk space overrides
DEV_CACHE_TTL=3600 dev                              # Set cache TTL to 1 hour
DEV_CACHE_MAX_SIZE=50 dev                           # Limit cache to 50MB
DEV_MIN_DISK_SPACE=10 dev                           # Require 10GB free space
```

### Configuration Validation
```bash
dev config validate              # Validate all config files
# Checks for:
# - Valid YAML syntax (only format supported)
# - Known configuration keys
# - Correct value types (boolean, string, number)
# - Valid characters in names
# - Network configuration values
```

### File Mounting (Security)

**SSH keys and git config are NOT mounted by default** for security. Enable only when needed:

```yaml
# In ~/.dev-envs/config.yaml or .devenv.yaml
mount_ssh_keys: true              # Mount ~/.ssh for git operations
mount_git_config: true            # Mount ~/.gitconfig
```

**Per-command override:**
```bash
DEV_MOUNT_SSH_KEYS=true dev shell    # Mount SSH keys for this session only
```

**Why disabled by default:**
- Reduces attack surface if container is compromised
- Prevents accidental exposure of credentials
- Follows principle of least privilege

**When to enable:**
- Need to clone/push to private git repositories
- Need git commit signing
- Need SSH access to remote servers from container

### Environment Variable Passing

Automatically pass environment variables to containers based on configuration:

```yaml
# In ~/.dev-envs/config.yaml or .devenv.yaml
pass_env_vars:
  # Patterns to match (supports wildcards with *)
  patterns:
    - AWS_*          # Passes AWS_PROFILE, AWS_REGION, etc.
    - SNYK_*         # Passes SNYK_TOKEN, SNYK_ORG, etc.
    - GITHUB_*       # Passes GITHUB_TOKEN, etc.
    - NODE_ENV       # Exact match
    - DEBUG          # Exact match
  # Explicit variable names (no wildcards)
  explicit:
    - CUSTOM_VAR
    - API_KEY
```

**Usage:**
```bash
# Set environment variables on your host
export AWS_PROFILE=dev
export SNYK_TOKEN=abc123
export GITHUB_TOKEN=xyz789

# Run container - variables automatically passed
dev shell

# Inside container, variables are available
echo $AWS_PROFILE  # outputs: dev
echo $SNYK_TOKEN   # outputs: abc123
```

**Command-line environment variables:**
```bash
# Pass specific variables from host environment
dev -e GITLAB_TOKEN shell
dev -e DATABASE_URL -e API_KEY shell

# Pass variables with explicit values
dev -e NODE_ENV=production shell
dev -e DEBUG=true -e PORT=8080 shell

# Pass variables with spaces (quote the entire argument)
dev -e 'VAR=value with spaces' shell
dev -e "VAR=value with spaces" shell

# Load variables from .env file
dev --env-file .env shell
dev --env-file .env.production shell

# Combine multiple methods
dev -e GITLAB_TOKEN --env-file .env shell
```

**Important**: For environment variables with spaces, quote the entire `VAR=value` argument, not just the value:
- ✅ Correct: `dev -e 'VAR=value with spaces'` 
- ✅ Correct: `dev -e "VAR=value with spaces"`
- ❌ Wrong: `dev -e VAR="value with spaces"`

The quotes must wrap the entire `VAR=value` argument to prevent the shell from treating spaces as argument separators.

**Project-specific variables:**
```bash
# Create project config
dev config --init

# Edit .devenv.yaml to add project-specific variables
pass_env_vars:
  patterns:
    - DATABASE_*
    - API_*
  explicit:
    - CUSTOM_APP_VAR
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

## Network Optimization

### Network Configuration Options

Configure networking behavior through configuration files or environment variables:

```yaml
# Network optimization settings in ~/.dev-envs/config.yaml
network_mode: bridge                    # Default: bridge networking
auto_host_networking: false             # Auto-detect when to use host networking
port_range: "3000-9000"                 # Port range for auto-detection
enable_port_health_check: true          # Verify port accessibility
port_health_timeout: 5                  # Health check timeout (seconds)
```

### Network Modes

**Bridge Mode (Default):**
- Isolated container networking
- Port forwarding required
- Best for security and multi-container setups

**Host Mode:**
- Direct access to host network
- No port forwarding needed
- Better performance for single services
- Use when `auto_host_networking: true` or `network_mode: host`

**Custom Networks:**
- Set `network_mode` to custom network name
- Enables service discovery between containers
- Useful for multi-container development

### Port Management

**Auto-Detection:**
- Scans project files for framework-specific ports
- Respects configured `port_range`
- Automatically forwards detected ports

**Manual Override:**
```yaml
# In ~/.dev-envs/config.yaml or .devenv.yaml
forward_ports: "8080,9000"  # Comma-separated list of ports
```

```bash
# Or use environment variable
DEV_FORWARD_PORTS="8080,9000" dev
```

**Health Checking:**
- Verifies port accessibility when `enable_port_health_check: true`
- Configurable timeout with `port_health_timeout`
- Helps identify port conflicts

### Performance Tips

1. **Use host networking for single services:**
   ```bash
   DEV_NETWORK_MODE=host dev
   ```

2. **Optimize port range for your stack:**
   ```yaml
   port_range: "8000-8999"  # Narrow range for faster detection
   ```

3. **Disable health checks for faster startup:**
   ```yaml
   enable_port_health_check: false
   ```

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
- Override auto-detected ports: `DEV_FORWARD_PORTS="8080,9000" dev`

**Environment variable issues:**
- For variables with spaces, quote the entire argument: `dev -e 'VAR=value with spaces'`
- Avoid quoting only the value: `dev -e VAR="value with spaces"` (incorrect)
- Use configuration files for complex variables: `.devenv.yaml` with `pass_env_vars`

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

## Additional Resources

- [📚 Development Cookbook](COOKBOOK.md) - Practical recipes and examples for common scenarios
- [GitHub Repository](https://github.com/mwing/isolated-dev) - Source code, issues, and contributions
- [Language Plugin Guide](languages/README.md) - How to add new language support

## Contributing

Contributions are welcome! To ensure code quality and functionality:

### Running Tests
```bash
# Run all tests
./test.sh

# In CI environments (automatically skips OrbStack-dependent tests)
CI=true ./test.sh

# Run only integration tests (requires OrbStack)
./integration-test.sh
```

**Test Coverage:**
- Configuration system (YAML parsing, precedence, validation)
- Environment variable passing (patterns, special characters, files)
- Template generation and project detection
- Security validation
- Error handling

Tests automatically skip OrbStack-dependent tests when `CI=true` is set. Please run tests before submitting pull requests.

### Adding Language Support

See the [Language Plugin Guide](languages/README.md) for information on adding new language templates and extending the plugin architecture.

## Auto-completion

Optional bash/zsh completion for enhanced productivity:

```bash
# Install completion
./install.sh --completion

# Or install manually
bash completions/install-completion.sh .
```

**Features:**
- Tab completion for all commands and flags
- Dynamic language template suggestions
- Context-aware completions
- VM name completion from OrbStack

**Examples:**
```bash
dev <TAB>                    # Shows: new, list, config, security...
dev new <TAB>                # Shows: python-3.13, node-22, golang-1.22...
dev config <TAB>             # Shows: --edit, --init, validate
dev --platform <TAB>         # Shows: linux/amd64, linux/arm64
```

## Features

- **Multiple Languages**: Python, Node.js, Go, Rust, Java, PHP, Ubuntu templates
- **Security Hardening**: Non-root containers, capability dropping, resource limits, vulnerability scanning
- **OrbStack Integration**: VM isolation, security enforcement, Docker engine integration
- **Auto Port Forwarding**: Detects common development ports
- **Git Integration**: SSH keys and configuration automatically mounted
- **VS Code Support**: Generate devcontainer.json configurations
- **Multi-Architecture**: ARM64 and x86_64 support with auto-detection
- **Smart Templates**: Auto-updating versions from Docker Hub with caching
- **Project Scaffolding**: Language-specific starter files
- **Automation Support**: `--yes` flag for CI/CD pipelines
- **Performance Optimized**: Cached API calls, optimized port detection, unified project detection

## License

MIT License - see [LICENSE](LICENSE) file for details.