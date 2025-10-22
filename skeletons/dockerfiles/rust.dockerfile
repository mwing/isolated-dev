# Rust Dockerfile skeleton
FROM rust:{{VERSION}}-slim
WORKDIR /workspace
COPY . .
RUN cargo build --release
CMD ["./target/release/{{PROJECT_NAME}}"]
RUN apt-get update && apt-get install -y \
    build-essential \
    pkg-config \
    libssl-dev \
    git \
    vim \
    curl \
    && rm -rf /var/lib/apt/lists/*
FROM rust:{{VERSION}}-slim

# Install system dependencies
RUN apt-get update && apt-get install -y \
    build-essential \
    pkg-config \
    libssl-dev \
    git \
    vim \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /workspace

# Install common Rust tools
RUN cargo install cargo-watch cargo-edit

# Copy Cargo files if they exist
COPY Cargo*.toml* ./
RUN if [ -f Cargo.toml ]; then cargo fetch; fi

# Keep container running
CMD ["bash"]