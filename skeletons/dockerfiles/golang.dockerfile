FROM golang:{{VERSION}}-bullseye

# Install system dependencies
RUN apt-get update && apt-get install -y \
    git \
    vim \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /workspace

# Install common Go tools
RUN go install golang.org/x/tools/cmd/goimports@latest && \
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest && \
    go install github.com/air-verse/air@latest

# Keep container running
CMD ["bash"]