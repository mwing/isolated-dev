-----

# Isolated Development Environment Guide

This guide explains how to use your custom scripts (`env-ctl` and `dev-container`) to create secure, isolated development environments for any language using Docker. The main goal is to run all dependency installations and code execution inside a sandboxed container to protect your host Mac.

-----

## \#\# One-Time Setup: Creating the Host VM

Before you can work on any projects, you need to create the single, generic `docker-host` virtual machine. You only need to do this once.

1.  Open your terminal.
2.  Run the following command:
    ```bash
    env-ctl create docker-host
    ```

This will create and provision a lightweight Linux VM that runs the Docker engine. After it's done, you'll be connected to it. You can simply type `exit` to return to your Mac. The VM will keep running in the background.

-----

## \#\# Project Workflow

Follow these steps for each new or existing project you want to work on.

### 1\. Create a `Dockerfile`

In the root directory of your project, create a file named `Dockerfile`. This file is the blueprint for your project's specific environment.

#### **Python Project Example**

For a Python project, your `Dockerfile` should look like this:

```dockerfile
# Start from a slim Python image
FROM python:3.11-slim

# Enable terminal colors
ENV TERM=xterm-256color

# Set the working directory (must be /app to match the script)
WORKDIR /app

# Set the default command to an interactive bash shell
CMD [ "bash" ]
```

#### **Node.js Project Example**

For a Node.js project, your `Dockerfile` is very similar:

```dockerfile
# Start from the Node.js 20 LTS image
FROM node:20

# Enable terminal colors
ENV TERM=xterm-256color

# Set the working directory (must be /app to match the script)
WORKDIR /app

# Set the default command to an interactive bash shell
CMD [ "bash" ]
```

### 2\. Enter the Isolated Environment

Navigate to your project's root directory (the one containing the `Dockerfile`) in your terminal and run:

```bash
dev-container
```

This command will automatically:

1.  Build a Docker image based on your `Dockerfile`.
2.  Start a new container from that image.
3.  Mount your current project folder into the container's `/app` directory.
4.  Drop you into a `bash` shell inside the container.

### 3\. Install Dependencies & Develop

You are now inside the secure sandbox. All commands you run here are isolated from your Mac.

  * To install dependencies, run the package manager's command. For example:
      * **Python:** `pip install -r requirements.txt`
      * **Node.js:** `npm install`
  * Run your development server, tests, or any other scripts as you normally would.
  * Edit your code on your Mac using your favorite editor (like VS Code). The changes are instantly reflected inside the container because the folder is mounted.
  * When you are done, just type `exit` to leave the container.

-----

## \#\# Managing the Host VM

The `docker-host` VM runs in the background. You generally don't need to interact with it, but here are the commands if you do:

  * **To stop the VM** (e.g., to save battery):
    ```bash
    env-ctl stop docker-host
    ```
  * **To start the VM again:** The `dev-container` script will start it for you automatically. Alternatively, you can start it manually with:
    ```bash
    env-ctl start docker-host
    ```
