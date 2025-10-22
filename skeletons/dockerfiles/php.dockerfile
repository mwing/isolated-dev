# PHP Dockerfile skeleton
FROM php:{{VERSION}}
WORKDIR /var/www/{{PROJECT_NAME}}
COPY . .
CMD ["php", "index.php"]
# PHP Development Environment with PHP {{VERSION}}
FROM php:{{VERSION}}-cli

# Set working directory
WORKDIR /workspace

# Install system dependencies and PHP extensions
RUN apt-get update && apt-get install -y \
    git \
    curl \
    wget \
    vim \
    nano \
    tree \
    zip \
    unzip \
    libzip-dev \
    libicu-dev \
    libonig-dev \
    && docker-php-ext-install zip intl mbstring \
    && rm -rf /var/lib/apt/lists/*

# Install Composer globally
RUN curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer

# Copy Git configuration (if mounted)  
RUN if [ -f /tmp/.gitconfig ]; then cp /tmp/.gitconfig /root/.gitconfig; fi

# Set default command
CMD ["/bin/bash"]