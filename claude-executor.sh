#!/bin/bash

# This script executes Claude CLI in the same context as a terminal session
# to ensure MCP servers and authentication work properly

# Source the user's shell profile to get all environment variables
if [ -f ~/.zshrc ]; then
    source ~/.zshrc
elif [ -f ~/.bash_profile ]; then
    source ~/.bash_profile
elif [ -f ~/.bashrc ]; then
    source ~/.bashrc
fi

# Get the message from the first argument
MESSAGE="$1"

# Get allowed tools from environment variable (default to "*" if not set)
ALLOWED_TOOLS="${ALLOWED_TOOLS:-*}"

# Change to home directory (or you can change to a specific project directory)
cd "$HOME"

# Execute Claude with the message and allowed tools configuration
if [ "$ALLOWED_TOOLS" = "" ]; then
    # If ALLOWED_TOOLS is explicitly empty, don't pass the flag at all
    /opt/homebrew/bin/claude -p "$MESSAGE"
else
    # Pass the allowed tools configuration
    /opt/homebrew/bin/claude -p "$MESSAGE" --allowedTools $ALLOWED_TOOLS
fi