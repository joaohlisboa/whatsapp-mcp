package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

func main() {
	logger := waLog.Stdout("DailySummary", "INFO", true)
	logger.Infof("Starting daily summary generation...")

	// Check if daily summary is enabled
	enabled := os.Getenv("DAILY_SUMMARY_ENABLED")
	if enabled != "true" {
		logger.Infof("Daily summary is disabled. Set DAILY_SUMMARY_ENABLED=true to enable.")
		return
	}

	// Get configuration from environment
	groupJID := os.Getenv("DAILY_SUMMARY_GROUP_JID")
	sendTo := os.Getenv("DAILY_SUMMARY_SEND_TO")
	timezone := os.Getenv("DAILY_SUMMARY_TIMEZONE")

	// Load timezone
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		logger.Errorf("Failed to load timezone %s: %v", timezone, err)
		loc = time.UTC
	}

	// Get current date in the configured timezone
	now := time.Now().In(loc)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, loc)

	logger.Infof("Generating summary for group %s from %s to %s", groupJID, startOfDay.Format("2006-01-02 15:04:05"), endOfDay.Format("2006-01-02 15:04:05"))

	// Get messages from the database
	messages, err := getMessagesFromGroup(groupJID, startOfDay, endOfDay, logger)
	if err != nil {
		logger.Errorf("Failed to get messages: %v", err)
		return
	}

	if len(messages) == 0 {
		logger.Infof("No messages found for today in group %s", groupJID)
		return
	}

	logger.Infof("Found %d messages for today", len(messages))

	// Load prompt template
	prompt, err := loadPromptTemplate(messages, startOfDay.Format("2006-01-02"))
	if err != nil {
		logger.Errorf("Failed to load prompt template: %v", err)
		return
	}

	// Call Claude API
	response, err := callClaudeServer(prompt)
	if err != nil {
		logger.Errorf("Failed to call Claude server: %v", err)
		return
	}

	logger.Infof("Generated summary (%d characters)", len(response))

	// Send the summary
	err = sendSummary(response, sendTo, groupJID, logger)
	if err != nil {
		logger.Errorf("Failed to send summary: %v", err)
		return
	}

	// Add episodes to Graphiti knowledge graph
	logger.Infof("Starting Graphiti episode addition...")

	// Get group name for better organization
	groupName := getGroupName(groupJID, logger)

	// Segment messages by topic
	topicSegments, err := segmentMessagesByTopic(messages, groupName, startOfDay.Format("2006-01-02"), logger)
	if err != nil {
		logger.Warnf("Failed to segment messages by topic: %v", err)
	} else {
		// Add episodes to Graphiti
		err = addEpisodesToGraphiti(topicSegments, groupName, startOfDay.Format("2006-01-02"), logger)
		if err != nil {
			logger.Warnf("Failed to add episodes to Graphiti: %v", err)
		} else {
			logger.Infof("Successfully added conversation episodes to Graphiti knowledge graph")
		}
	}

	logger.Infof("Daily summary completed successfully")
}

// loadPromptTemplate loads the prompt template and replaces placeholders
func loadPromptTemplate(messages []DailySummaryMessage, date string) (string, error) {
	// Try to load custom prompt template
	promptPath := "prompts/daily-summary.md"
	promptBytes, err := os.ReadFile(promptPath)

	var promptTemplate string
	if err != nil {
		// Use default prompt if file doesn't exist
		promptTemplate = `You are an executive assistant analyzing conversations in the group for the day. 
Please provide:

1. **Executive Summary**: Main discussions and decisions
2. **Pending Actions**: Tasks identified and responsible  
3. **Metrics**: Companies mentioned, valuations discussed
4. **Follow-ups Needed**: Suggested next steps

Be direct and concise. Use data and numbers whenever mentioned.

Messages of the day ({{DATE}}):
{{MESSAGES}}`
	} else {
		promptTemplate = string(promptBytes)
	}

	// Format messages as text
	var messageLines []string
	for _, msg := range messages {
		direction := "←"
		if msg.IsFromMe {
			direction = "→"
		}
		messageLines = append(messageLines, fmt.Sprintf("[%s] %s %s: %s",
			msg.Timestamp, direction, msg.Sender, msg.Content))
	}
	messagesText := strings.Join(messageLines, "\n")

	// Replace placeholders
	prompt := strings.ReplaceAll(promptTemplate, "{{MESSAGES}}", messagesText)
	prompt = strings.ReplaceAll(prompt, "{{DATE}}", date)

	return prompt, nil
}

// sendSummary sends the generated summary to the specified recipient
func sendSummary(summary, sendTo, groupJID string, logger waLog.Logger) error {
	// If sendTo is "self", send to self-chat
	if sendTo == "self" {
		return sendToSelfChat(summary, logger)
	}

	// Otherwise, send to specified JID
	return sendToRecipient(summary, sendTo, logger)
}

// sendToSelfChat sends the summary to the user's self-chat
func sendToSelfChat(summary string, logger waLog.Logger) error {
	// We need to get the WhatsApp client to send to self
	// For now, let's use the REST API approach
	return sendToRecipient(summary, "self", logger)
}