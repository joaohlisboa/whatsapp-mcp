# Historical Import - Local Usage Only

This directory contains tools for importing historical WhatsApp conversations into Graphiti's memory. These tools are designed to run **locally** on the host machine, not inside the Docker container.

## Architecture

- **Docker Container**: Runs the WhatsApp bridge and daily summary routines
- **Local Scripts**: Handle on-demand historical imports by connecting to the same database files

## Files

- `historical-import.go` - Go binary for historical import (build locally)
- `import-history.sh` - Bash wrapper script for easy operation
- `common.go` - Shared functions used by both daily summary and historical import

## Setup

1. Make sure the Docker container is running (so databases are accessible)
2. Build the historical import binary locally:
   ```bash
   go build -o historical-import historical-import.go common.go
   ```
3. Make the shell script executable:
   ```bash
   chmod +x import-history.sh
   ```

## Usage Examples

```bash
# Import last 30 days
./import-history.sh import-days --group-jid "YOUR_GROUP_ID@g.us" --days 30

# Import specific month
./import-history.sh import-month --group-jid "YOUR_GROUP_ID@g.us" --month "2024-01"

# Import date range
./import-history.sh import-range --group-jid "YOUR_GROUP_ID@g.us" --start "2024-01-01" --end "2024-01-31"

# Preview without processing
./import-history.sh dry-run --group-jid "YOUR_GROUP_ID@g.us" --days 7

# Resume interrupted import
./import-history.sh resume

# Check status
./import-history.sh status
```

## Important Notes

- **Local Only**: Never run historical imports inside the Docker container
- **Database Access**: Requires the Docker container to be running for database access
- **Progress Tracking**: Imports can be safely interrupted and resumed
- **Rate Limiting**: Built-in delays between API calls to avoid overwhelming Claude
- **Error Recovery**: Failed days can be retried individually

## Configuration

The script uses the same environment variables as the Docker container:
- `CLAUDE_SERVER_URL` - Claude Code HTTP server URL
- `CLAUDE_ALLOWED_TOOLS` - Tools available to Claude (automatically set to include Graphiti)

Make sure these are configured in your local environment when running historical imports.