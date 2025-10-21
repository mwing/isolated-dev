-----

# Isolated Development Environment Tools

A comprehensive toolkit for creating secure, isolated development environments using OrbStack VMs and Docker containers. This system protects your host machine by running all development work in sandboxed environments while maintaining seamless integration with your local development workflow.

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

-----

## 🏗️ One-Time Setup: Creating the Host VM

Before working on projects, create the Docker host VM (only needed once):

```bash
env-ctl create docker-host
```

This creates a lightweight Linux VM running Docker. After setup, you'll be connected to it. Type `exit` to return to your Mac - the VM continues running in the background.

-----

## 🛠️ Quick Start

### Method 1: Use Templates (Recommended)
Create a new project with a pre-configured environment:

```bash
# List available language templates
dev-container --list-templates

# Create a new project
mkdir my-python-project && cd my-python-project
dev-container --create python

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

-----

## 📋 Available Templates

The following language templates are available with `dev-container --create <language>`:

| Language | Template | Includes |
|----------|----------|----------|
| **Python** | `python` | Python 3.11, pip, development tools |
| **Node.js** | `node` | Node.js 20 LTS, npm, development tools |
| **Go** | `golang` | Go 1.21, go tools, gopls, delve debugger |
| **Rust** | `rust` | Rust 1.75, cargo tools, clippy, rustfmt |
| **Java** | `java` | OpenJDK 21, Maven 3.9.5, Gradle 8.4 |
| **PHP** | `php` | PHP 8.3, Composer, common extensions |
| **Bash** | `bash` | Shell scripting tools, shellcheck, utilities |

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

### Getting Help
```bash
env-ctl --help          # Environment management help
dev-container --help    # Container development help
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
