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
- **🎯 Enhanced Developer Experience**: 
  - Automatic port forwarding detection (Node.js 3000, Python 8000, etc.)
  - SSH key mounting for seamless git operations
  - Git configuration sharing between host and container
- **🧠 Smart Template Matching**: Intelligent version selection when exact versions aren't available
- **⚙️ Comprehensive Configuration**: Global and project-local configuration files
- **🔄 Dynamic Version Detection**: Real-time template updates from Docker Hub APIs
- **📖 Comprehensive Help System**: Command-specific help, troubleshooting guides, and better error messages

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
devenv new docker-host
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
devenv new k8s-dev

# The VM will be named 'dev-vm-k8s-dev'
# You can start/stop it like any other environment
devenv down k8s-dev
devenv up k8s-dev
```

3. **Use with dev:**
```bash
# Update dev to use the custom environment
# Edit the VM_NAME in dev script or use environment variable
VM_NAME="dev-vm-k8s-dev" dev
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
dev list

# Create a new project (smart matching)
mkdir my-python-project && cd my-python-project
dev new python      # Uses python-3.11 automatically

# Or specify exact version
dev new python-3.11 # Explicit version

# Start developing immediately
dev
```

### Method 2: Existing Project
For projects with existing Dockerfiles:

```bash
cd my-existing-project
dev
```

### Method 3: Custom Dockerfile
Create your own Dockerfile and use the development tools:

```bash
cd my-custom-project
# Create your Dockerfile
dev
```

### 📝 Custom Dockerfile Names

If your project already has a `Dockerfile` (e.g., for production), you can use custom names for development:

```bash
# Create development Dockerfile with custom name
dev new python              # Creates "Dockerfile" by default
mv Dockerfile Dockerfile.dev   # Rename for development

# Use the custom Dockerfile
dev -f Dockerfile.dev

# Other naming examples
dev -f docker/Dockerfile.development
dev -f .devcontainer/Dockerfile
dev -f Dockerfile.local
```

**💡 Tip**: This keeps your production `Dockerfile` separate from development configurations, allowing different tool sets, debug symbols, or development-specific optimizations.

-----

## 📋 Available Templates

The following language templates are available with `dev new <language>`:

### 🎯 **Quick Reference**
```bash
# List all templates with versions
dev list

# Create with latest/default version (smart matching)
dev new python    # Multiple versions available - will prompt
dev new node      # Multiple versions available - will prompt

# Create with specific version
dev new python-3.13
dev new node-22
dev new golang-1.22

