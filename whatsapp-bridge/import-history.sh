#!/bin/bash

# WhatsApp Historical Import Batch Script
# This script provides easy commands for importing historical WhatsApp conversations to Graphiti
# 
# NOTE: This script is designed to run LOCALLY, not inside the Docker container.
# It connects to the same database files that the containerized bridge uses,
# but runs the historical import process on the host machine for on-demand imports.

set -e

# Default configuration
DEFAULT_GROUP_JID=""
DEFAULT_TIMEZONE="America/Sao_Paulo"
DEFAULT_DELAY=2
HISTORICAL_IMPORT_BIN="./historical-import"
PROGRESS_FILE="store/import-progress.json"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Print colored output
print_info() {
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

# Usage function
show_usage() {
    cat << EOF
WhatsApp Historical Import Script (LOCAL USE ONLY)

This script runs locally and connects to the same database files used by the Docker container.
Make sure the Docker container is running so the databases are accessible.

USAGE:
    $0 [COMMAND] [OPTIONS]

COMMANDS:
    import-days     Import specific number of days back
    import-range    Import specific date range
    import-month    Import specific month
    resume          Resume interrupted import
    status          Show import progress
    clean           Clean progress files
    dry-run         Preview what would be imported
    help            Show this help

EXAMPLES:
    # Import last 30 days
    $0 import-days --group-jid "YOUR_GROUP_ID@g.us" --days 30

    # Import specific month
    $0 import-month --group-jid "YOUR_GROUP_ID@g.us" --month "2024-01"

    # Import date range
    $0 import-range --group-jid "YOUR_GROUP_ID@g.us" --start "2024-01-01" --end "2024-01-31"

    # Resume interrupted import
    $0 resume

    # Show current progress
    $0 status

    # Preview import without processing
    $0 dry-run --group-jid "YOUR_GROUP_ID@g.us" --days 7

OPTIONS:
    --group-jid     WhatsApp group JID (required for new imports)
    --days          Number of days back to import
    --start         Start date (YYYY-MM-DD)
    --end           End date (YYYY-MM-DD)
    --month         Month to import (YYYY-MM)
    --delay         Delay in seconds between days (default: 2)
    --timezone      Timezone (default: America/Sao_Paulo)
    --verbose       Enable verbose logging
    --skip-graphiti Skip Graphiti integration (messages only)

EOF
}

# Parse command line arguments
parse_args() {
    GROUP_JID="$DEFAULT_GROUP_JID"
    TIMEZONE="$DEFAULT_TIMEZONE"
    DELAY="$DEFAULT_DELAY"
    VERBOSE=""
    SKIP_GRAPHITI=""
    
    while [[ $# -gt 0 ]]; do
        case $1 in
            --group-jid)
                GROUP_JID="$2"
                shift 2
                ;;
            --days)
                DAYS="$2"
                shift 2
                ;;
            --start)
                START_DATE="$2"
                shift 2
                ;;
            --end)
                END_DATE="$2"
                shift 2
                ;;
            --month)
                MONTH="$2"
                shift 2
                ;;
            --delay)
                DELAY="$2"
                shift 2
                ;;
            --timezone)
                TIMEZONE="$2"
                shift 2
                ;;
            --verbose)
                VERBOSE="--verbose"
                shift
                ;;
            --skip-graphiti)
                SKIP_GRAPHITI="--skip-graphiti"
                shift
                ;;
            *)
                print_error "Unknown option: $1"
                show_usage
                exit 1
                ;;
        esac
    done
}

# Check if historical-import binary exists
check_binary() {
    if [[ ! -x "$HISTORICAL_IMPORT_BIN" ]]; then
        print_error "Historical import binary not found or not executable: $HISTORICAL_IMPORT_BIN"
        print_info "Please build it first with: go build -o historical-import historical-import.go daily-summary-utils.go claude.go"
        exit 1
    fi
}

# Validate required parameters
validate_group_jid() {
    if [[ -z "$GROUP_JID" ]]; then
        print_error "Group JID is required for new imports"
        print_info "Use --group-jid \"your_group_jid@g.us\""
        exit 1
    fi
}

# Import specific number of days back
import_days() {
    parse_args "$@"
    check_binary
    validate_group_jid
    
    if [[ -z "$DAYS" ]]; then
        print_error "Number of days is required"
        print_info "Use --days NUMBER"
        exit 1
    fi
    
    print_info "Importing last $DAYS days from group: $GROUP_JID"
    
    $HISTORICAL_IMPORT_BIN \
        --group-jid "$GROUP_JID" \
        --days-back "$DAYS" \
        --delay "$DELAY" \
        --timezone "$TIMEZONE" \
        $VERBOSE \
        $SKIP_GRAPHITI
}

# Import specific date range
import_range() {
    parse_args "$@"
    check_binary
    validate_group_jid
    
    if [[ -z "$START_DATE" || -z "$END_DATE" ]]; then
        print_error "Start and end dates are required"
        print_info "Use --start YYYY-MM-DD --end YYYY-MM-DD"
        exit 1
    fi
    
    print_info "Importing date range $START_DATE to $END_DATE from group: $GROUP_JID"
    
    $HISTORICAL_IMPORT_BIN \
        --group-jid "$GROUP_JID" \
        --start-date "$START_DATE" \
        --end-date "$END_DATE" \
        --delay "$DELAY" \
        --timezone "$TIMEZONE" \
        $VERBOSE \
        $SKIP_GRAPHITI
}

