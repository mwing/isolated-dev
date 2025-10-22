# Java Development Environment with OpenJDK {{VERSION}}
FROM openjdk:{{VERSION}}-jdk

# Set working directory
WORKDIR /workspace

# Install common development tools
RUN apt-get update && apt-get install -y \
    git \
    curl \
    wget \
    vim \
    nano \
    tree \
    unzip \
    && rm -rf /var/lib/apt/lists/*

# Install Maven (latest)
RUN curl -fsSL https://archive.apache.org/dist/maven/maven-3/3.9.9/binaries/apache-maven-3.9.9-bin.tar.gz \
    | tar xzf - -C /opt \
    && ln -s /opt/apache-maven-3.9.9 /opt/maven \
    && ln -s /opt/maven/bin/mvn /usr/local/bin/mvn

# Set environment variables
ENV MAVEN_HOME=/opt/maven
ENV PATH=$PATH:$MAVEN_HOME/bin

# Copy Git configuration (if mounted)
RUN if [ -f /tmp/.gitconfig ]; then cp /tmp/.gitconfig /root/.gitconfig; fi

# Set default command
CMD ["/bin/bash"]