# Create with project scaffolding (starter files)
dev new python-3.13 --init    # Creates requirements.txt, main.py, .gitignore
dev new node-22 --init        # Creates package.json, index.js, .gitignore
dev new golang-1.22 --init    # Creates go.mod, main.go, .gitignore
```

### 📋 **Template Catalog**

| Language | Available Versions | Latest Template | Includes |
|----------|-------------------|----------------|-----------|
| **Python** | 3.11, 3.12, 3.13 | `python-3.13` | Python interpreter, pip, development tools |
| **Node.js** | 20, 22 | `node-22` | Node.js LTS, npm, development tools |
| **Go** | 1.21, 1.22 | `golang-1.22` | Go compiler, go tools, gopls, delve debugger |
| **Rust** | 1.75 | `rust-1.75` | Rust toolchain, cargo, clippy, rustfmt |
| **Java** | 21 | `java-21` | OpenJDK 21 LTS, Maven 3.9.5, Gradle 8.4 |
| **PHP** | 8.3 | `php-8.3` | PHP 8.3, Composer, common extensions |
| **Bash** | latest | `bash-latest` | Shell scripting tools, shellcheck, utilities |

### 🔮 **Smart Template Matching & Dynamic Updates**
- **Unversioned names** (e.g., `python`) automatically use the available version
- **Multiple versions** will prompt you to specify which one you want
- **Exact names** (e.g., `python-3.11`) work as expected
- **Dynamic version detection** fetches latest versions from Docker Hub APIs
- **Auto-updating templates** ensure you always have access to current versions
- **Future versions** are automatically discovered and made available

All templates include:
- 🔧 **Essential tools**: git, vim, curl, wget, jq, tree, htop
- 👤 **Non-root user**: Secure development environment
- 🎨 **Color terminal**: Enhanced developer experience
- 🏗️ **Build tools**: Language-specific development toolchain

### 🚀 **Project Initialization**
Add `--init` to any `dev new` command to create language-specific starter files:

| Language | Scaffolding Includes |
|----------|---------------------|
| **Python** | `requirements.txt`, `main.py`, `.gitignore` |
| **Node.js** | `package.json`, `index.js`, `.gitignore` |
| **Go** | `go.mod`, `main.go`, `.gitignore` |
| **Java** | `pom.xml`, `src/main/java/.../Main.java`, `.gitignore` |
| **Rust** | `Cargo.toml`, `src/main.rs`, `.gitignore` |
| **PHP** | `composer.json`, `index.php`, `src/`, `.gitignore` |
| **Bash** | `script.sh`, `README.md`, `.gitignore` |

-----

## ✨ Enhanced Developer Experience

The isolated development environment automatically enhances your workflow with intelligent features:

### 🌐 **Automatic Port Forwarding**
No need to manually configure ports - the system detects common development ports based on your project:

| Framework Detection | Auto-forwarded Port | Trigger Files |
|-------------------|-------------------|---------------|
| **Node.js** | `3000` | `package.json` |
| **Python Flask/Django** | `8000` | `requirements.txt`, `app.py` |
| **Go** | `8080` | `go.mod`, `main.go` |
| **Rust** | `8000` | `Cargo.toml` |
| **Java Spring** | `8080` | `pom.xml`, `build.gradle` |
| **PHP** | `8080` | `composer.json`, `index.php` |

Just run your app inside the container - it's automatically accessible on your host!

### 🔑 **Seamless Git Integration**
Your SSH keys and git configuration are automatically mounted:
- **SSH Keys**: `~/.ssh/` directory mounted for git operations
- **Git Config**: Your `.gitconfig` shared for consistent commits  
- **SSH Agent**: Forwarded for seamless authentication

```bash
# Inside container - works automatically with your existing setup
git clone git@github.com:your-org/your-repo.git
git commit -m "Made changes from container"
git push origin main
```



### 🎯 **Smart Project Detection**
The system intelligently configures itself based on project contents:
- Detects language from files (package.json, requirements.txt, etc.)
- Configures appropriate port forwarding for the detected framework
- Sets up seamless git integration with SSH key forwarding

-----

## 💻 Development Workflow

### 1. Create or Navigate to Project
```bash
# New project with template
mkdir my-app && cd my-app
dev new python-3.13 --init  # Creates Dockerfile + project scaffolding

# Existing project
cd existing-project
```

### 2. Start Development Environment
```bash
dev        # Build and run container
# OR
dev shell  # Open interactive bash shell
```

This automatically:
- 🏗️ Builds Docker image from your Dockerfile
- 🚀 Starts container with your project mounted at `/workspace`
- 🔗 Connects you to an interactive shell inside the container
- 🌐 **Auto port forwarding**: Detects and forwards common development ports
- 🔑 **SSH key mounting**: Seamlessly access git repositories with your existing keys
- ⚙️ **Git integration**: Shares your git configuration for consistent commits

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

## ⚙️ **Optional: Customize Your Setup**

### Set Default Templates
Avoid version prompts by setting preferred defaults:
```bash
dev config --edit    # Opens global config
# Set: default_template = "python-3.13"
```

### Per-Project Configuration
Create project-specific settings:
```bash
cd my-special-project
dev config --init    # Creates .devenv.yaml
# Customize VM, templates, container names per project
```

-----

## 🎛️ Command Reference

### Environment Management (`devenv`)
```bash
devenv --help                     # Show help and available environments
devenv new docker-host            # Create Docker host VM (one-time setup)
devenv up docker-host             # Start the VM
devenv down docker-host           # Stop the VM (save battery)
devenv rm docker-host             # Permanently delete VM
```

### Container Development (`dev`)
```bash
dev --help                        # Show comprehensive help
dev list                          # List available language templates
dev new <language>                # Create Dockerfile from template
dev new <language> --init         # Create template with project scaffolding
dev                               # Build and run container (default)
dev shell                         # Open interactive bash shell in container
dev build                         # Build image only
dev clean                         # Remove containers and images
dev -f Dockerfile.dev             # Use custom Dockerfile
dev help <command>                # Get help for specific command
dev troubleshoot                  # Show troubleshooting guide
```

### Configuration Management
```bash
dev config                        # Show current configuration
dev config --edit                 # Edit global configuration
dev config --init                 # Create project-local configuration
```

### Template Management
```bash
dev templates update              # Update all templates to latest versions
dev templates check               # Check for available template updates
```



### Advanced Options
```bash
# Custom container names and tags
dev -t my-custom-tag -n my-container

