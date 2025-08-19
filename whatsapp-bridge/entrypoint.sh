#!/bin/sh

# Daily Summary Entrypoint Script
# Starts both cron daemon and WhatsApp bridge

echo "Starting WhatsApp Bridge with cron jobs..."

# Set timezone if specified
if [ -n "$DAILY_SUMMARY_TIMEZONE" ]; then
    export TZ="$DAILY_SUMMARY_TIMEZONE"
    echo "Timezone set to: $TZ"
fi

# Check if daily summary is enabled
if [ "$DAILY_SUMMARY_ENABLED" = "true" ]; then
    echo "Daily summary is enabled"
    
    # Get the configured time (default: 22:00)
    DAILY_TIME="${DAILY_SUMMARY_TIME:-22:00}"
    
    # Parse time (format: HH:MM)
    HOUR=$(echo "$DAILY_TIME" | cut -d':' -f1)
    MINUTE=$(echo "$DAILY_TIME" | cut -d':' -f2)
    
    # Validate hour and minute
    if [ "$HOUR" -ge 0 ] && [ "$HOUR" -le 23 ] && [ "$MINUTE" -ge 0 ] && [ "$MINUTE" -le 59 ]; then
        echo "Daily summary scheduled for: $HOUR:$MINUTE"
        
        # Create environment file for cron job
        cat > /app/daily-summary.env << EOF
export DAILY_SUMMARY_ENABLED="$DAILY_SUMMARY_ENABLED"
export DAILY_SUMMARY_TIME="$DAILY_SUMMARY_TIME"
export DAILY_SUMMARY_GROUP_JID="$DAILY_SUMMARY_GROUP_JID"
export DAILY_SUMMARY_SEND_TO="$DAILY_SUMMARY_SEND_TO"
export DAILY_SUMMARY_TIMEZONE="$DAILY_SUMMARY_TIMEZONE"
export CLAUDE_SERVER_URL="$CLAUDE_SERVER_URL"
export CLAUDE_ALLOWED_TOOLS="$CLAUDE_ALLOWED_TOOLS"
export TZ="$TZ"
EOF
        
        # Create cron job that sources environment and runs as whatsapp user
        echo "$MINUTE $HOUR * * * cd /app && echo \"[$(date)] Starting daily summary...\" >> /app/store/daily-summary.log && . ./daily-summary.env && su whatsapp -c './daily-summary' >> /app/store/daily-summary.log 2>&1 && echo \"[$(date)] Daily summary completed\" >> /app/store/daily-summary.log" > /tmp/crontab
        
        # Install the crontab
        crontab /tmp/crontab
        
        # Show installed crontab for debugging
        echo "Installed cron job:"
        crontab -l
        
        # Ensure log files exist and have proper permissions
        touch /app/store/daily-summary.log /app/store/cron.log
        chown whatsapp:whatsapp /app/store/daily-summary.log
        
        # Start cron daemon in background
        crond -b -l 2 -L /app/store/cron.log
        
        echo "Cron daemon started with environment variables"
    else
        echo "Warning: Invalid time format for DAILY_SUMMARY_TIME ($DAILY_TIME). Expected HH:MM format."
        echo "Daily summary will be disabled for this session."
    fi
else
    echo "Daily summary is disabled"
fi

# Ensure all log files exist with proper permissions
touch /app/store/daily-summary.log /app/store/cron.log
chown whatsapp:whatsapp /app/store/daily-summary.log /app/store/cron.log

# Show current configuration
echo "=== Daily Summary Configuration ==="
echo "Enabled: ${DAILY_SUMMARY_ENABLED:-false}"
echo "Time: ${DAILY_SUMMARY_TIME:-22:00}"
echo "Group JID: ${DAILY_SUMMARY_GROUP_JID}"
echo "Send To: ${DAILY_SUMMARY_SEND_TO:-self}"
echo "Timezone: ${DAILY_SUMMARY_TIMEZONE:-America/Sao_Paulo}"
echo "Claude Server: ${CLAUDE_SERVER_URL:-http://host.docker.internal:8888/claude}"
echo "==================================="

# Check if prompt template exists
if [ -f "/app/prompts/daily-summary.md" ]; then
    echo "Custom prompt template found at /app/prompts/daily-summary.md"
else
    echo "Using default prompt template (custom template not found)"
fi

# Start the WhatsApp bridge as whatsapp user (this will run in foreground)
echo "Starting WhatsApp bridge..."
exec su whatsapp -c './whatsapp-bridge'