# Import specific month
import_month() {
    parse_args "$@"
    check_binary
    validate_group_jid
    
    if [[ -z "$MONTH" ]]; then
        print_error "Month is required"
        print_info "Use --month YYYY-MM"
        exit 1
    fi
    
    # Validate month format
    if [[ ! "$MONTH" =~ ^[0-9]{4}-[0-9]{2}$ ]]; then
        print_error "Invalid month format. Use YYYY-MM (e.g., 2024-01)"
        exit 1
    fi
    
    # Calculate first and last day of month
    START_DATE="${MONTH}-01"
    # Get last day of month using date command
    END_DATE=$(date -d "${START_DATE} +1 month -1 day" +%Y-%m-%d 2>/dev/null || \
              date -v1d -v+1m -v-1d -j -f "%Y-%m-%d" "$START_DATE" +%Y-%m-%d 2>/dev/null || \
              echo "${MONTH}-31")
    
    print_info "Importing month $MONTH ($START_DATE to $END_DATE) from group: $GROUP_JID"
    
    $HISTORICAL_IMPORT_BIN \
        --group-jid "$GROUP_JID" \
        --start-date "$START_DATE" \
        --end-date "$END_DATE" \
        --delay "$DELAY" \
        --timezone "$TIMEZONE" \
        $VERBOSE \
        $SKIP_GRAPHITI
}

# Resume interrupted import
resume_import() {
    check_binary
    
    if [[ ! -f "$PROGRESS_FILE" ]]; then
        print_error "No progress file found: $PROGRESS_FILE"
        print_info "Nothing to resume. Start a new import instead."
        exit 1
    fi
    
    print_info "Resuming interrupted import..."
    
    $HISTORICAL_IMPORT_BIN --resume $VERBOSE
}

# Show import status
show_status() {
    if [[ ! -f "$PROGRESS_FILE" ]]; then
        print_info "No import progress file found"
        return
    fi
    
    print_info "Import Progress:"
    echo "----------------------------------------"
    
    # Extract key information from progress file
    if command -v jq &> /dev/null; then
        # Use jq if available for nice formatting
        GROUP_JID=$(jq -r '.group_jid' "$PROGRESS_FILE")
        START_DATE=$(jq -r '.start_date' "$PROGRESS_FILE")
        END_DATE=$(jq -r '.end_date' "$PROGRESS_FILE")
        LAST_PROCESSED=$(jq -r '.last_processed_date' "$PROGRESS_FILE")
        PROCESSED_COUNT=$(jq -r '.processed_dates | length' "$PROGRESS_FILE")
        FAILED_COUNT=$(jq -r '.failed_dates | length' "$PROGRESS_FILE")
        TOTAL_MESSAGES=$(jq -r '.total_messages' "$PROGRESS_FILE")
        TOTAL_EPISODES=$(jq -r '.total_episodes' "$PROGRESS_FILE")
        
        echo "Group JID: $GROUP_JID"
        echo "Date Range: $START_DATE to $END_DATE"
        echo "Last Processed: $LAST_PROCESSED"
        echo "Days Processed: $PROCESSED_COUNT"
        echo "Days Failed: $FAILED_COUNT"
        echo "Total Messages: $TOTAL_MESSAGES"
        echo "Total Episodes: $TOTAL_EPISODES"
        
        if [[ "$FAILED_COUNT" != "0" ]]; then
            print_warning "Failed dates:"
            jq -r '.failed_dates | to_entries[] | "  \(.key): \(.value)"' "$PROGRESS_FILE"
        fi
    else
        # Fallback to basic display
        cat "$PROGRESS_FILE"
    fi
}

# Dry run
dry_run() {
    parse_args "$@"
    check_binary
    validate_group_jid
    
    if [[ -n "$DAYS" ]]; then
        print_info "DRY RUN: Would import last $DAYS days from group: $GROUP_JID"
        $HISTORICAL_IMPORT_BIN \
            --group-jid "$GROUP_JID" \
            --days-back "$DAYS" \
            --dry-run
    elif [[ -n "$START_DATE" && -n "$END_DATE" ]]; then
        print_info "DRY RUN: Would import range $START_DATE to $END_DATE from group: $GROUP_JID"
        $HISTORICAL_IMPORT_BIN \
            --group-jid "$GROUP_JID" \
            --start-date "$START_DATE" \
            --end-date "$END_DATE" \
            --dry-run
    else
        print_error "Either --days or --start/--end dates required for dry run"
        exit 1
    fi
}

# Clean progress files
clean_progress() {
    if [[ -f "$PROGRESS_FILE" ]]; then
        print_warning "This will delete the progress file: $PROGRESS_FILE"
        read -p "Are you sure? (y/N) " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            rm -f "$PROGRESS_FILE"
            print_success "Progress file deleted"
        else
            print_info "Operation cancelled"
        fi
    else
        print_info "No progress file to clean"
    fi
}

# Main command dispatcher
case ${1:-help} in
    import-days)
        shift
        import_days "$@"
        ;;
    import-range)
        shift
        import_range "$@"
        ;;
    import-month)
        shift
        import_month "$@"
        ;;
    resume)
        resume_import
        ;;
    status)
        show_status
        ;;
    dry-run)
        shift
        dry_run "$@"
        ;;
    clean)
        clean_progress
        ;;
    help|--help|-h)
        show_usage
        ;;
    *)
        print_error "Unknown command: $1"
        show_usage
        exit 1
        ;;
esac