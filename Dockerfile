FROM ubuntu:24.04

# Create non-root user
RUN groupadd -r appuser && useradd -r -g appuser -m appuser

# Install system dependencies and shell tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    bash-completion \
    git \
    vim \
    curl \
    shellcheck \
    && rm -rf /var/lib/apt/lists/*

# Set working directory and change ownership
WORKDIR /workspace
RUN chown -R appuser:appuser /workspace

# Switch to non-root user
USER appuser

# Keep container running
CMD ["bash"]