# Different Dockerfile locations
dev -f docker/Dockerfile.production

# Using custom environments (instead of default docker-host)
VM_NAME="dev-vm-k8s-dev" dev
VM_NAME="dev-vm-ml-gpu" dev build

# Combining custom environment with other options
VM_NAME="dev-vm-secure" dev -f Dockerfile.secure -t secure-app
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

-----

## 🔧 Configuration

### Configuration System
The isolated development environment supports both global and project-local configuration:

#### Global Configuration (`~/.dev-envs/config.yaml`)
```yaml
# Default VM name to use for containers
vm_name = "dev-vm-docker-host"

# Default template when language has multiple versions
default_template = "python-3.13"

# Automatically start VM if not running
auto_start_vm = "true"

# Prefix for container and image names
container_prefix = "dev"
```

#### Project-Local Configuration (`.devenv.yaml`)
Create in any project directory to override global settings:
```bash
dev config --init    # Creates .devenv.yaml in current directory
```

Example project config:
```yaml
# Project-specific overrides
vm_name = "dev-vm-my-project"
default_template = "node-22"
container_prefix = "myproject"
```

### Directory Structure
After installation:
```
~/.dev-envs/
├── config.yaml      # Global configuration file
├── setups/           # VM configuration files
│   └── docker-host.yaml
└── templates/        # Dockerfile templates
    ├── Dockerfile-python-3.11
    ├── Dockerfile-python-3.12
    ├── Dockerfile-python-3.13
    ├── Dockerfile-node-20
    ├── Dockerfile-node-22
    ├── Dockerfile-golang-1.21
    ├── Dockerfile-golang-1.22
    ├── Dockerfile-rust-1.75
    ├── Dockerfile-java-21
    ├── Dockerfile-php-8.3
    └── Dockerfile-bash-latest
```

### VM Management
The Docker host VM runs automatically but you can manage it manually:

```bash
# Check VM status
orb status dev-vm-docker-host

# Connect to VM directly (advanced)
orb -m dev-vm-docker-host

# VM lifecycle
devenv down docker-host     # Stop when not needed
devenv up docker-host       # Restart (or let dev do it)
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

### Quick Help
```bash
dev troubleshoot                  # Show comprehensive troubleshooting guide
dev help <command>                # Get help for specific commands
dev --help                        # Show all available commands and options
```

### Common Issues

**"No Dockerfile found"**
```bash
# Create one from template
dev new python

# Or use custom path
dev -f path/to/Dockerfile

# See all available templates
dev list
```

**"Docker host VM not running"**
```bash
# Start it manually
devenv up docker-host

# Or let dev start it automatically
dev

# Check VM status
devenv status
```

**"Port forwarding not working"**
- Ensure your application binds to `0.0.0.0`, not `localhost`
- Check if port is already in use on host
- Verify framework-specific files exist (package.json, requirements.txt, etc.)

**"SSH keys not working in container"**
```bash
# Check SSH agent is running
ssh-add -l

# Add keys to agent
ssh-add ~/.ssh/id_rsa

# Verify keys exist
ls -la ~/.ssh/
```

**"Templates not found or outdated"**
```bash
# Update templates from Docker Hub
dev templates update

# Check for available updates
dev templates check

# Reinstall to get latest templates
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
devenv --help           # Environment management help
dev --help              # Container development help
dev help <command>      # Command-specific help (new, config, templates)
dev troubleshoot        # Comprehensive troubleshooting guide
./install.sh --help     # Installation and uninstall options
```

For more help, visit: [https://github.com/mwing/isolated-dev](https://github.com/mwing/isolated-dev)

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
