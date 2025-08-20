package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// DailySummaryMessage represents a message for the daily summary
type DailySummaryMessage struct {
	Timestamp string `json:"timestamp"`
	Sender    string `json:"sender"`
	Content   string `json:"content"`
	IsFromMe  bool   `json:"is_from_me"`
}

// ClaudeRequest represents the request to Claude Code HTTP server
type ClaudeRequest struct {
	Prompt string   `json:"prompt"`
	Args   []string `json:"args"`
}

// ClaudeResponse represents the response from Claude Code HTTP server
type ClaudeResponse struct {
	Type          string  `json:"type"`
	Subtype       string  `json:"subtype"`
	IsError       bool    `json:"is_error"`
	DurationMs    int     `json:"duration_ms"`
	DurationApiMs int     `json:"duration_api_ms"`
	NumTurns      int     `json:"num_turns"`
	Result        string  `json:"result"`
	SessionId     string  `json:"session_id"`
	TotalCostUsd  float64 `json:"total_cost_usd"`
	Usage         struct {
		InputTokens         int `json:"input_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
		OutputTokens        int `json:"output_tokens"`
	} `json:"usage"`
}

// TopicSegment represents a topic with its associated messages
type TopicSegment struct {
	Messages []int  `json:"messages"`
	Summary  string `json:"summary"`
}

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

// getMessagesFromGroup retrieves all messages from a specific group for the given day
func getMessagesFromGroup(groupJID string, startOfDay, endOfDay time.Time, logger waLog.Logger) ([]DailySummaryMessage, error) {
	// Open SQLite database for messages
	db, err := sql.Open("sqlite3", "file:store/messages.db?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("failed to open message database: %v", err)
	}
	defer db.Close()

	// Query messages for the specific group and day
	rows, err := db.Query(`
		SELECT id, sender, content, timestamp, is_from_me, media_type, filename
		FROM messages 
		WHERE chat_jid = ? 
		AND timestamp >= ? 
		AND timestamp <= ?
		AND (content != '' OR media_type != '')
		ORDER BY timestamp ASC
	`, groupJID, startOfDay, endOfDay)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %v", err)
	}
	defer rows.Close()

	var messages []DailySummaryMessage
	for rows.Next() {
		var id, sender, content, mediaType, filename string
		var timestamp time.Time
		var isFromMe bool

		err := rows.Scan(&id, &sender, &content, &timestamp, &isFromMe, &mediaType, &filename)
		if err != nil {
			logger.Warnf("Failed to scan message row: %v", err)
			continue
		}

		// Format content - if it's media, indicate the media type
		messageContent := content
		if mediaType != "" {
			if content != "" {
				messageContent = fmt.Sprintf("[%s: %s] %s", mediaType, filename, content)
			} else {
				messageContent = fmt.Sprintf("[%s: %s]", mediaType, filename)
			}
		}

		// Skip empty messages
		if messageContent == "" {
			continue
		}

		// Get sender name from contacts if possible
		senderName := getSenderName(sender, logger)

		message := DailySummaryMessage{
			Timestamp: timestamp.Format("15:04:05"),
			Sender:    senderName,
			Content:   messageContent,
			IsFromMe:  isFromMe,
		}

		messages = append(messages, message)
	}

	return messages, nil
}

// getSenderName attempts to get a friendly name for a sender
func getSenderName(senderJID string, logger waLog.Logger) string {
	// Try to get from chats table first
	db, err := sql.Open("sqlite3", "file:store/messages.db?_foreign_keys=on")
	if err != nil {
		return senderJID
	}
	defer db.Close()

	var name string
	err = db.QueryRow("SELECT name FROM chats WHERE jid = ?", senderJID+"@s.whatsapp.net").Scan(&name)
	if err == nil && name != "" {
		return name
	}

	// Extract phone number if it's a JID
	if strings.Contains(senderJID, "@") {
		parts := strings.Split(senderJID, "@")
		return parts[0]
	}

	return senderJID
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

// callClaudeServer sends a message to the Claude Code HTTP server and returns the response
func callClaudeServer(prompt string) (string, error) {
	// Get configuration from environment
	claudeServer := os.Getenv("CLAUDE_SERVER_URL")
	if claudeServer == "" {
		claudeServer = "http://host.docker.internal:8888/claude"
	}

	allowedTools := os.Getenv("CLAUDE_ALLOWED_TOOLS")
	if allowedTools == "" {
		allowedTools = "mcp__whatsapp"
	}

	// Prepare the request
	req := ClaudeRequest{
		Prompt: prompt,
		Args:   []string{"--allowedTools", allowedTools},
	}

	// Marshal the request to JSON
	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("error marshaling request: %v", err)
	}

	// Create the HTTP request
	httpReq, err := http.NewRequest("POST", claudeServer, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("error creating request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Create a client with timeout
	client := &http.Client{
		Timeout: 300 * time.Second,
	}

	// Send the request
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %v", err)
	}

	// Parse the response
	var claudeResp ClaudeResponse
	err = json.Unmarshal(body, &claudeResp)
	if err != nil {
		return "", fmt.Errorf("error parsing response: %v", err)
	}

	// Log the response for debugging (but truncate if very long)
	responseText := claudeResp.Result
	if len(responseText) > 500 {
		responseText = responseText[:500] + "... [truncated]"
	}
	fmt.Printf("Claude MCP response: %s\n", responseText)

	// Check for errors in the response
	if claudeResp.IsError {
		return "", fmt.Errorf("Claude returned an error: %s", claudeResp.Result)
	}

	return claudeResp.Result, nil
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

// sendToRecipient sends the summary to a specific recipient using the WhatsApp client
func sendToRecipient(summary, recipient string, logger waLog.Logger) error {
	// Try to initialize WhatsApp client for sending
	dbLog := waLog.Stdout("Database", "INFO", true)
	container, err := sqlstore.New(context.Background(), "sqlite3", "file:store/whatsapp.db?_foreign_keys=on", dbLog)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %v", err)
	}

	// Get device store
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get device: %v", err)
	}

	// Create client instance
	client := whatsmeow.NewClient(deviceStore, logger)
	if client == nil {
		return fmt.Errorf("failed to create WhatsApp client")
	}

	// Connect to WhatsApp (should reuse existing session)
	err = client.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect: %v", err)
	}
	defer client.Disconnect()

	// Determine recipient JID
	var recipientJID types.JID
	if recipient == "self" && client.Store.ID != nil {
		// Send to self
		recipientJID = types.JID{
			User:   client.Store.ID.User,
			Server: "s.whatsapp.net",
		}
	} else {
		// Parse recipient JID
		recipientJID, err = types.ParseJID(recipient)
		if err != nil {
			return fmt.Errorf("failed to parse recipient JID: %v", err)
		}
	}

	// Prepare summary message with header
	now := time.Now()
	summaryMessage := fmt.Sprintf("📊 *Daily Summary*\n📅 %s\n\n%s",
		now.Format("2006-01-02"), summary)

	// Split message if too long
	const maxLength = 4000
	if len(summaryMessage) > maxLength {
		// Split into chunks
		for i := 0; i < len(summaryMessage); i += maxLength {
			end := i + maxLength
			if end > len(summaryMessage) {
				end = len(summaryMessage)
			}
			chunk := summaryMessage[i:end]

			// Add continuation marker for non-first chunks
			if i > 0 {
				chunk = fmt.Sprintf("... (continuação)\n%s", chunk)
			}

			msg := &waProto.Message{
				Conversation: proto.String(chunk),
			}

			_, err := client.SendMessage(context.Background(), recipientJID, msg)
			if err != nil {
				return fmt.Errorf("failed to send message chunk: %v", err)
			}

			// Small delay between chunks to avoid rate limiting
			time.Sleep(500 * time.Millisecond)
		}
	} else {
		// Send as single message
		msg := &waProto.Message{
			Conversation: proto.String(summaryMessage),
		}

		_, err := client.SendMessage(context.Background(), recipientJID, msg)
		if err != nil {
			return fmt.Errorf("failed to send message: %v", err)
		}
	}

	logger.Infof("Summary sent to %s (%d characters)", recipient, len(summaryMessage))
	return nil
}

// getGroupName retrieves the friendly name for a group from the database
func getGroupName(groupJID string, logger waLog.Logger) string {
	// Open SQLite database for chats
	db, err := sql.Open("sqlite3", "file:store/messages.db?_foreign_keys=on")
	if err != nil {
		logger.Warnf("Failed to open database to get group name: %v", err)
		return extractGroupIDFromJID(groupJID)
	}
	defer db.Close()

	var name string
	err = db.QueryRow("SELECT name FROM chats WHERE jid = ?", groupJID).Scan(&name)
	if err == nil && name != "" {
		return name
	}

	// If no name found, extract a simple identifier from the JID
	return extractGroupIDFromJID(groupJID)
}

// extractGroupIDFromJID extracts a simple group identifier from the JID
func extractGroupIDFromJID(groupJID string) string {
	// Format: 120363414686079039@g.us -> Group_120363414686079039
	if strings.Contains(groupJID, "@g.us") {
		parts := strings.Split(groupJID, "@")
		if len(parts) > 0 {
			return "Group_" + parts[0]
		}
	}
	return groupJID
}

// getUserRealName returns the real name for the user by looking up their JID in the database
func getUserRealName(userJID string, logger waLog.Logger) string {
	// Try to get name from chats table
	db, err := sql.Open("sqlite3", "file:store/messages.db?_foreign_keys=on")
	if err != nil {
		logger.Warnf("Failed to open database to get user real name: %v", err)
		return userJID
	}
	defer db.Close()

	// Look up user's name in chats table
	// The userJID might already have @s.whatsapp.net or might be just the phone number
	var name string
	var searchJID string
	if strings.Contains(userJID, "@") {
		searchJID = userJID
	} else {
		searchJID = userJID + "@s.whatsapp.net"
	}

	err = db.QueryRow("SELECT name FROM chats WHERE jid = ?", searchJID).Scan(&name)
	if err == nil && name != "" {
		return name
	}

	// Fallback: extract phone number if it's a JID format
	if strings.Contains(userJID, "@") {
		parts := strings.Split(userJID, "@")
		return parts[0]
	}

	return userJID
}

// segmentMessagesByTopic analyzes messages and groups them by topic using Claude
func segmentMessagesByTopic(messages []DailySummaryMessage, groupName, date string, logger waLog.Logger) (map[string][]DailySummaryMessage, error) {
	if len(messages) == 0 {
		return make(map[string][]DailySummaryMessage), nil
	}

	// Load topic segmentation prompt template
	prompt, err := loadTopicSegmentationPrompt(messages, date)
	if err != nil {
		logger.Warnf("Failed to load topic segmentation prompt, using fallback: %v", err)
		// Fallback: put all messages in one topic
		fallbackTopic := fmt.Sprintf("%s_daily_conversation", groupName)
		return map[string][]DailySummaryMessage{fallbackTopic: messages}, nil
	}

	// Call Claude API for topic segmentation
	response, err := callClaudeServer(prompt)
	if err != nil {
		logger.Warnf("Failed to segment topics, using fallback: %v", err)
		// Fallback: put all messages in one topic
		fallbackTopic := fmt.Sprintf("%s_daily_conversation", groupName)
		return map[string][]DailySummaryMessage{fallbackTopic: messages}, nil
	}

	// Parse the JSON response
	var topicSegments map[string]TopicSegment
	err = json.Unmarshal([]byte(response), &topicSegments)
	if err != nil {
		logger.Warnf("Failed to parse topic segmentation response, using fallback: %v", err)
		// Fallback: put all messages in one topic
		fallbackTopic := fmt.Sprintf("%s_daily_conversation", groupName)
		return map[string][]DailySummaryMessage{fallbackTopic: messages}, nil
	}

	// Convert to final format with actual message objects
	result := make(map[string][]DailySummaryMessage)
	for topicName, segment := range topicSegments {
		var topicMessages []DailySummaryMessage
		for _, msgIndex := range segment.Messages {
			if msgIndex >= 0 && msgIndex < len(messages) {
				topicMessages = append(topicMessages, messages[msgIndex])
			}
		}
		if len(topicMessages) > 0 {
			result[topicName] = topicMessages
		}
	}

	// If no valid topics found, use fallback
	if len(result) == 0 {
		fallbackTopic := fmt.Sprintf("%s_daily_conversation", groupName)
		result[fallbackTopic] = messages
	}

	logger.Infof("Segmented messages into %d topics", len(result))
	return result, nil
}

// loadTopicSegmentationPrompt loads and formats the topic segmentation prompt
func loadTopicSegmentationPrompt(messages []DailySummaryMessage, date string) (string, error) {
	// Try to load custom prompt template
	promptPath := "prompts/topic-segmentation.md"
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("failed to read topic segmentation prompt: %v", err)
	}

	promptTemplate := string(promptBytes)

	// Format messages as numbered list for segmentation analysis
	var messageLines []string
	for i, msg := range messages {
		direction := "←"
		if msg.IsFromMe {
			direction = "→"
		}
		messageLines = append(messageLines, fmt.Sprintf("%d. [%s] %s %s: %s",
			i, msg.Timestamp, direction, msg.Sender, msg.Content))
	}
	messagesText := strings.Join(messageLines, "\n")

	// Replace placeholders
	prompt := strings.ReplaceAll(promptTemplate, "{{MESSAGES}}", messagesText)
	prompt = strings.ReplaceAll(prompt, "{{DATE}}", date)

	return prompt, nil
}

// loadAddEpisodePrompt loads and formats the add episode prompt template
func loadAddEpisodePrompt(episodeName, topicName, groupName, date, episodeBody, sourceDescription string) (string, error) {
	// Try to load custom prompt template
	promptPath := "prompts/add-episode.md"
	promptBytes, err := os.ReadFile(promptPath)

	var promptTemplate string
	if err != nil {
		// Use default prompt if file doesn't exist
		promptTemplate = `Add this WhatsApp conversation segment to Graphiti's memory:

**Instructions:**
Use the mcp__graphiti__add_memory tool with the following parameters:
- name: "{{EPISODE_NAME}}"
- episode_body: "{{EPISODE_BODY}}"
- source: "message"
- source_description: "{{SOURCE_DESCRIPTION}}"

DO NOT SEND group_id as a parameter.
After adding the episode, confirm that it was successfully added to the knowledge graph.`
	} else {
		promptTemplate = string(promptBytes)
	}

	// Replace placeholders
	prompt := strings.ReplaceAll(promptTemplate, "{{EPISODE_NAME}}", episodeName)
	prompt = strings.ReplaceAll(prompt, "{{TOPIC_NAME}}", topicName)
	prompt = strings.ReplaceAll(prompt, "{{GROUP_NAME}}", groupName)
	prompt = strings.ReplaceAll(prompt, "{{DATE}}", date)
	prompt = strings.ReplaceAll(prompt, "{{EPISODE_BODY}}", strings.ReplaceAll(episodeBody, `"`, `\"`))
	prompt = strings.ReplaceAll(prompt, "{{SOURCE_DESCRIPTION}}", sourceDescription)

	return prompt, nil
}

// addEpisodesToGraphiti adds each topic segment as an episode to Graphiti
func addEpisodesToGraphiti(topicSegments map[string][]DailySummaryMessage, groupName, date string, logger waLog.Logger) error {
	successCount := 0
	totalTopics := len(topicSegments)

	// Get user's real name from the first "IsFromMe" message we find
	var userRealName string
	for _, messages := range topicSegments {
		for _, msg := range messages {
			if msg.IsFromMe {
				userRealName = getUserRealName(msg.Sender, logger)
				break
			}
		}
		if userRealName != "" {
			break
		}
	}

	for topicName, messages := range topicSegments {
		// Format messages for Graphiti (message format: "sender: message")
		var episodeLines []string
		for _, msg := range messages {
			senderName := msg.Sender
			if msg.IsFromMe {
				senderName = userRealName
			}
			episodeLines = append(episodeLines, fmt.Sprintf("%s: %s", senderName, msg.Content))
		}
		episodeBody := strings.Join(episodeLines, "\n")

		// Create episode name
		episodeName := fmt.Sprintf("%s", topicName)
		sourceDescription := fmt.Sprintf("WhatsApp_%s", groupName)

		// Load and format the add episode prompt
		addEpisodePrompt, err := loadAddEpisodePrompt(episodeName, topicName, groupName, date, episodeBody, sourceDescription)
		if err != nil {
			logger.Warnf("Failed to load add episode prompt for %s: %v", episodeName, err)
			continue
		}

		// Log the exact prompt being sent for debugging
		logger.Infof("Sending prompt to MCP server for episode %s:", episodeName)
		logger.Infof("--- START PROMPT ---")
		logger.Infof("%s", addEpisodePrompt)
		logger.Infof("--- END PROMPT ---")

		// Call Claude with Graphiti tools enabled
		_, err = callClaudeServerWithGraphiti(addEpisodePrompt)
		if err != nil {
			logger.Warnf("Failed to add episode %s to Graphiti: %v", episodeName, err)
			continue
		}

		successCount++
		logger.Infof("Successfully added episode %s to Graphiti", episodeName)
	}

	logger.Infof("Added %d/%d episodes to Graphiti successfully", successCount, totalTopics)

	if successCount == 0 {
		return fmt.Errorf("failed to add any episodes to Graphiti")
	}

	return nil
}

// callClaudeServerWithGraphiti calls Claude server with Graphiti tools enabled
func callClaudeServerWithGraphiti(prompt string) (string, error) {
	// Get configuration from environment
	claudeServer := os.Getenv("CLAUDE_SERVER_URL")
	if claudeServer == "" {
		claudeServer = "http://host.docker.internal:8888/claude"
	}

	// Use both WhatsApp and Graphiti tools
	allowedTools := "mcp__whatsapp,mcp__graphiti"

	// Prepare the request
	req := ClaudeRequest{
		Prompt: prompt,
		Args:   []string{"--allowedTools", allowedTools},
	}

	// Log the exact request being sent for debugging
	fmt.Printf("Sending request to Claude MCP server: %s\n", claudeServer)
	fmt.Printf("Allowed tools: %s\n", allowedTools)

	// Marshal the request to JSON
	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("error marshaling request: %v", err)
	}

	// Create the HTTP request
	httpReq, err := http.NewRequest("POST", claudeServer, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("error creating request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Create a client with timeout
	client := &http.Client{
		Timeout: 300 * time.Second,
	}

	// Send the request
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %v", err)
	}

	// Parse the response
	var claudeResp ClaudeResponse
	err = json.Unmarshal(body, &claudeResp)
	if err != nil {
		return "", fmt.Errorf("error parsing response: %v", err)
	}

	// Log the response for debugging (but truncate if very long)
	responseText := claudeResp.Result
	if len(responseText) > 500 {
		responseText = responseText[:500] + "... [truncated]"
	}
	fmt.Printf("Claude MCP response: %s\n", responseText)

	// Check for errors in the response
	if claudeResp.IsError {
		return "", fmt.Errorf("Claude returned an error: %s", claudeResp.Result)
	}

	return claudeResp.Result, nil
}
