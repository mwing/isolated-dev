# Installation Guide

## Quick Installation (Recommended)

### One-Line Installation
```bash
curl -fsSL https://github.com/mwing/isolated-dev/releases/latest/download/install.sh | bash
```

### With Options
```bash
# Automated installation with shell completion
curl -fsSL https://github.com/mwing/isolated-dev/releases/latest/download/install.sh | bash -s -- --yes --completion

# Force reinstall over existing installation
curl -fsSL https://github.com/mwing/isolated-dev/releases/latest/download/install.sh | bash -s -- --force
```

## Alternative Installation Methods

### Manual Download
```bash
# Download and verify
wget https://github.com/mwing/isolated-dev/releases/latest/download/install.sh
wget https://github.com/mwing/isolated-dev/releases/latest/download/checksums.txt

# Verify checksum
sha256sum -c checksums.txt --ignore-missing

# Install
chmod +x install.sh
./install.sh
```

### Traditional Tarball
```bash
# Download tarball
wget https://github.com/mwing/isolated-dev/releases/latest/download/isolated-dev-1.0.0.tar.gz

# Extract and install
tar xzf isolated-dev-1.0.0.tar.gz
cd isolated-dev-1.0.0
./install.sh
```

### Development Installation
```bash
# Clone repository
git clone https://github.com/mwing/isolated-dev.git
cd isolated-dev

# Install from source
./install.sh
```

## Installation Options

| Option | Description |
|--------|-------------|
| `--force` | Overwrite existing files without prompting |
| `--yes`, `-y` | Skip all confirmation prompts (for automation) |
| `--completion` | Install shell completion for bash/zsh |
| `--help`, `-h` | Show installation help |

## What Gets Installed

The installer creates:
- **Scripts**: `~/.local/bin/dev`, `~/.local/bin/dev-env`
- **Configuration**: `~/.dev-envs/config.yaml`
- **Language plugins**: `~/.dev-envs/languages/`
- **Templates**: `~/.dev-envs/templates/`
- **Libraries**: `~/.dev-envs/lib/`

## Prerequisites

- **macOS** (Intel or Apple Silicon)
- **OrbStack** - [Download here](https://orbstack.dev/)
- **jq** - Install with `brew install jq`

The installer will check for these and help install missing dependencies.

## Verification

After installation, verify everything works:

```bash
# Check installation
dev --help

# Verify VM creation works
dev env new docker-host

# Test project creation
mkdir test-project && cd test-project
dev new python-3.13 --init
dev shell
```

## Uninstallation

### Remove Files Only (Preserve VMs)
```bash
curl -fsSL https://github.com/mwing/isolated-dev/releases/latest/download/install.sh | bash -s -- --uninstall
```

### Complete Removal (Including VMs)
```bash
curl -fsSL https://github.com/mwing/isolated-dev/releases/latest/download/install.sh | bash -s -- --uninstall-all
```

### Manual Removal
```bash
# Remove scripts
rm -f ~/.local/bin/dev ~/.local/bin/dev-env

# Remove configuration and data
rm -rf ~/.dev-envs

# Remove VMs (optional)
orb list | grep "^dev-vm-" | xargs -I {} orb delete {}
```

## Troubleshooting

### Common Issues

**Permission denied**:
```bash
chmod +x install.sh
```

**OrbStack not found**:
```bash
# Install OrbStack first
open https://orbstack.dev/
```

**jq not found**:
```bash
brew install jq
```

**Network issues**:
```bash
# Use manual download method
wget https://github.com/mwing/isolated-dev/releases/latest/download/install.sh
```

### Getting Help

- Check the [troubleshooting guide](README.md#troubleshooting)
- [Open an issue](https://github.com/mwing/isolated-dev/issues/new/choose)
- Run `dev troubleshoot` after installation

## Security

The installer:
- ✅ Verifies checksums automatically
- ✅ Uses HTTPS for all downloads
- ✅ Creates files with secure permissions
- ✅ Never requires sudo/root access
- ✅ All code is open source and auditable

For maximum security, review the installer script before running:
```bash
curl -fsSL https://github.com/mwing/isolated-dev/releases/latest/download/install.sh | less
```