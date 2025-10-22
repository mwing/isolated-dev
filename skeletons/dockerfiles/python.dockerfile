FROM python:{{VERSION}}-slim

# Install system dependencies
RUN apt-get update && apt-get install -y \
    build-essential \
    curl \
    git \
    vim \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /workspace

# Install common Python development tools
RUN pip install --no-cache-dir \
    pip-tools \
    black \
    flake8 \
    pytest \
    ipython \
    jupyter

# Copy requirements if they exist
COPY requirements*.txt* ./
RUN if [ -f requirements.txt ]; then pip install -r requirements.txt; fi

# Keep container running
CMD ["bash"]