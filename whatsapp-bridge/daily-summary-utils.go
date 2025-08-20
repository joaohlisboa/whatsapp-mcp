package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
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

// TopicSegment represents a topic with its associated messages
type TopicSegment struct {
	Messages []int  `json:"messages"`
	Summary  string `json:"summary"`
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
		if mediaType != "" && messageContent == "" {
			switch mediaType {
			case "image":
				messageContent = "[Imagem enviada]"
			case "video":
				messageContent = "[Vídeo enviado]"
			case "audio", "ptt":
				messageContent = "[Áudio enviado]"
			case "document":
				if filename != "" {
					messageContent = fmt.Sprintf("[Documento: %s]", filename)
				} else {
					messageContent = "[Documento enviado]"
				}
			default:
				messageContent = fmt.Sprintf("[%s enviado]", mediaType)
			}
		}

		// Get sender name for display
		senderName := getSenderName(sender, isFromMe, logger)

		// Replace @mentions with real names in message content
		processedContent := replaceMentionsWithNames(messageContent, logger)

		message := DailySummaryMessage{
			Timestamp: timestamp.Format("15:04"),
			Sender:    senderName,
			Content:   processedContent,
			IsFromMe:  isFromMe,
		}

		messages = append(messages, message)
	}

	logger.Infof("Retrieved %d messages from group %s for day %s", len(messages), groupJID, startOfDay.Format("2006-01-02"))
	return messages, nil
}

// getSenderName retrieves the display name for a sender
func getSenderName(sender string, isFromMe bool, logger waLog.Logger) string {
	// Handle empty sender (shouldn't happen but just in case)
	if sender == "" {
		return "Unknown"
	}

	// Convert phone number to full JID format if needed
	var fullJID string
	if strings.Contains(sender, "@") {
		fullJID = sender // Already has domain
	} else {
		fullJID = sender + "@s.whatsapp.net" // Add WhatsApp domain
	}

	// Try to get the real name from the contacts database
	realName := getUserRealName(fullJID, logger)
	if realName != "" {
		return realName
	}

	// If we couldn't get name from contacts, return just the phone number
	if strings.Contains(fullJID, "@s.whatsapp.net") {
		phoneNumber := strings.Split(fullJID, "@")[0]
		return phoneNumber
	}

	return sender
}

// getGroupName retrieves the display name for a group JID
func getGroupName(groupJID string, logger waLog.Logger) string {
	ctx := context.Background()

	// Open the WhatsApp database
	container, err := sqlstore.New(ctx, "sqlite3", "file:store/whatsapp.db?_foreign_keys=on", logger)
	if err != nil {
		logger.Errorf("Failed to connect to WhatsApp database: %v", err)
		return extractGroupIDFromJID(groupJID)
	}

	// Get all devices (should be just one)
	devices, err := container.GetAllDevices(ctx)
	if err != nil || len(devices) == 0 {
		logger.Errorf("Failed to get devices from WhatsApp database: %v", err)
		return extractGroupIDFromJID(groupJID)
	}

	return extractGroupIDFromJID(groupJID)
}

// extractGroupIDFromJID extracts a readable group ID from the full JID
func extractGroupIDFromJID(groupJID string) string {
	// Extract the group ID part (before @g.us)
	if strings.Contains(groupJID, "@g.us") {
		groupID := strings.Split(groupJID, "@")[0]
		return fmt.Sprintf("Grupo %s", groupID[len(groupID)-8:]) // Last 8 characters
	}
	return groupJID
}

// getUserRealName retrieves the real name of a user from the WhatsApp database
func getUserRealName(userJID string, logger waLog.Logger) string {
	ctx := context.Background()

	// Open the WhatsApp database
	container, err := sqlstore.New(ctx, "sqlite3", "file:store/whatsapp.db?_foreign_keys=on", logger)
	if err != nil {
		logger.Warnf("Failed to connect to WhatsApp database: %v", err)
		return ""
	}

	// Get all devices (should be just one)
	devices, err := container.GetAllDevices(ctx)
	if err != nil || len(devices) == 0 {
		logger.Warnf("Failed to get devices from WhatsApp database: %v", err)
		return ""
	}

	// Use the first device to get contact info
	device := devices[0]

	// Parse the JID
	parsedJID, err := types.ParseJID(userJID)
	if err != nil {
		logger.Warnf("Failed to parse JID %s: %v", userJID, err)
		return ""
	}

	// Try to get contact info
	contactInfo, err := device.Contacts.GetContact(ctx, parsedJID)
	if err != nil {
		logger.Warnf("Failed to get contact info for %s: %v", userJID, err)
		return ""
	}

	// Return the contact name if available
	if contactInfo.FullName != "" {
		return contactInfo.FullName
	}
	if contactInfo.FirstName != "" {
		return contactInfo.FirstName
	}
	if contactInfo.PushName != "" {
		return contactInfo.PushName
	}

	return ""
}

