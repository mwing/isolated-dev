FROM node:{{VERSION}}-slim

# Install system dependencies
RUN apt-get update && apt-get install -y \
    git \
    vim \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /workspace

# Install common development tools
RUN npm install -g \
    nodemon \
    typescript \
    @types/node \
    eslint \
    prettier

# Copy package files if they exist
COPY package*.json* ./
RUN if [ -f package.json ]; then npm install; fi

# Keep container running
CMD ["bash"]