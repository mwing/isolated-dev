# Language Plugin System

This directory contains language plugins for the isolated development environment. Each language is self-contained in its own directory.

## Structure

```
languages/
├── python/
│   ├── language.yaml          # Language definition and configuration
│   ├── Dockerfile.template     # Docker template with {{VERSION}} placeholder
│   ├── requirements.txt        # Scaffolding files
│   ├── main.py
│   └── .gitignore
├── node/
│   ├── language.yaml
│   ├── Dockerfile.template
│   ├── package.json
│   ├── index.js
│   └── .gitignore
├── golang/
│   ├── language.yaml
│   ├── Dockerfile.template
│   ├── go.mod
│   ├── main.go
│   └── .gitignore
├── rust/
│   ├── language.yaml
│   ├── Dockerfile.template
│   ├── Cargo.toml
│   ├── src/main.rs
│   └── .gitignore
├── java/
│   ├── language.yaml
│   ├── Dockerfile.template
│   ├── pom.xml
│   ├── Main.java
│   └── .gitignore
├── php/
│   ├── language.yaml
│   ├── Dockerfile.template
│   ├── composer.json
│   ├── index.php
│   └── .gitignore
├── bash/
│   ├── language.yaml
│   ├── Dockerfile.template
│   ├── main.sh
│   └── .gitignore
└── kotlin/                     # Example of new language
    ├── language.yaml
    ├── Dockerfile.template
    ├── build.gradle.kts
    ├── src/main/kotlin/Main.kt
    └── .gitignore
```

## Adding a New Language

To add support for a new language, create a single directory with all required files:

### 1. Create Language Directory
```bash
mkdir languages/mylang
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

# Install dependencies
RUN apt-get update && apt-get install -y git vim

WORKDIR /workspace
CMD ["bash"]
```

### 4. Add Scaffolding Files
Create any starter files that should be generated with `--init`:
- Configuration files
- Main source files
- .gitignore
- Build files

### 5. Update Detection Logic
Add your language to `scripts/lib/languages.sh`:
```bash
mylang)
    detection_files="mylang.config src/main.mylang"
    ;;
```

### 6. Test Your Plugin
```bash
# Create test project
mkdir test-mylang && cd test-mylang
echo "config: true" > mylang.config

# Test detection
dev new --yes
```

## Contributing

1. Fork the repository
2. Add your language directory with all files
3. Test the detection and template generation
4. Submit a pull request

New languages are automatically available after `git pull` and `./install.sh`!

## Available Languages

- **Python** - Python development with pip, virtual environments
- **Node.js** - JavaScript/TypeScript with npm
- **Go** - Go development with modules
- **Rust** - Rust development with Cargo
- **Java** - Java development with Maven/Gradle
- **PHP** - PHP development with Composer
- **Bash** - Shell scripting environment
- **Kotlin** - Kotlin/JVM development (example)

## Language Definition Reference

### language.yaml Format
```yaml
name: string              # Internal name (must match directory)
display_name: string      # Human-readable name
versions: [string]        # Available versions

detection:
  files: [string]         # Files that indicate this language

ports: [number]           # Common development ports

files:
  dockerfile: string      # Dockerfile template filename
  scaffolding: [string]   # Files to create with --init
```

### Template Placeholders
- `{{VERSION}}` - Language version
- `{{PROJECT_NAME}}` - Project directory name
- `{{GO_VERSION}}` - Go version (for go.mod)

### Detection Files
Common patterns:
- **Config files**: `package.json`, `Cargo.toml`, `go.mod`
- **Source files**: `main.py`, `index.js`, `main.go`
- **Build files**: `pom.xml`, `build.gradle`, `requirements.txt`