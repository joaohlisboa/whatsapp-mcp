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
		promptTemplate = `Você é um assistente executivo analisando as conversas do dia no grupo Avante. 
Por favor, forneça:

1. **Resumo Executivo**: Principais discussões e decisões
2. **Ações Pendentes**: Tarefas identificadas e responsáveis  
3. **Métricas**: Empresas mencionadas, valuations discutidos
4. **Follow-ups Necessários**: Próximos passos sugeridos

Seja direto e conciso. Use dados e números sempre que mencionados.

Mensagens do dia ({{DATE}}):
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
	summaryMessage := fmt.Sprintf("📊 *Resumo Diário - Avante*\n📅 %s\n\n%s",
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
