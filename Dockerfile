# Use Ubuntu as the base image for shell scripting
FROM ubuntu:22.04

# Add color support to the terminal
ENV TERM=xterm-256color

# Prevent interactive prompts during package installation
ENV DEBIAN_FRONTEND=noninteractive

# Install shell scripting tools and utilities
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

# Set the working directory
WORKDIR /workspace

# Copy Git configuration (if mounted)
RUN if [ -f /tmp/.gitconfig ]; then cp /tmp/.gitconfig /root/.gitconfig; fi

# Set default command
CMD ["/bin/bash"]