// replaceMentionsWithNames replaces @phone_number mentions with real contact names
func replaceMentionsWithNames(content string, logger waLog.Logger) string {
	// Regular expression to find @mentions (@ followed by phone numbers)
	mentionPattern := `@(\+?[0-9]{10,15})`

	// Use regex to find and replace all @mentions
	re := regexp.MustCompile(mentionPattern)

	result := re.ReplaceAllStringFunc(content, func(match string) string {
		// Extract the phone number (remove @ and optional +)
		phoneNumber := strings.TrimPrefix(match, "@")
		phoneNumber = strings.TrimPrefix(phoneNumber, "+")

		// Convert to full JID format
		fullJID := phoneNumber + "@s.whatsapp.net"

		// Try to get the real name
		realName := getUserRealName(fullJID, logger)
		if realName != "" {
			return "@" + realName
		}

		// If no name found, return the original mention
		return match
	})

	return result
}

// segmentMessagesByTopic groups messages into topic-based segments using Claude AI
func segmentMessagesByTopic(messages []DailySummaryMessage, groupName, date string, logger waLog.Logger) (map[string][]DailySummaryMessage, error) {
	if len(messages) == 0 {
		return make(map[string][]DailySummaryMessage), nil
	}

	// Load the topic segmentation prompt
	prompt, err := loadTopicSegmentationPrompt(messages, date)
	if err != nil {
		return nil, fmt.Errorf("failed to load topic segmentation prompt: %v", err)
	}

	// Call Claude API for topic segmentation
	response, err := callClaudeServer(prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to get topic segmentation from Claude: %v", err)
	}

	logger.Infof("Received topic segmentation response from Claude")

	// Extract JSON from markdown code blocks if present
	jsonContent := extractJSONFromMarkdown(response)

	// Parse the JSON response (expecting map format from prompt)
	var segments map[string]TopicSegment
	err = json.Unmarshal([]byte(jsonContent), &segments)
	if err != nil {
		logger.Warnf("Failed to parse topic segmentation JSON: %v", err)
		logger.Warnf("Response content: %s", jsonContent)
		return nil, fmt.Errorf("failed to parse topic segmentation JSON: %v", err)
	}

	// Convert segments to map of topic -> messages
	topicSegments := make(map[string][]DailySummaryMessage)
	for topicName, segment := range segments {
		var topicMessages []DailySummaryMessage
		for _, messageIndex := range segment.Messages {
			if messageIndex >= 0 && messageIndex < len(messages) {
				topicMessages = append(topicMessages, messages[messageIndex])
			}
		}
		if len(topicMessages) > 0 {
			topicSegments[topicName] = topicMessages
		}
	}

	logger.Infof("Successfully segmented %d messages into %d topics", len(messages), len(topicSegments))
	return topicSegments, nil
}

// loadTopicSegmentationPrompt loads and formats the topic segmentation prompt
func loadTopicSegmentationPrompt(messages []DailySummaryMessage, date string) (string, error) {
	// Load the prompt template from file
	promptTemplate, err := os.ReadFile("prompts/topic-segmentation.md")
	if err != nil {
		return "", fmt.Errorf("failed to read topic segmentation prompt template: %v", err)
	}

	// Format messages as JSON for the prompt
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return "", fmt.Errorf("failed to marshal messages to JSON: %v", err)
	}

	// Replace placeholders in the template
	prompt := string(promptTemplate)
	prompt = strings.ReplaceAll(prompt, "{{MESSAGES}}", string(messagesJSON))
	prompt = strings.ReplaceAll(prompt, "{{DATE}}", date)

	return prompt, nil
}

// loadAddEpisodePrompt loads and formats the add episode prompt for Graphiti
func loadAddEpisodePrompt(episodeName, topicName, groupName, date, episodeBody, sourceDescription string) (string, error) {
	// Load the prompt template from file
	promptTemplate, err := os.ReadFile("prompts/add-episode.md")
	if err != nil {
		return "", fmt.Errorf("failed to read add episode prompt template: %v", err)
	}

	// Replace placeholders in the template
	prompt := string(promptTemplate)
	prompt = strings.ReplaceAll(prompt, "{{EPISODE_NAME}}", episodeName)
	prompt = strings.ReplaceAll(prompt, "{{TOPIC_NAME}}", topicName)
	prompt = strings.ReplaceAll(prompt, "{{GROUP_NAME}}", groupName)
	prompt = strings.ReplaceAll(prompt, "{{DATE}}", date)
	prompt = strings.ReplaceAll(prompt, "{{EPISODE_BODY}}", episodeBody)
	prompt = strings.ReplaceAll(prompt, "{{SOURCE_DESCRIPTION}}", sourceDescription)

	return prompt, nil
}

