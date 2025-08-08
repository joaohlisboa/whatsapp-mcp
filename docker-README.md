# Docker Setup for WhatsApp MCP

This guide explains how to run the WhatsApp MCP Server using Docker Compose.

## Prerequisites

- Docker
- Docker Compose
- Your WhatsApp mobile device (for QR code scanning)

## Quick Start

1. **Start the WhatsApp Bridge:**
   ```bash
   docker-compose up -d
   ```

2. **Authenticate with WhatsApp:**
   The first time you run the bridge, you'll need to scan a QR code. View the logs to see the QR code:
   ```bash
   docker-compose logs -f whatsapp-bridge
   ```
   
   Scan the QR code with your WhatsApp mobile app to authenticate. If the camera is not reading the QR code in the terminal, use docker logs.

3. **Check service status:**
   ```bash
   docker-compose ps
   ```

## Services

### WhatsApp Bridge (Docker)
- **Container**: `whatsapp-bridge`
- **Port**: 8080
- **Purpose**: Connects to WhatsApp Web API and stores messages in SQLite

### WhatsApp MCP Server (Local)
- **No container** (runs locally with `uv run main.py`)
- **Purpose**: Provides MCP tools for Claude to interact with WhatsApp data

## Data Persistence

The SQLite databases (`whatsapp.db` and `messages.db`) are stored in the `./whatsapp-bridge/store/` directory and mounted as a volume. This ensures your authentication and message history persist across container restarts.

## Configuration for Claude Desktop

### Default: Hybrid Approach (Recommended)
Run only the WhatsApp bridge in Docker and the MCP server locally:

1. **Start only the WhatsApp bridge:**
   ```bash
   docker-compose up -d
   ```

2. **Run the MCP server locally:**
   ```bash
   cd whatsapp-mcp-server
   # Set environment variable to use Docker bridge
   export WHATSAPP_BRIDGE_HOST=localhost
   export WHATSAPP_BRIDGE_PORT=8080  
   export MESSAGES_DB_PATH=../whatsapp-bridge/store/messages.db
   uv run main.py
   ```

3. **Update your `claude_desktop_config.json`:**
   ```json
   {
     "mcpServers": {
       "whatsapp": {
         "command": "uv",
         "args": [
           "--directory",
           "/path/to/whatsapp-mcp/whatsapp-mcp-server",
           "run",
           "main.py"
         ],
         "env": {
           "WHATSAPP_BRIDGE_HOST": "localhost",
           "WHATSAPP_BRIDGE_PORT": "8080",
           "MESSAGES_DB_PATH": "/path/to/whatsapp-mcp/whatsapp-bridge/store/messages.db"
         }
       }
     }
   }
   ```

### Alternative: Full Local Development
For the standard setup (as described in the main README), run both services locally without Docker.

## Useful Commands

- **View logs**: `docker-compose logs -f [service-name]`
- **Restart services**: `docker-compose restart`
- **Stop services**: `docker-compose down`
- **Rebuild images**: `docker-compose build --no-cache`
- **Remove everything**: `docker-compose down -v` (⚠️ This will delete your authentication!)

## Troubleshooting

- **QR Code not visible**: Make sure your terminal supports displaying QR codes, or check the logs for the QR code text
- **Authentication expires**: Delete the database files in `./whatsapp-bridge/store/` and restart
- **Permission issues**: Make sure the `./whatsapp-bridge/store/` directory is writable. If you encounter permission errors, run: `sudo chown -R 1000:1000 ./whatsapp-bridge/store/`
- **Services not connecting**: Check that both services are running with `docker-compose ps`

## Security Notes

- The WhatsApp databases contain your personal messages
- Keep the `./whatsapp-bridge/store/` directory secure
- Containers run as non-root users for improved security
- Consider additional hardening for production environments