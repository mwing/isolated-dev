# Bash Development Environment with Ubuntu {{VERSION}}
FROM ubuntu:{{VERSION}}

# Set working directory
WORKDIR /workspace

# Install common development tools and utilities
RUN apt-get update && apt-get install -y \
    bash \
    bash-completion \
    git \
    curl \
    wget \
    vim \
    nano \
    tree \
    jq \
    shellcheck \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

# Copy Git configuration (if mounted)
RUN if [ -f /tmp/.gitconfig ]; then cp /tmp/.gitconfig /root/.gitconfig; fi

# Set default command
CMD ["/bin/bash"]