// addEpisodesToGraphiti adds topic segments as episodes to the Graphiti knowledge graph
func addEpisodesToGraphiti(topicSegments map[string][]DailySummaryMessage, groupName, date string, logger waLog.Logger) error {
	if len(topicSegments) == 0 {
		logger.Infof("No topic segments to add to Graphiti")
		return nil
	}

	var successCount int
	for topicName, messages := range topicSegments {
		// Format messages as episode body
		var episodeBody strings.Builder
		for i, message := range messages {
			episodeBody.WriteString(fmt.Sprintf("%s: %s", message.Sender, message.Content))
			if i < len(messages)-1 {
				episodeBody.WriteString("\n")
			}
		}

		// Create episode name
		episodeName := fmt.Sprintf("%s - %s", date, topicName)

		// Load and format the add episode prompt
		addEpisodePrompt, err := loadAddEpisodePrompt(
			episodeName,
			topicName,
			groupName,
			date,
			episodeBody.String(),
			"WhatsApp group conversation daily summary",
		)
		if err != nil {
			logger.Errorf("Failed to load add episode prompt for topic '%s': %v", topicName, err)
			continue
		}

		// Call Claude with Graphiti tools to add the episode
		_, err = callClaudeServer(addEpisodePrompt, "mcp__graphiti")
		if err != nil {
			logger.Errorf("Failed to add episode to Graphiti for topic '%s': %v", topicName, err)
			continue
		}

		logger.Infof("Successfully added episode to Graphiti for topic: %s", topicName)
		successCount++
	}

	if successCount == 0 {
		return fmt.Errorf("failed to add any episodes to Graphiti")
	}

	return nil
}

// sendToRecipient sends a message to a specific recipient using the WhatsApp client
func sendToRecipient(message, recipient string, logger waLog.Logger) error {
	ctx := context.Background()

	// Try to initialize WhatsApp client for sending
	container, err := sqlstore.New(ctx, "sqlite3", "file:store/whatsapp.db?_foreign_keys=on", waLog.Stdout("Database", "ERROR", true))
	if err != nil {
		return fmt.Errorf("failed to connect to database: %v", err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("failed to get device: %v", err)
	}

	client := whatsmeow.NewClient(deviceStore, waLog.Stdout("Client", "INFO", true))
	defer client.Disconnect()

	// Connect to WhatsApp
	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %v", err)
	}

	// Handle different recipient types
	var targetJID types.JID
	var err2 error

	if recipient == "self" {
		// Send to self (status broadcast)
		targetJID = types.NewJID(client.Store.ID.User, types.DefaultUserServer)
	} else {
		// Parse as regular JID
		targetJID, err2 = types.ParseJID(recipient)
		if err2 != nil {
			return fmt.Errorf("failed to parse recipient JID: %v", err2)
		}
	}

	// Create and send message
	msg := &waProto.Message{
		Conversation: proto.String(message),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = client.SendMessage(ctx, targetJID, msg)
	if err != nil {
		return fmt.Errorf("failed to send message: %v", err)
	}

	logger.Infof("Successfully sent message to %s", recipient)
	return nil
}

// extractJSONFromMarkdown extracts JSON content from markdown code blocks
func extractJSONFromMarkdown(response string) string {
	// Look for ```json...``` blocks
	jsonStart := strings.Index(response, "```json")
	if jsonStart == -1 {
		// Try just ``` blocks
		jsonStart = strings.Index(response, "```")
		if jsonStart == -1 {
			// No markdown, return as-is
			return response
		}
	}

	// Find the start of JSON content (after the opening ```)
	contentStart := strings.Index(response[jsonStart:], "\n")
	if contentStart == -1 {
		return response
	}
	contentStart += jsonStart + 1

	// Find the closing ```
	jsonEnd := strings.Index(response[contentStart:], "```")
	if jsonEnd == -1 {
		// No closing backticks, return from content start to end
		return strings.TrimSpace(response[contentStart:])
	}

	// Extract just the JSON content
	jsonContent := response[contentStart : contentStart+jsonEnd]
	return strings.TrimSpace(jsonContent)
}
