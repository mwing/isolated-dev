FROM ubuntu:24.04

# Create non-root user with explicit UID 1000
RUN (groupadd -g 1000 appuser 2>/dev/null || groupmod -n appuser $(getent group 1000 | cut -d: -f1)) && \
    (useradd -u 1000 -g 1000 -m -s /bin/bash appuser 2>/dev/null || usermod -l appuser -d /home/appuser -m $(getent passwd 1000 | cut -d: -f1))

# Install system dependencies and shell tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    bash-completion \
    git \
    vim \
    curl \
    less \
    shellcheck \
    && rm -rf /var/lib/apt/lists/*

# Optional: Uncomment to enable sudo for development tasks
# RUN apt-get update && apt-get install -y sudo && rm -rf /var/lib/apt/lists/* && \
#     echo 'appuser ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers

# Set working directory and change ownership
WORKDIR /workspace
RUN chown -R 1000:1000 /workspace

# Switch to non-root user by UID
USER 1000:1000

# Keep container running
CMD ["bash"]