# Development Cookbook

Practical examples and recipes for common development scenarios using isolated development environments.

## Table of Contents

- [Quick Start Recipes](#quick-start-recipes)
- [Language-Specific Examples](#language-specific-examples)
- [Network Configuration](#network-configuration)
- [Team Workflows](#team-workflows)
- [CI/CD Integration](#cicd-integration)
- [Performance Optimization](#performance-optimization)
- [Troubleshooting Recipes](#troubleshooting-recipes)

## Quick Start Recipes

### 🚀 New Project from Scratch
```bash
# Create a new Python web API
mkdir my-api && cd my-api
dev new python-3.13 --init --devcontainer --yes

# Add dependencies and start coding
echo "flask" >> requirements.txt
dev shell
# Inside container: pip install -r requirements.txt && python main.py
```

### 🔄 Add Containers to Existing Project
```bash
# Add containerized development to existing project
cd existing-project
dev new python  # Auto-detects from requirements.txt/pyproject.toml
dev devcontainer  # Add VS Code support
dev  # Start developing
```

### ⚡ Quick Experimentation
```bash
# Try a new language without installing anything
mkdir rust-experiment && cd rust-experiment
dev new rust-1.90 --init --yes
dev shell
# Inside container: cargo run
```

## Language-Specific Examples

### Python Development

#### Flask Web API
```bash
mkdir flask-api && cd flask-api
dev new python-3.13 --init --devcontainer --yes

# Add Flask and dependencies
cat >> requirements.txt << EOF
flask==3.0.0
python-dotenv==1.0.0
EOF

# Create a simple API
cat > main.py << 'EOF'
from flask import Flask, jsonify
import os

app = Flask(__name__)

@app.route('/')
def hello():
    return jsonify({"message": "Hello from containerized Flask!"})

@app.route('/health')
def health():
    return jsonify({"status": "healthy"})

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5000, debug=True)
EOF

dev shell
# Inside container:
# pip install -r requirements.txt
# python main.py
# API available at http://localhost:5000
```

#### Django Project
```bash
mkdir django-project && cd django-project
dev new python-3.13 --init --yes

cat >> requirements.txt << EOF
django==5.0.0
djangorestframework==3.14.0
EOF

dev shell
# Inside container:
# pip install -r requirements.txt
# django-admin startproject myproject .
# python manage.py runserver 0.0.0.0:8000
```

#### Data Science with Jupyter
```bash
mkdir data-analysis && cd data-analysis
dev new python-3.13 --init --yes

cat >> requirements.txt << EOF
jupyter==1.0.0
pandas==2.1.0
numpy==1.24.0
matplotlib==3.7.0
EOF

dev shell
# Inside container:
# pip install -r requirements.txt
# jupyter notebook --ip=0.0.0.0 --port=8888 --no-browser --allow-root
```

### Node.js Development

#### Express API Server
```bash
mkdir express-api && cd express-api
dev new node-22 --init --devcontainer --yes

# Initialize with Express
cat > package.json << 'EOF'
{
  "name": "express-api",
  "version": "1.0.0",
  "main": "server.js",
  "scripts": {
    "start": "node server.js",
    "dev": "nodemon server.js"
  },
  "dependencies": {
    "express": "^4.18.0",
    "cors": "^2.8.5"
  },
  "devDependencies": {
    "nodemon": "^3.0.0"
  }
}
EOF

cat > server.js << 'EOF'
const express = require('express');
const cors = require('cors');

const app = express();
const PORT = process.env.PORT || 3000;

app.use(cors());
app.use(express.json());

app.get('/', (req, res) => {
  res.json({ message: 'Hello from containerized Express!' });
});

app.get('/api/health', (req, res) => {
  res.json({ status: 'healthy', timestamp: new Date().toISOString() });
});

app.listen(PORT, '0.0.0.0', () => {
  console.log(`Server running on http://localhost:${PORT}`);
});
EOF

dev shell
# Inside container:
# npm install
# npm run dev
```

#### React Development
```bash
mkdir react-app && cd react-app
dev new node-22 --init --yes

dev shell
# Inside container:
# npx create-react-app . --template typescript
# npm start
# App available at http://localhost:3000
```

### Go Development

#### REST API with Gin
```bash
mkdir go-api && cd go-api
dev new golang-1.22 --init --yes

cat > go.mod << 'EOF'
module go-api

go 1.22

require github.com/gin-gonic/gin v1.9.1
EOF

cat > main.go << 'EOF'
package main

import (
    "net/http"
    "github.com/gin-gonic/gin"
)

func main() {
    r := gin.Default()
    
    r.GET("/", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{
            "message": "Hello from containerized Go!",
        })
    })
    
    r.GET("/health", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{
            "status": "healthy",
        })
    })
    
    r.Run("0.0.0.0:8080")
}
EOF

dev shell
# Inside container:
# go mod tidy
# go run main.go
```

#### CLI Tool Development
```bash
mkdir go-cli && cd go-cli
dev new golang-1.22 --init --yes

cat > main.go << 'EOF'
package main

import (
    "fmt"
    "os"
)

func main() {
    if len(os.Args) < 2 {
        fmt.Println("Usage: mycli <command>")
        return
    }
    
    command := os.Args[1]
    fmt.Printf("Executing command: %s\n", command)
}
EOF

dev shell
# Inside container:
# go build -o mycli
# ./mycli hello
```

### Rust Development

#### Web Server with Actix
```bash
mkdir rust-web && cd rust-web
dev new rust-1.90 --init --yes

cat > Cargo.toml << 'EOF'
[package]
name = "rust-web"
version = "0.1.0"
edition = "2021"

[dependencies]
actix-web = "4.4"
tokio = { version = "1.0", features = ["full"] }
serde = { version = "1.0", features = ["derive"] }
EOF

cat > src/main.rs << 'EOF'
use actix_web::{web, App, HttpResponse, HttpServer, Result};
use serde::Serialize;

#[derive(Serialize)]
struct Health {
    status: String,
}

async fn hello() -> Result<HttpResponse> {
    Ok(HttpResponse::Ok().json("Hello from containerized Rust!"))
}

async fn health() -> Result<HttpResponse> {
    Ok(HttpResponse::Ok().json(Health {
        status: "healthy".to_string(),
    }))
}

#[actix_web::main]
async fn main() -> std::io::Result<()> {
    println!("Starting server at http://0.0.0.0:8080");
    
    HttpServer::new(|| {
        App::new()
            .route("/", web::get().to(hello))
            .route("/health", web::get().to(health))
    })
    .bind("0.0.0.0:8080")?
    .run()
    .await
}
EOF

dev shell
# Inside container:
# cargo run
```

## Network Configuration

### Host Networking for Performance
```bash
# Use host networking for single services (better performance)
DEV_NETWORK_MODE=host dev

# Or configure per project (recommended)
dev config --init --yes
cat >> .devenv.yaml << EOF
network_mode: host
auto_host_networking: true
EOF

# Or configure globally for all projects
cat >> ~/.dev-envs/config.yaml << EOF
network_mode: host
auto_host_networking: true
EOF
```

### Custom Port Ranges
```bash
# Configure per project (recommended)
dev config --init --yes
cat >> .devenv.yaml << EOF
port_range: "8000-8999"
enable_port_health_check: true
port_health_timeout: 10
EOF

# Or configure globally for all projects
cat >> ~/.dev-envs/config.yaml << EOF
port_range: "8000-8999"
enable_port_health_check: true
port_health_timeout: 10
EOF

# Or override per session
DEV_PORT_RANGE="3000-3999" dev
```

### Multi-Service Development
```bash
# Configure for microservices development (per project)
dev config --init --yes
cat >> .devenv.yaml << EOF
network_mode: bridge
port_range: "8000-8010"
enable_port_health_check: true
EOF

# Each service gets its own port automatically
# Team shares this config via git
```

## Team Workflows

### Standardized Team Environment
```bash
# Team lead sets up project
mkdir team-project && cd team-project
dev new node-22 --init --devcontainer --yes

# Customize for team needs
cat >> package.json << EOF
{
  "scripts": {
    "setup": "npm install && npm run build",
    "dev": "npm run start:dev",
    "test": "jest",
    "lint": "eslint src/"
  }
}
EOF

# Commit the environment
git add .
git commit -m "Add containerized development environment"
git push

# Team members just need:
# git clone <repo> && cd <project> && dev
```

### Project-Specific Configuration
```bash
# Create project-specific settings
dev config --init --yes

cat >> .devenv.yaml << EOF
vm_name: dev-vm-myproject
container_prefix: myproject
network_mode: bridge
port_range: "3000-3010"
EOF

# Team shares this configuration via git
```

### Onboarding New Developers
```bash
# New developer setup (after installing isolated-dev)
git clone <team-repo>
cd <team-repo>
dev  # Everything just works!

# No need to install language runtimes, databases, etc.
```

## CI/CD Integration

### GitHub Actions
```yaml
# .github/workflows/test.yml
name: Test in Container
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Install isolated-dev
        run: |
          git clone https://github.com/mwing/isolated-dev.git
          cd isolated-dev
          ./install.sh --yes
          
      - name: Setup VM
        run: dev env new docker-host
        
      - name: Run tests
        run: |
          dev new python-3.13 --yes
          dev shell -c "pip install -r requirements.txt && python -m pytest"
```

### Automated Deployment Testing
```bash
# Test deployment builds locally
dev new python-3.13 --init --yes
dev --platform linux/amd64 build  # Build for production architecture
dev shell -c "python -m pytest"   # Run tests in container
```

### Multi-Architecture Builds
```bash
# Build for different architectures
dev --platform linux/arm64 build   # ARM64 (Apple Silicon)
dev --platform linux/amd64 build   # x86_64 (Intel servers)

# Test on both architectures
dev --platform linux/arm64 shell -c "npm test"
dev --platform linux/amd64 shell -c "npm test"
```

## Performance Optimization

### Fast Startup Configuration
```bash
# Per project (recommended)
dev config --init --yes
cat >> .devenv.yaml << EOF
auto_start_vm: true
network_mode: host
enable_port_health_check: false
port_range: "8000-8010"  # Narrow range
memory_limit: "512m"     # Limit memory for faster startup
cpu_limit: "0.5"         # Limit CPU usage
EOF

# Or globally in ~/.dev-envs/config.yaml
```

### Development vs Production Builds
```bash
# Development (fast iteration)
dev new python-3.13 --init --yes
dev shell  # Quick development

# Production testing (optimized build)
cat > Dockerfile.prod << 'EOF'
FROM python:3.13-slim
RUN groupadd -r app && useradd -r -g app app
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
USER app
CMD ["python", "main.py"]
EOF

dev -f Dockerfile.prod build
```

### Resource Optimization
```bash
# Configure resource limits per project (recommended)
dev config --init --yes
cat >> .devenv.yaml << EOF
memory_limit: "512m"
cpu_limit: "0.5"
EOF

# Or configure globally
cat >> ~/.dev-envs/config.yaml << EOF
memory_limit: "1g"
cpu_limit: "1.0"
EOF

# Or override per session
DEV_MEMORY_LIMIT="256m" DEV_CPU_LIMIT="0.25" dev

# Resource limits are automatically applied when running containers
dev shell  # Runs with configured limits
```

## Troubleshooting Recipes

### Port Conflicts
```bash
# Check what's using ports
dev troubleshoot

# Use different port range
DEV_PORT_RANGE="9000-9999" dev

# Use host networking to avoid conflicts
DEV_NETWORK_MODE=host dev
```

### SSH Key Issues
```bash
# Ensure SSH agent is running
ssh-add -l

# Add your keys
ssh-add ~/.ssh/id_rsa

# Test git access in container
dev shell -c "git clone git@github.com:user/repo.git"
```

### Architecture Mismatches
```bash
# Check current architecture
dev arch

# Force specific platform
dev --platform linux/amd64 build  # For Intel deployment
dev --platform linux/arm64 build  # For ARM deployment

# Clean and rebuild
dev clean && dev build
```

### VM Issues
```bash
# Check VM status
dev env status docker-host

# Restart VM
dev env down docker-host
dev env up docker-host

# Recreate VM if needed
dev env rm docker-host --yes
dev env new docker-host
```

### Configuration Problems
```bash
# Validate configuration
dev config validate

# Reset to defaults
mv ~/.dev-envs/config.yaml ~/.dev-envs/config.yaml.backup
dev config  # Creates new default config

# Check environment overrides
DEV_VM_NAME=test-vm dev config
```

### Performance Issues
```bash
# Use host networking for better performance
DEV_NETWORK_MODE=host dev

# Disable health checks for faster startup
DEV_ENABLE_PORT_HEALTH_CHECK=false dev

# Use smaller port range
DEV_PORT_RANGE="8000-8005" dev
```

### Container Build Failures
```bash
# Check Dockerfile syntax
dev security validate

# Build with verbose output
dev build --verbose  # Future feature

# Clean and rebuild
dev clean
dev build

# Try different base image version
dev new python-3.12 --yes  # Use different version
```

---

## Tips and Best Practices

### 🎯 Development Workflow
1. **Start with detection**: Let `dev new` auto-detect your project type
2. **Use --init for new projects**: Gets you started with working code
3. **Add --devcontainer for VS Code**: Seamless IDE integration
4. **Commit the environment**: Share Dockerfile and configs with your team

### 🚀 Performance Tips
1. **Use host networking for single services**: Better performance, fewer port conflicts
2. **Narrow port ranges**: Faster port detection and startup
3. **Disable health checks if not needed**: Faster container startup
4. **Use native architecture**: ARM64 on Apple Silicon, x86_64 on Intel

### 🔒 Security Best Practices
1. **Run security scans**: Use `dev security scan` regularly
2. **Keep base images updated**: Use latest stable versions
3. **Don't commit secrets**: Use environment variables or mounted files
4. **Use non-root containers**: All templates are secure by default

### 👥 Team Collaboration
1. **Use project-specific configs**: `dev config --init` and commit `.devenv.yaml` to git
2. **Standardize environments**: Use same templates and configurations
3. **Version your environment**: Commit Dockerfile and configs to git
4. **Document setup**: Include setup instructions in README

For more examples and community recipes, visit the [GitHub repository](https://github.com/mwing/isolated-dev).