# Language Plugin System

Language plugins for the isolated development environment. Each language is self-contained in its own directory with templates and configuration.

## Table of Contents

- [Available Languages](#available-languages)
- [Quick Start](#quick-start)
- [Adding a New Language](#adding-a-new-language)
- [Plugin Structure](#plugin-structure)
- [Configuration Reference](#configuration-reference)

## Available Languages

| Language | Versions | Detection Files | Ports |
|----------|----------|----------------|-------|
| **Python** | 3.11, 3.12, 3.13, 3.14 | requirements.txt, pyproject.toml | 8000, 5000 |
| **Node.js** | 20, 22, 25 | package.json | 3000, 8080, 5000 |
| **Go** | 1.21, 1.22, 1.25 | go.mod, main.go | 8080 |
| **Rust** | 1.75, 1.90 | Cargo.toml | 8000 |
| **Java** | 21 | pom.xml, build.gradle | 8080 |
| **PHP** | 8.3 | composer.json, index.php | 8080 |
| **Bash** | latest | *.sh files | - |
| **Kotlin** | 1.9 | build.gradle.kts | 8080 |

## Quick Start

### Using Existing Languages
```bash
# List available languages
dev list

# Create project with language template
dev new python-3.13 --init
dev new node-22 --devcontainer
```

### Adding Your Language
```bash
# 1. Create language directory
mkdir languages/mylang

# 2. Add required files (see structure below)
# 3. Test the plugin
dev new mylang --init
```

## Adding a New Language

### 1. Create Directory Structure
```bash
mkdir languages/mylang
cd languages/mylang
```

### 2. Create language.yaml
```yaml
name: mylang
display_name: My Language
versions: ["1.0", "2.0"]

detection:
  files: [mylang.config, src/main.mylang]

ports: [8080]

files:
  dockerfile: Dockerfile.template
  scaffolding: [mylang.config, src/main.mylang, .gitignore]
```

### 3. Create Dockerfile.template
```dockerfile
FROM mylang:{{VERSION}}

# Create non-root user
RUN useradd -m -s /bin/bash dev
USER dev

# Install development tools
RUN apt-get update && apt-get install -y git vim curl

WORKDIR /workspace
CMD ["bash"]
```

### 4. Add Scaffolding Files
Create starter files for `--init` flag:

**mylang.config:**
```
# {{PROJECT_NAME}} configuration
version: 1.0
```

**src/main.mylang:**
```mylang
// Hello world in {{PROJECT_NAME}}
print("Hello from MyLang!")
```

**.gitignore:**
```
# MyLang specific
*.mylang-cache
build/
```

### 5. Test Your Plugin
```bash
# Test detection
mkdir test-project && cd test-project
echo "config: true" > mylang.config
dev new  # Should detect mylang

# Test template creation
dev new mylang-1.0 --init --yes
```

## Plugin Structure

```
languages/mylang/
├── language.yaml          # Language configuration
├── Dockerfile.template    # Docker template with {{VERSION}} placeholder
├── mylang.config         # Scaffolding files (created with --init)
├── src/main.mylang
└── .gitignore
```

### Required Files
- **language.yaml** - Language definition and metadata
- **Dockerfile.template** - Docker container template
- **Scaffolding files** - Starter files for new projects

### Optional Files
- Any additional scaffolding files listed in `language.yaml`
- Build configuration files (pom.xml, package.json, etc.)

## Configuration Reference

### language.yaml Format
```yaml
name: string              # Internal name (must match directory)
display_name: string      # Human-readable name for UI
versions: [string]        # Available versions

detection:
  files: [string]         # Files that indicate this language
  version_files:          # Optional: version detection
    - file: string        # File to check
      extract: string     # Regex to extract version

ports: [number]           # Common development ports for auto-forwarding

files:
  dockerfile: string      # Dockerfile template filename
  scaffolding: [string]   # Files to create with --init flag
```

### Template Placeholders
Available in all template files:

| Placeholder | Description | Example |
|-------------|-------------|---------|
| `{{VERSION}}` | Language version | `3.13`, `22`, `1.90` |
| `{{PROJECT_NAME}}` | Project directory name | `my-api`, `web-app` |
| `{{GO_VERSION}}` | Go version (go.mod only) | `1.22` |

### Detection Patterns
Common file patterns for language detection:

| Language | Primary Files | Secondary Files |
|----------|---------------|----------------|
| **Python** | requirements.txt, pyproject.toml | setup.py, Pipfile |
| **Node.js** | package.json | .nvmrc |
| **Go** | go.mod | main.go |
| **Rust** | Cargo.toml | src/main.rs |
| **Java** | pom.xml, build.gradle | src/main/java |
| **PHP** | composer.json | index.php |

### Port Conventions
Common development ports by language:

- **Web frameworks**: 3000, 8000, 8080
- **APIs**: 3000, 8080, 5000
- **Databases**: 5432, 3306, 27017
- **Development servers**: 4000, 5000, 8080

## Examples

### Minimal Language Plugin
```yaml
# languages/simple/language.yaml
name: simple
display_name: Simple Language
versions: ["1.0"]
detection:
  files: [simple.txt]
ports: [8080]
files:
  dockerfile: Dockerfile.template
  scaffolding: [simple.txt, .gitignore]
```

### Advanced Language Plugin
```yaml
# languages/advanced/language.yaml
name: advanced
display_name: Advanced Language
versions: ["2.0", "2.1", "3.0"]

detection:
  files: [advanced.config, src/main.adv]
  version_files:
    - file: advanced.config
      extract: "version:\\s*([0-9]+\\.[0-9]+)"

ports: [8080, 9000]

files:
  dockerfile: Dockerfile.template
  scaffolding: [advanced.config, src/main.adv, build.xml, .gitignore]
```

## Contributing

1. Fork the repository
2. Create your language directory in `languages/`
3. Add all required files following the structure above
4. Test with `dev new yourlang --init`
5. Submit a pull request

New languages are automatically available after installation!

## License

Language plugins follow the same MIT License as the main project.