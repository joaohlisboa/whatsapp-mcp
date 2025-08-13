# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

WhatsApp MCP Server - A Model Context Protocol (MCP) server that enables AI assistants like Claude to interact with WhatsApp through a local bridge. The system stores messages locally in SQLite and provides tools for searching, reading, and sending WhatsApp messages including media files.

## Architecture

### Two-Component System
1. **Go WhatsApp Bridge** (`whatsapp-bridge/`) - Connects to WhatsApp Web API, handles authentication, maintains message database
2. **Python MCP Server** (`whatsapp-mcp-server/`) - Implements MCP protocol for Claude integration

### Data Flow
```
Claude → Python MCP Server → Go Bridge → WhatsApp API
                ↓
           SQLite DB (local storage)
```

## Common Development Commands

### Running the Application

1. **Start WhatsApp Bridge** (required first):
```bash
cd whatsapp-bridge
go run main.go
# First run: scan QR code with WhatsApp mobile app
```

2. **Run MCP Server** (for development/testing):
```bash
cd whatsapp-mcp-server
uv run main.py
```

### Docker Operations
```bash
# Run with Docker Compose
docker compose up -d

# View logs
docker compose logs -f whatsapp-bridge

# Rebuild after changes
docker compose build
docker compose up -d
```

### Database Management
- Messages stored in: `whatsapp-bridge/store/messages.db`
- WhatsApp session in: `whatsapp-bridge/store/whatsapp.db`
- To reset/resync: Delete both `.db` files and restart bridge

## Key Implementation Details

### Go Bridge (`whatsapp-bridge/main.go`)
- Uses `whatsmeow` library for WhatsApp Web API
- HTTP API server on port 8080
- Endpoints:
  - `/api/send_message` - Send text messages
  - `/api/send_file` - Send media files
  - `/api/send_audio_message` - Send voice messages
  - `/api/download_media` - Download media from messages
- Database tables: `chats`, `messages`
- Media handling: Stores metadata with download info (url, media_key, file_sha256)

### Python MCP Server (`whatsapp-mcp-server/`)
- **main.py**: FastMCP server with tool definitions
- **whatsapp.py**: Database queries and API calls to Go bridge
- **audio.py**: Audio format conversion (MP3/WAV → Opus OGG)
- Uses environment variables for Docker/local compatibility:
  - `MESSAGES_DB_PATH` - Path to SQLite database
  - `WHATSAPP_BRIDGE_HOST` - Bridge hostname (localhost or docker service name)
  - `WHATSAPP_BRIDGE_PORT` - Bridge port (default: 8080)

### MCP Tools Available
- Contact operations: `search_contacts`, `get_direct_chat_by_contact`
- Message operations: `list_messages`, `get_message_context`, `send_message`
- Chat operations: `list_chats`, `get_chat`, `get_contact_chats`
- Media operations: `send_file`, `send_audio_message`, `download_media`
- Utility: `get_last_interaction`

### Media Handling
- **Sending**: Files uploaded via multipart form to Go bridge
- **Voice Messages**: Require Opus OGG format (auto-conversion with FFmpeg)
- **Downloading**: Media metadata stored, actual download on-demand via `download_media` tool
- Supported types: Images, videos, documents, audio

### Claude Integration (Optional)
- Configure via `.env` file:
  - `CLAUDE_SERVER_URL` - Claude Code HTTP server endpoint
  - `CLAUDE_ALLOWED_TOOLS` - Comma-separated list of allowed MCP tools
- Enables self-chat functionality where Claude can respond to WhatsApp messages

## Testing & Debugging

### Check Go Bridge Status
```bash
curl http://localhost:8080/api/status
```

### View SQLite Database
```bash
sqlite3 whatsapp-bridge/store/messages.db
.tables  # Show tables
.schema messages  # Show message schema
SELECT * FROM messages ORDER BY timestamp DESC LIMIT 10;  # Recent messages
```

### Common Issues & Solutions

1. **Windows CGO Issues**: 
   - Enable CGO: `go env -w CGO_ENABLED=1`
   - Install C compiler via MSYS2

2. **Authentication Problems**:
   - Delete both `.db` files in `whatsapp-bridge/store/`
   - Restart bridge and re-scan QR code

3. **Media Not Sending**:
   - For voice messages: Install FFmpeg or send as regular file
   - Check file permissions and paths

4. **Database Sync Issues**:
   - Messages can take minutes to load after authentication
   - Check Go bridge logs for sync progress

## Dependencies

### Go Bridge
- `go.mau.fi/whatsmeow` - WhatsApp Web API client
- `github.com/mattn/go-sqlite3` - SQLite driver
- `github.com/mdp/qrterminal` - QR code display

### Python MCP Server  
- `mcp[cli]` - Model Context Protocol implementation
- `httpx` - HTTP client for API calls
- `requests` - HTTP library for file uploads