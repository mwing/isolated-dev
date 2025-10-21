-----

# Isolated Development Environment Tools for Mac using OrbStack

A toolkit for creating secure, isolated development environments using OrbStack VMs and Docker containers. This system protects your host machine by running all development work in sandboxed environments while maintaining seamless integration with your local development workflow.

## 🚀 Features

- **🏗️ Multiple Language Support**: Pre-built templates for Python, Node.js, Go, Rust, Java, PHP, and Bash
- **🔒 Security First**: All development work runs in isolated containers with non-root users
- **⚡ Quick Setup**: One-command environment creation and project bootstrapping
- **🔄 Smart Management**: Automatic VM lifecycle management and resource optimization
- **🛠️ Developer Tools**: Each template includes essential development tools (git, vim, curl, etc.)
- **📦 Template System**: Quickly create new projects with language-specific Dockerfiles

-----

## 📦 Installation

### Prerequisites
- [OrbStack](https://orbstack.dev/) installed and running
- macOS with Terminal access

### Quick Install
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
./install.sh --quiet    # Minimal output
```

The installer will:
- Copy scripts to `~/.local/bin/`
- Install configuration files to `~/.dev-envs/`
- Set up Dockerfile templates for all supported languages
- Optionally add `~/.local/bin` to your PATH

### 🗑️ Uninstallation

If you need to remove the isolated development environment:

```bash
# Remove files and directories only (VMs remain)
./install.sh --uninstall

# Complete removal including all VMs (DESTRUCTIVE)
./install.sh --uninstall-all
```

#### Uninstall Options:

| Option | What Gets Removed | VMs | Data Loss Risk |
|--------|------------------|-----|----------------|
| `--uninstall` | ✅ Scripts<br>✅ Config files<br>✅ Templates | ❌ Preserved | 🟢 **Safe** - No project data lost |
| `--uninstall-all` | ✅ Scripts<br>✅ Config files<br>✅ Templates<br>✅ All dev VMs | ✅ **Deleted** | 🔴 **DESTRUCTIVE** - All VM data lost |

**⚠️ Important Notes:**
- `--uninstall` is safe and preserves your VMs and project data
- `--uninstall-all` will **permanently delete all development VMs** and their data
- Your actual project files (on your Mac) are never affected
- You can manually manage remaining VMs with `orb list` and `orb delete <vm-name>`

**🔄 Re-installation:**
After uninstalling, you can reinstall anytime by running `./install.sh` again.

-----

## 🏗️ One-Time Setup: Creating the Host VM

Before working on projects, create the Docker host VM (only needed once):

```bash
env-ctl create docker-host
```

This creates a lightweight Linux VM running Docker. After setup, you'll be connected to it. Type `exit` to return to your Mac - the VM continues running in the background.

### 🌐 Multiple Environment Support

While most users only need the `docker-host` environment, the system supports multiple environments for advanced use cases:

- **🔧 Tool specialization**: Different VMs with specialized tool sets (Kubernetes, databases, etc.)
- **📊 Resource isolation**: Separate VMs for heavy vs. light workloads  
- **🔒 Security levels**: Isolated environments with different network access policies
- **👥 Team workflows**: Project-specific development environments
- **🧪 Testing environments**: Different OS versions or configurations

#### Creating Custom Environments

1. **Create a cloud-init configuration file:**
```bash
# Example: Create a Kubernetes development environment
cat > ~/.dev-envs/setups/k8s-dev.yaml << 'EOF'
#cloud-config
package_update: true
packages:
  - docker.io
  - curl

users:
  - name: default
    groups: [docker]
    append: true

runcmd:
  # Install kubectl
  - curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
  - install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl
  # Install kind
  - curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64
  - chmod +x ./kind && mv ./kind /usr/local/bin/kind
EOF
```

2. **Create and use the environment:**
```bash
# Create the custom environment
env-ctl create k8s-dev

# The VM will be named 'dev-vm-k8s-dev'
# You can start/stop it like any other environment
env-ctl stop k8s-dev
env-ctl start k8s-dev
```

3. **Use with dev-container:**
```bash
# Update dev-container to use the custom environment
# Edit the VM_NAME in dev-container script or use environment variable
VM_NAME="dev-vm-k8s-dev" dev-container
```

**💡 Pro Tips:**
- Custom environments inherit the same naming pattern: `dev-vm-<environment-name>`
- Cloud-init files support packages, users, files, and arbitrary commands
- Each environment is completely isolated with its own resources
- You can have multiple environments running simultaneously

-----

## 🛠️ Quick Start

### Method 1: Use Templates (Recommended)
Create a new project with a pre-configured environment:

```bash
# List available language templates with versions
dev-container --list-templates

# Create a new project (smart matching)
mkdir my-python-project && cd my-python-project
dev-container --create python      # Uses python-3.11 automatically

# Or specify exact version
dev-container --create python-3.11 # Explicit version

# Start developing immediately
dev-container
```

### Method 2: Existing Project
For projects with existing Dockerfiles:

```bash
cd my-existing-project
dev-container
```

### Method 3: Custom Dockerfile
Create your own Dockerfile and use the development tools:

```bash
cd my-custom-project
# Create your Dockerfile
dev-container
```

### 📝 Custom Dockerfile Names

If your project already has a `Dockerfile` (e.g., for production), you can use custom names for development:

```bash
# Create development Dockerfile with custom name
dev-container --create python  # Creates "Dockerfile" by default
mv Dockerfile Dockerfile.dev   # Rename for development

# Use the custom Dockerfile
dev-container -f Dockerfile.dev

# Other naming examples
dev-container -f docker/Dockerfile.development
dev-container -f .devcontainer/Dockerfile
dev-container -f Dockerfile.local
```

**💡 Tip**: This keeps your production `Dockerfile` separate from development configurations, allowing different tool sets, debug symbols, or development-specific optimizations.

-----

## 📋 Available Templates

The following language templates are available with `dev-container --create <language>`:

### 🎯 **Quick Reference**
```bash
# List all templates with versions
dev-container --list-templates

# Create with latest/default version (smart matching)
dev-container --create python    # Uses python-3.11
dev-container --create node      # Uses node-20

# Create with specific version
dev-container --create python-3.11
dev-container --create node-20
```

### 📋 **Template Catalog**

| Language | Current Version | Template Name | Includes |
|----------|----------------|---------------|-----------|
| **Python** | 3.11 | `python-3.11` | Python 3.11, pip, development tools |
| **Node.js** | 20 LTS | `node-20` | Node.js 20 LTS, npm, development tools |
| **Go** | 1.21 | `golang-1.21` | Go 1.21, go tools, gopls, delve debugger |
| **Rust** | 1.75 | `rust-1.75` | Rust 1.75, cargo tools, clippy, rustfmt |
| **Java** | 21 LTS | `java-21` | OpenJDK 21, Maven 3.9.5, Gradle 8.4 |
| **PHP** | 8.3 | `php-8.3` | PHP 8.3, Composer, common extensions |
| **Bash** | latest | `bash` | Shell scripting tools, shellcheck, utilities |

### 🔮 **Smart Template Matching**
- **Unversioned names** (e.g., `python`) automatically use the available version
- **Multiple versions** will prompt you to specify which one you want
- **Exact names** (e.g., `python-3.11`) work as expected
- **Future versions** can be added without breaking existing workflows

All templates include:
- 🔧 **Essential tools**: git, vim, curl, wget, jq, tree, htop
- 👤 **Non-root user**: Secure development environment
- 🎨 **Color terminal**: Enhanced developer experience
- 🏗️ **Build tools**: Language-specific development toolchain

-----

## 💻 Development Workflow

### 1. Create or Navigate to Project
```bash
# New project with template
mkdir my-app && cd my-app
dev-container --create python

# Existing project
cd existing-project
```

### 2. Start Development Environment
```bash
dev-container
```

This automatically:
- 🏗️ Builds Docker image from your Dockerfile
- 🚀 Starts container with your project mounted at `/app`
- 🔗 Connects you to an interactive shell inside the container

### 3. Develop Inside the Container
All commands run in the secure sandbox:
```bash
# Install dependencies
pip install -r requirements.txt  # Python
npm install                      # Node.js
go mod download                  # Go
cargo build                      # Rust

# Run your application
python app.py
npm start
go run main.go
cargo run
```

### 4. Edit Code on Host
- Use your favorite editor (VS Code, etc.) on your Mac
- Changes are instantly reflected in the container
- Full IDE features work with mounted directories

### 5. Exit When Done
```bash
exit  # Leave container and return to host
```

-----

## 🎛️ Command Reference

### Environment Management (`env-ctl`)
```bash
env-ctl --help                    # Show help and available environments
env-ctl create docker-host        # Create Docker host VM (one-time setup)
env-ctl start docker-host         # Start the VM
env-ctl stop docker-host          # Stop the VM (save battery)
env-ctl delete docker-host        # Permanently delete VM
```

### Container Development (`dev-container`)
```bash
dev-container --help              # Show comprehensive help
dev-container --list-templates    # List available language templates
dev-container --create <language> # Create Dockerfile from template
dev-container                     # Build and run container (default)
dev-container build               # Build image only
dev-container clean               # Remove containers and images
dev-container -f Dockerfile.dev   # Use custom Dockerfile
```

### Advanced Options
```bash
# Custom container names and tags
dev-container -t my-custom-tag -n my-container

# Different Dockerfile locations
dev-container -f docker/Dockerfile.production

# Using custom environments (instead of default docker-host)
VM_NAME="dev-vm-k8s-dev" dev-container
VM_NAME="dev-vm-ml-gpu" dev-container build

# Combining custom environment with other options
VM_NAME="dev-vm-secure" dev-container -f Dockerfile.secure -t secure-app
```

### Environment Configuration
To permanently use a custom environment, you can modify the `dev-container` script or set an environment variable:

```bash
# Option 1: Set environment variable for session
export VM_NAME="dev-vm-k8s-dev"
dev-container

# Option 2: Create environment-specific wrapper script
cat > k8s-container << 'EOF'
#!/bin/bash
VM_NAME="dev-vm-k8s-dev" dev-container "$@"
EOF
chmod +x k8s-container
./k8s-container --create golang
```

-----

## 🔧 Configuration

### Directory Structure
After installation:
```
~/.dev-envs/
├── setups/           # VM configuration files
│   └── docker-host.yaml
└── templates/        # Dockerfile templates
    ├── Dockerfile-python
    ├── Dockerfile-node
    ├── Dockerfile-golang
    ├── Dockerfile-rust
    ├── Dockerfile-java
    ├── Dockerfile-php
    └── Dockerfile-bash
```

### VM Management
The Docker host VM runs automatically but you can manage it manually:

```bash
# Check VM status
orb status dev-vm-docker-host

# Connect to VM directly (advanced)
orb -m dev-vm-docker-host

# VM lifecycle
env-ctl stop docker-host    # Stop when not needed
env-ctl start docker-host   # Restart (or let dev-container do it)
```

-----

## 🎯 Use Cases

### 🧪 **Experimentation**
- Try new languages without polluting your system
- Test different versions of dependencies
- Prototype with unfamiliar toolchains

### 🔒 **Security**
- Run untrusted code in isolated environments
- Prevent dependency conflicts on host system
- Sandbox potentially harmful operations

### 👥 **Team Consistency**
- Standardize development environments across team
- Ensure reproducible builds and deployments
- Onboard new developers quickly

### 🚀 **CI/CD Integration**
- Test locally in production-like containers
- Build deployment-ready images during development
- Validate Dockerfiles before deployment

-----

## 🔍 Troubleshooting

### Common Issues

**"No Dockerfile found"**
```bash
# Create one from template
dev-container --create python

# Or use custom path
dev-container -f path/to/Dockerfile
```

**"Docker host VM not running"**
```bash
# Start it manually
env-ctl start docker-host

# Or let dev-container start it automatically
dev-container
```

**"Permission denied in container"**
- All templates use non-root users for security
- Use `sudo` inside container when needed
- File ownership is handled automatically

**"Templates not found"**
```bash
# Reinstall to update templates
./install.sh --force
```

**"Want to completely start over"**
```bash
# Clean uninstall and reinstall
./install.sh --uninstall-all  # WARNING: Deletes all VMs
./install.sh                  # Fresh installation
```

**"Partial installation issues"**
```bash
# Force reinstall over existing files
./install.sh --force

# Or clean uninstall first, then reinstall
./install.sh --uninstall
./install.sh
```

**"Old VMs taking up space"**
```bash
# List all VMs
orb list

# Delete specific VM
orb delete dev-vm-old-project

# Or use uninstall-all for complete cleanup (DESTRUCTIVE)
./install.sh --uninstall-all
```

### Getting Help
```bash
env-ctl --help          # Environment management help
dev-container --help    # Container development help
./install.sh --help     # Installation and uninstall options
```

-----

## 🤝 Contributing

1. Fork the repository
2. Create feature branch: `git checkout -b feature-name`
3. Add new templates to `/templates/` directory
4. Update documentation
5. Submit pull request

### Adding New Language Templates
Templates should follow the established pattern:
- Use official base images
- Install common development tools
- Create non-root user
- Set up proper working directory
- Include language-specific toolchain

-----

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
