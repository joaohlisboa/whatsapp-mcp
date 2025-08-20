package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// ImportProgress represents the progress tracking for historical import
type ImportProgress struct {
	StartDate         string            `json:"start_date"`
	EndDate           string            `json:"end_date"`
	GroupJID          string            `json:"group_jid"`
	LastProcessedDate string            `json:"last_processed_date"`
	ProcessedDates    []string          `json:"processed_dates"`
	FailedDates       map[string]string `json:"failed_dates"` // date -> error message
	TotalMessages     int               `json:"total_messages"`
	TotalEpisodes     int               `json:"total_episodes"`
	StartTime         time.Time         `json:"start_time"`
}

// ImportStats holds statistics for a single day's import
type ImportStats struct {
	Date           string `json:"date"`
	MessagesFound  int    `json:"messages_found"`
	TopicsCreated  int    `json:"topics_created"`
	EpisodesAdded  int    `json:"episodes_added"`
	ProcessingTime string `json:"processing_time"`
}

const (
	progressFile = "store/import-progress.json"
	defaultDelay = 2 * time.Second
)

var (
	groupJID      = flag.String("group-jid", "", "WhatsApp group JID to import (required)")
	startDate     = flag.String("start-date", "", "Start date in YYYY-MM-DD format")
	endDate       = flag.String("end-date", "", "End date in YYYY-MM-DD format")
	daysBack      = flag.Int("days-back", 0, "Number of days back to import from today")
	delaySeconds  = flag.Int("delay", 2, "Delay in seconds between processing each day")
	resume        = flag.Bool("resume", false, "Resume interrupted import from progress file")
	dryRun        = flag.Bool("dry-run", false, "Show what would be imported without actually processing")
	skipGraphiti  = flag.Bool("skip-graphiti", false, "Skip adding episodes to Graphiti (only process messages)")
	timezone      = flag.String("timezone", "America/Sao_Paulo", "Timezone for date processing")
	verbose       = flag.Bool("verbose", false, "Enable verbose logging")
)

func main() {
	flag.Parse()

	// Setup logger with appropriate level
	logLevel := "INFO"
	if *verbose {
		logLevel = "DEBUG"
	}
	logger := waLog.Stdout("HistoricalImport", logLevel, true)

	logger.Infof("Starting WhatsApp Historical Import to Graphiti")

	// Validate required parameters
	if err := validateParameters(); err != nil {
		logger.Errorf("Parameter validation failed: %v", err)
		flag.Usage()
		os.Exit(1)
	}

	// Setup graceful shutdown
	ctx, cancel := setupGracefulShutdown(logger)
	defer cancel()

	// Load or create progress
	progress, err := loadOrCreateProgress()
	if err != nil {
		logger.Errorf("Failed to load progress: %v", err)
		os.Exit(1)
	}

	logger.Infof("Import configuration:")
	logger.Infof("  Group JID: %s", progress.GroupJID)
	logger.Infof("  Date range: %s to %s", progress.StartDate, progress.EndDate)
	logger.Infof("  Timezone: %s", *timezone)
	logger.Infof("  Delay between days: %v", time.Duration(*delaySeconds)*time.Second)
	logger.Infof("  Dry run: %v", *dryRun)
	logger.Infof("  Skip Graphiti: %v", *skipGraphiti)

	if *resume {
		logger.Infof("Resuming from last processed date: %s", progress.LastProcessedDate)
	}

	// Load timezone
	loc, err := time.LoadLocation(*timezone)
	if err != nil {
		logger.Errorf("Failed to load timezone %s: %v", *timezone, err)
		loc = time.UTC
	}

	// Parse date range
	dates, err := generateDateRange(progress.StartDate, progress.EndDate, loc)
	if err != nil {
		logger.Errorf("Failed to generate date range: %v", err)
		os.Exit(1)
	}

	// Filter out already processed dates
	if *resume {
		dates = filterProcessedDates(dates, progress.ProcessedDates)
		logger.Infof("Filtered to %d remaining dates to process", len(dates))
	}

	if len(dates) == 0 {
		logger.Infof("No dates to process. Import complete.")
		return
	}

	if *dryRun {
		logger.Infof("DRY RUN: Would process the following dates:")
		for _, date := range dates {
			logger.Infof("  - %s", date)
		}
		return
	}

	// Get group name for better organization
	groupName := getGroupName(progress.GroupJID, logger)
	logger.Infof("Processing group: %s", groupName)

	// Process each day
	successCount := 0
	for i, dateStr := range dates {
		select {
		case <-ctx.Done():
			logger.Infof("Received shutdown signal, stopping gracefully...")
			break
		default:
			// Process this date
			stats, err := processSingleDay(dateStr, progress.GroupJID, groupName, loc, logger)
			if err != nil {
				logger.Errorf("Failed to process %s: %v", dateStr, err)
				progress.FailedDates[dateStr] = err.Error()
			} else {
				logger.Infof("Successfully processed %s: %d messages, %d topics, %d episodes", 
					dateStr, stats.MessagesFound, stats.TopicsCreated, stats.EpisodesAdded)
				progress.ProcessedDates = append(progress.ProcessedDates, dateStr)
				progress.LastProcessedDate = dateStr
				progress.TotalMessages += stats.MessagesFound
				progress.TotalEpisodes += stats.EpisodesAdded
				successCount++
			}

			// Save progress after each day
			if err := saveProgress(progress); err != nil {
				logger.Warnf("Failed to save progress: %v", err)
			}

			// Add delay between days (except for the last one)
			if i < len(dates)-1 {
				delay := time.Duration(*delaySeconds) * time.Second
				logger.Infof("Waiting %v before processing next day...", delay)
				select {
				case <-time.After(delay):
					// Continue
				case <-ctx.Done():
					logger.Infof("Received shutdown signal during delay, stopping...")
					break
				}
			}
		}
	}

	// Final summary
	logger.Infof("Import completed!")
	logger.Infof("  Successfully processed: %d/%d days", successCount, len(dates))
	logger.Infof("  Total messages imported: %d", progress.TotalMessages)
	logger.Infof("  Total episodes created: %d", progress.TotalEpisodes)
	logger.Infof("  Failed dates: %d", len(progress.FailedDates))
	
	if len(progress.FailedDates) > 0 {
		logger.Infof("Failed dates can be retried by running the command again with --resume")
		for failedDate, failedError := range progress.FailedDates {
			logger.Warnf("  %s: %s", failedDate, failedError)
		}
	}
}

