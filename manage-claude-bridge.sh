#!/bin/bash

# Claude Bridge Service Manager
# Manages the macOS background service for WhatsApp <-> Claude integration

SERVICE_NAME="com.$(whoami).claude-bridge"
PLIST_FILE="$HOME/Library/LaunchAgents/$SERVICE_NAME.plist"
CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_SCRIPT="$CURRENT_DIR/claude-bridge-service.py"
LOG_DIR="$HOME/Library/Logs/ClaudeBridge"
VENV_DIR="$CURRENT_DIR/.venv"
VENV_PYTHON="$VENV_DIR/bin/python"
ENV_FILE="$CURRENT_DIR/.env"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

load_env_file() {
    if [ -f "$ENV_FILE" ]; then
        print_status "Loading environment variables from .env file..."
        # Export variables from .env file, ignoring comments and empty lines
        while IFS='=' read -r key value; do
            # Skip comments and empty lines
            if [[ ! "$key" =~ ^# ]] && [[ -n "$key" ]]; then
                # Remove quotes from value if present
                value="${value%\"}"
                value="${value#\"}"
                export "$key=$value"
            fi
        done < "$ENV_FILE"
        print_success "Environment variables loaded from .env"
    else
        print_warning ".env file not found at $ENV_FILE"
        print_warning "Please create it from .env.example:"
        print_warning "  cp $CURRENT_DIR/.env.example $ENV_FILE"
        print_warning "  nano $ENV_FILE"
        return 1
    fi
}

check_prerequisites() {
    print_status "Checking prerequisites..."
    
    # Check if Claude CLI is installed
    if ! command -v claude &> /dev/null; then
        print_error "Claude CLI not found. Please install it first:"
        print_error "https://docs.anthropic.com/en/docs/claude-code"
        return 1
    else
        CLAUDE_PATH=$(which claude)
        print_success "Claude CLI found at: $CLAUDE_PATH"
    fi
    
    # Check if Python 3 is available
    if ! command -v python3 &> /dev/null; then
        print_error "Python 3 not found. Please install Python 3."
        return 1
    else
        PYTHON_PATH=$(which python3)
        PYTHON_VERSION=$(python3 --version)
        print_success "Python found at: $PYTHON_PATH ($PYTHON_VERSION)"
    fi
    
    # Check if service script exists
    if [ ! -f "$SERVICE_SCRIPT" ]; then
        print_error "Service script not found at: $SERVICE_SCRIPT"
        return 1
    else
        print_success "Service script found: $SERVICE_SCRIPT"
    fi
    
    # Check if uv is available
    if ! command -v uv &> /dev/null; then
        print_error "uv not found. Please install it first:"
        print_error "curl -LsSf https://astral.sh/uv/install.sh | sh"
        return 1
    else
        UV_PATH=$(which uv)
        print_success "uv found at: $UV_PATH"
    fi
    
    print_success "All prerequisites met"
    return 0
}

setup_virtual_environment() {
    print_status "Setting up Python virtual environment..."
    
    # Remove existing venv if it exists
    if [ -d "$VENV_DIR" ]; then
        print_status "Removing existing virtual environment..."
        rm -rf "$VENV_DIR"
    fi
    
    # Create new virtual environment with uv
    print_status "Creating virtual environment with uv..."
    if uv venv "$VENV_DIR" --python python3; then
        print_success "Virtual environment created at: $VENV_DIR"
    else
        print_error "Failed to create virtual environment"
        return 1
    fi
    
    # Install dependencies
    print_status "Installing dependencies with uv..."
    if uv pip install --python "$VENV_PYTHON" aiohttp; then
        print_success "Dependencies installed successfully"
    else
        print_error "Failed to install dependencies"
        return 1
    fi
    
    # Verify installation
    if "$VENV_PYTHON" -c "import aiohttp; print(f'aiohttp {aiohttp.__version__} installed')" 2>/dev/null; then
        print_success "Dependencies verified successfully"
    else
        print_error "Dependency verification failed"
        return 1
    fi
    
    return 0
}

install_service() {
    print_status "Installing Claude Bridge service..."
    
    # Load environment variables from .env file
    if ! load_env_file; then
        return 1
    fi
    
    # Check required environment variables
    if [ -z "$YOUR_PHONE" ]; then
        print_error "YOUR_PHONE variable is not set in .env file"
        print_error "Please edit $ENV_FILE and set YOUR_PHONE=<your_phone_number>"
        return 1
    fi
    
    print_success "Environment variables validated"
    
    if ! check_prerequisites; then
        return 1
    fi
    
    # Setup virtual environment
    if ! setup_virtual_environment; then
        return 1
    fi
    
    # Stop service if it's already running
    if launchctl list | grep -q "$SERVICE_NAME"; then
        print_status "Stopping existing service..."
        launchctl unload "$PLIST_FILE" 2>/dev/null
    fi
    
    # Create log directory
    print_status "Creating log directory: $LOG_DIR"
    if mkdir -p "$LOG_DIR"; then
        print_success "Log directory created"
    else
        print_error "Failed to create log directory"
        return 1
    fi
    
    # Make service script executable
    print_status "Making service script executable..."
    if chmod +x "$SERVICE_SCRIPT"; then
        print_success "Service script is executable"
    else
        print_error "Failed to make service script executable"
        return 1
    fi
    
    # Create LaunchAgents directory if it doesn't exist
    print_status "Creating LaunchAgents directory..."
    if mkdir -p "$HOME/Library/LaunchAgents"; then
        print_success "LaunchAgents directory ready"
    else
        print_error "Failed to create LaunchAgents directory"
        return 1
    fi
    
    # Generate plist with correct virtual environment paths
    print_status "Generating service configuration..."
    cat > "$PLIST_FILE" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <!-- Service identification -->
    <key>Label</key>
    <string>$SERVICE_NAME</string>
    
    <!-- Program to run -->
    <key>Program</key>
    <string>$VENV_PYTHON</string>
    
    <!-- Program arguments -->
    <key>ProgramArguments</key>
    <array>
        <string>$VENV_PYTHON</string>
        <string>$SERVICE_SCRIPT</string>
    </array>
    
    <!-- Working directory -->
    <key>WorkingDirectory</key>
    <string>$HOME</string>
    
    <!-- Environment variables -->
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:$HOME/.local/bin</string>
        <key>HOME</key>
        <string>$HOME</string>
        <key>USER</key>
        <string>$(whoami)</string>
        <key>VIRTUAL_ENV</key>
        <string>$VENV_DIR</string>
        <key>YOUR_PHONE</key>
        <string>${YOUR_PHONE}</string>
        <key>ALLOWED_TOOLS</key>
        <string>${ALLOWED_TOOLS:-*}</string>
        <key>CLAUDE_CODE_ENTRYPOINT</key>
        <string>sdk-ts</string>
        <key>CLAUDECODE</key>
        <string>1</string>
        <key>CLAUDE_EXTENSIONS_DIR</key>
        <string>$HOME/.claude-bridge/Claude Extensions</string>
        <key>CLAUDE_EXTENSIONS_SETTINGS_DIR</key>
        <string>$HOME/.claude-bridge/Claude Extensions Settings</string>
    </dict>
    
    <!-- Auto-start and restart behavior -->
    <key>RunAtLoad</key>
    <true/>
    
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
        <key>Crashed</key>
        <true/>
    </dict>
    
    <!-- Restart after crash with delay -->
    <key>ThrottleInterval</key>
    <integer>10</integer>
    
    <!-- Logging -->
    <key>StandardOutPath</key>
    <string>$LOG_DIR/stdout.log</string>
    
    <key>StandardErrorPath</key>
    <string>$LOG_DIR/stderr.log</string>
    
    <!-- Process limits -->
    <key>ProcessType</key>
    <string>Background</string>
    
    <!-- Only run when user is logged in -->
    <key>LimitLoadToSessionType</key>
    <array>
        <string>Aqua</string>
    </array>
    
    <!-- Resource limits -->
    <key>SoftResourceLimits</key>
    <dict>
        <key>NumberOfFiles</key>
        <integer>1024</integer>
    </dict>
    
    <!-- Don't run if disabled -->
    <key>Disabled</key>
    <false/>
    
</dict>
</plist>
EOF

    if [ $? -eq 0 ]; then
        print_success "Service configuration generated at: $PLIST_FILE"
    else
        print_error "Failed to generate service configuration"
        return 1
    fi
    
    # Verify the plist file is valid
    print_status "Verifying service configuration..."
    if plutil -lint "$PLIST_FILE" > /dev/null 2>&1; then
        print_success "Service configuration is valid"
    else
        print_error "Service configuration is invalid. Check the plist file."
        return 1
    fi
    
    # Copy Claude Extensions and settings for MCP server access
    print_status "Copying Claude Extensions for MCP server access..."
    CLAUDE_EXTENSIONS_DIR="$HOME/Library/Application Support/Claude/Claude Extensions"
    CLAUDE_EXTENSIONS_SETTINGS_DIR="$HOME/Library/Application Support/Claude/Claude Extensions Settings"
    SERVICE_CLAUDE_DIR="$HOME/.claude-bridge"
    
    if [ -d "$CLAUDE_EXTENSIONS_DIR" ]; then
        mkdir -p "$SERVICE_CLAUDE_DIR"
        cp -R "$CLAUDE_EXTENSIONS_DIR" "$SERVICE_CLAUDE_DIR/" 2>/dev/null || true
        cp -R "$CLAUDE_EXTENSIONS_SETTINGS_DIR" "$SERVICE_CLAUDE_DIR/" 2>/dev/null || true
        print_success "Claude Extensions copied for background service"
    else
        print_warning "No Claude Extensions found, MCP servers may not be available"
    fi

    print_success "✅ Service installed successfully!"
    print_status "Next steps:"
    print_status "  1. Run: ./manage-claude-bridge.sh start"
    print_status "  2. Start WhatsApp bridge: docker-compose up -d"
    print_status "  3. Test by sending yourself a WhatsApp message"
}

uninstall_service() {
    print_status "Uninstalling Claude Bridge service..."
    
    # Stop service if running
    stop_service
    
    # Remove plist file
    if [ -f "$PLIST_FILE" ]; then
        rm "$PLIST_FILE"
        print_success "Service uninstalled"
    else
        print_warning "Service was not installed"
    fi
}

start_service() {
    print_status "Starting Claude Bridge service..."
    
    if [ ! -f "$PLIST_FILE" ]; then
        print_error "Service not installed. Run './manage-claude-bridge.sh install' first"
        return 1
    fi
    
    launchctl load "$PLIST_FILE"
    
    if [ $? -eq 0 ]; then
        print_success "Service started"
        print_status "Service will start automatically on login"
    else
        print_error "Failed to start service"
        return 1
    fi
}

stop_service() {
    print_status "Stopping Claude Bridge service..."
    
    launchctl unload "$PLIST_FILE" 2>/dev/null
    
    if [ $? -eq 0 ]; then
        print_success "Service stopped"
    else
        print_warning "Service was not running"
    fi
}

restart_service() {
    print_status "Restarting Claude Bridge service..."
    stop_service
    sleep 2
    start_service
}

status_service() {
    print_status "Checking Claude Bridge service status..."
    
    # Check if virtual environment exists
    if [ -d "$VENV_DIR" ] && [ -f "$VENV_PYTHON" ]; then
        print_success "Virtual environment: OK ($VENV_DIR)"
    else
        print_warning "Virtual environment: MISSING ($VENV_DIR)"
        print_status "Run './manage-claude-bridge.sh install' to fix this"
        return 1
    fi
    
    # Check if service is installed
    if [ -f "$PLIST_FILE" ]; then
        print_success "Service configuration: OK"
    else
        print_warning "Service configuration: MISSING"
        print_status "Run './manage-claude-bridge.sh install' to fix this"
        return 1
    fi
    
    # Check if service is running
    if launchctl list | grep -q "$SERVICE_NAME"; then
        print_success "Service status: RUNNING"
        
        # Show recent log entries
        print_status "Recent log entries:"
        echo "===================="
        tail -10 "$LOG_DIR/claude-bridge.log" 2>/dev/null || print_warning "No log file found yet"
    else
        print_warning "Service status: NOT RUNNING"
        print_status "Run './manage-claude-bridge.sh start' to start the service"
    fi
}

show_logs() {
    print_status "Claude Bridge service logs:"
    echo "============================="
    
    if [ -f "$LOG_DIR/claude-bridge.log" ]; then
        tail -50 "$LOG_DIR/claude-bridge.log"
    else
        print_warning "No log file found"
    fi
    
    echo
    print_status "To follow logs in real-time, run:"
    echo "tail -f '$LOG_DIR/claude-bridge.log'"
}

follow_logs() {
    print_status "Following Claude Bridge service logs (Ctrl+C to stop):"
    echo "======================================================"
    
    if [ -f "$LOG_DIR/claude-bridge.log" ]; then
        tail -f "$LOG_DIR/claude-bridge.log"
    else
        print_warning "No log file found. Start the service first."
    fi
}

show_help() {
    echo "Claude Bridge Service Manager"
    echo "============================="
    echo
    echo "Usage: $0 {install|uninstall|start|stop|restart|status|logs|follow|help}"
    echo
    echo "Commands:"
    echo "  install     Install the background service"
    echo "  uninstall   Remove the background service"
    echo "  start       Start the service"
    echo "  stop        Stop the service"
    echo "  restart     Restart the service"
    echo "  status      Check service status"
    echo "  logs        Show recent logs"
    echo "  follow      Follow logs in real-time"
    echo "  help        Show this help message"
    echo
    echo "After installation, the service will start automatically when you log in."
    echo "Send yourself a WhatsApp message to test the Claude integration!"
}

# Main script logic
case "$1" in
    install)
        install_service
        ;;
    uninstall)
        uninstall_service
        ;;
    start)
        start_service
        ;;
    stop)
        stop_service
        ;;
    restart)
        restart_service
        ;;
    status)
        status_service
        ;;
    logs)
        show_logs
        ;;
    follow)
        follow_logs
        ;;
    help|--help|-h)
        show_help
        ;;
    *)
        print_error "Unknown command: $1"
        show_help
        exit 1
        ;;
esac