package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

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

// callClaudeServer sends a message to the Claude Code HTTP server with optional tools
// If no tools are specified, uses environment variable or defaults to "mcp__whatsapp"
// If tools are specified, joins them with commas
func callClaudeServer(prompt string, tools ...string) (string, error) {
	// Get configuration from environment
	claudeServer := os.Getenv("CLAUDE_SERVER_URL")
	if claudeServer == "" {
		claudeServer = "http://host.docker.internal:8888/claude"
	}

	// Determine allowed tools
	var allowedTools string
	if len(tools) > 0 {
		allowedTools = strings.Join(tools, ",")
	} else {
		allowedTools = os.Getenv("CLAUDE_ALLOWED_TOOLS")
	}

	// Enable debug logging for Graphiti tools (when multiple tools are specified)
	enableDebugLogging := len(tools) > 0 && strings.Contains(allowedTools, "mcp__graphiti")

	// Prepare the request
	req := ClaudeRequest{
		Prompt: prompt,
		Args:   []string{"--allowedTools", allowedTools},
	}

	if enableDebugLogging {
		// Log the exact request being sent for debugging
		fmt.Printf("Sending request to Claude MCP server: %s\n", claudeServer)
		fmt.Printf("Allowed tools: %s\n", allowedTools)
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

	if enableDebugLogging {
		// Log the response for debugging (but truncate if very long)
		responseText := claudeResp.Result
		if len(responseText) > 500 {
			responseText = responseText[:500] + "... [truncated]"
		}
		fmt.Printf("Claude MCP response: %s\n", responseText)
	}

	// Check for errors in the response
	if claudeResp.IsError {
		return "", fmt.Errorf("Claude returned an error: %s", claudeResp.Result)
	}

	return claudeResp.Result, nil
}