func validateParameters() error {
	if *resume {
		// When resuming, we get parameters from the progress file
		return nil
	}

	if *groupJID == "" {
		return fmt.Errorf("group-jid is required")
	}

	if *daysBack > 0 {
		// Using days-back, calculate dates
		return nil
	}

	if *startDate == "" || *endDate == "" {
		return fmt.Errorf("either --days-back OR both --start-date and --end-date must be provided")
	}

	// Validate date formats
	if _, err := time.Parse("2006-01-02", *startDate); err != nil {
		return fmt.Errorf("invalid start-date format, expected YYYY-MM-DD: %v", err)
	}

	if _, err := time.Parse("2006-01-02", *endDate); err != nil {
		return fmt.Errorf("invalid end-date format, expected YYYY-MM-DD: %v", err)
	}

	return nil
}

func loadOrCreateProgress() (*ImportProgress, error) {
	if *resume {
		// Try to load existing progress
		data, err := os.ReadFile(progressFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read progress file for resume: %v", err)
		}

		var progress ImportProgress
		if err := json.Unmarshal(data, &progress); err != nil {
			return nil, fmt.Errorf("failed to parse progress file: %v", err)
		}

		return &progress, nil
	}

	// Create new progress
	progress := &ImportProgress{
		GroupJID:       *groupJID,
		ProcessedDates: make([]string, 0),
		FailedDates:    make(map[string]string),
		StartTime:      time.Now(),
	}

	// Calculate date range
	if *daysBack > 0 {
		loc, _ := time.LoadLocation(*timezone)
		now := time.Now().In(loc)
		progress.EndDate = now.Format("2006-01-02")
		progress.StartDate = now.AddDate(0, 0, -*daysBack).Format("2006-01-02")
	} else {
		progress.StartDate = *startDate
		progress.EndDate = *endDate
	}

	return progress, nil
}

func saveProgress(progress *ImportProgress) error {
	// Create directory if it doesn't exist
	if err := os.MkdirAll("store", 0755); err != nil {
		return fmt.Errorf("failed to create store directory: %v", err)
	}

	data, err := json.MarshalIndent(progress, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal progress: %v", err)
	}

	return os.WriteFile(progressFile, data, 0644)
}

func generateDateRange(startStr, endStr string, loc *time.Location) ([]string, error) {
	start, err := time.Parse("2006-01-02", startStr)
	if err != nil {
		return nil, fmt.Errorf("invalid start date: %v", err)
	}

	end, err := time.Parse("2006-01-02", endStr)
	if err != nil {
		return nil, fmt.Errorf("invalid end date: %v", err)
	}

	if start.After(end) {
		return nil, fmt.Errorf("start date cannot be after end date")
	}

	var dates []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format("2006-01-02"))
	}

	return dates, nil
}

func filterProcessedDates(allDates, processedDates []string) []string {
	processedSet := make(map[string]bool)
	for _, date := range processedDates {
		processedSet[date] = true
	}

	var remaining []string
	for _, date := range allDates {
		if !processedSet[date] {
			remaining = append(remaining, date)
		}
	}

	return remaining
}

func processSingleDay(dateStr, groupJID, groupName string, loc *time.Location, logger waLog.Logger) (*ImportStats, error) {
	startTime := time.Now()

	// Parse the date and create time range for the day
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid date format: %v", err)
	}

	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	endOfDay := time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, 999999999, loc)

	logger.Infof("Processing %s (%s to %s)", dateStr, 
		startOfDay.Format("2006-01-02 15:04:05"), 
		endOfDay.Format("2006-01-02 15:04:05"))

	// Get messages from the database
	messages, err := getMessagesFromGroup(groupJID, startOfDay, endOfDay, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %v", err)
	}

	stats := &ImportStats{
		Date:          dateStr,
		MessagesFound: len(messages),
	}

	if len(messages) == 0 {
		logger.Infof("No messages found for %s", dateStr)
		stats.ProcessingTime = time.Since(startTime).String()
		return stats, nil
	}

	logger.Infof("Found %d messages for %s", len(messages), dateStr)

	// Skip Graphiti processing if requested
	if *skipGraphiti {
		logger.Infof("Skipping Graphiti processing as requested")
		stats.ProcessingTime = time.Since(startTime).String()
		return stats, nil
	}

	// Segment messages by topic
	topicSegments, err := segmentMessagesByTopic(messages, groupName, dateStr, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to segment messages by topic: %v", err)
	}

	stats.TopicsCreated = len(topicSegments)
	logger.Infof("Segmented into %d topics", stats.TopicsCreated)

	// Add episodes to Graphiti
	err = addEpisodesToGraphiti(topicSegments, groupName, dateStr, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to add episodes to Graphiti: %v", err)
	}

	stats.EpisodesAdded = len(topicSegments)
	stats.ProcessingTime = time.Since(startTime).String()

	return stats, nil
}

func setupGracefulShutdown(logger waLog.Logger) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		logger.Infof("Received shutdown signal, finishing current operation and exiting...")
		cancel()
	}()

	return ctx, cancel
}