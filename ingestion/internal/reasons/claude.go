// claude.go — Anthropic Claude API client.
// No pipeline logic lives here — this file only builds prompts and calls
// the /v1/messages endpoint.
//
// See ARCHITECTURE.md §6.2 — Claude as the reason generation LLM.
// See WINDSURF.md Rule 7   — model string must be hardcoded, never configurable.
package reasons

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// ClaudeClient makes authenticated HTTP requests to the Anthropic Messages API.
// httpClient uses a 30-second timeout — Claude inference takes longer than a
// plain data fetch and must not stall the pipeline indefinitely.
type ClaudeClient struct {
	apiKey     string
	baseURL    string      // "https://api.anthropic.com"
	model      string      // hardcoded per WINDSURF.md Rule 7 — never user-configurable
	httpClient *http.Client
	logger     *log.Logger
}

// ── Request / response structs ────────────────────────────────────────────────

// ClaudeRequest is the outbound JSON body for POST /v1/messages.
type ClaudeRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"` // capped at 100 — one sentence needs < 50 tokens
	Messages  []Message `json:"messages"`
}

// Message is a single turn in the Claude conversation.
// Role is always "user" for single-turn reason generation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ClaudeResponse is the inbound JSON body from POST /v1/messages.
type ClaudeResponse struct {
	Content []ContentBlock `json:"content"`
}

// ContentBlock holds one segment of Claude's reply.
// Type is "text" for plain text responses; Text is the actual reason string.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ── Constructor ───────────────────────────────────────────────────────────────

// NewClaudeClient initialises a ClaudeClient.
// The model is hardcoded to claude-haiku-4-5-20251001 per WINDSURF.md Rule 7 —
// changing the model requires a code change and review, not a config file edit.
func NewClaudeClient(apiKey string) *ClaudeClient {
	return &ClaudeClient{
		apiKey:  apiKey,
		baseURL: "https://api.anthropic.com",
		// WINDSURF.md Rule 7: model string must be hardcoded.
		model:      "claude-haiku-4-5-20251001",
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     log.New(os.Stderr, "[claude] ", log.LstdFlags),
	}
}

// ── Public methods ────────────────────────────────────────────────────────────

// GenerateReason builds a prompt for ticker based on its headlines and price
// movement, calls Claude, and returns a trimmed one-sentence reason string.
//
// Two prompt templates are used (see ARCHITECTURE.md §6.2):
//   - With headlines: asks Claude to explain the move using the news context.
//   - Without headlines: asks Claude to describe a technical/momentum move.
//
// Callers must handle the error case and use a fallback reason string —
// this function does not produce a fallback itself.
func (c *ClaudeClient) GenerateReason(
	ctx context.Context,
	ticker string,
	direction string,
	changePercent float64,
	headlines []string,
) (string, error) {
	prompt := c.buildPrompt(ticker, direction, changePercent, headlines)

	reqBody := ClaudeRequest{
		Model:     c.model,
		MaxTokens: 100, // one concise sentence needs < 50 tokens; 100 is a safe ceiling
		Messages: []Message{
			{Role: "user", Content: prompt},
		},
	}

	// json.Marshal serialises the struct to a []byte for the HTTP body.
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("GenerateReason(%s): marshal request: %w", ticker, err)
	}

	endpoint := c.baseURL + "/v1/messages"

	// bytes.NewReader wraps the body so it satisfies io.Reader without copying.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("GenerateReason(%s): build request: %w", ticker, err)
	}

	// Anthropic requires these three headers on every /v1/messages call.
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GenerateReason(%s): execute request: %w", ticker, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GenerateReason(%s): HTTP %d: %s", ticker, resp.StatusCode, string(body))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("GenerateReason(%s): read response: %w", ticker, err)
	}

	var claudeResp ClaudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return "", fmt.Errorf("GenerateReason(%s): unmarshal response: %w", ticker, err)
	}

	if len(claudeResp.Content) == 0 {
		return "", fmt.Errorf("GenerateReason(%s): empty content in response", ticker)
	}

	// strings.TrimSpace removes any leading/trailing whitespace or newlines
	// that Claude may add around its one-sentence response.
	reason := strings.TrimSpace(claudeResp.Content[0].Text)
	if reason == "" {
		return "", fmt.Errorf("GenerateReason(%s): empty text in first content block", ticker)
	}

	c.logger.Printf("GenerateReason(%s): generated %d chars", ticker, len(reason))
	return reason, nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

// buildPrompt constructs the Claude prompt for ticker.
// Uses the news-context template when headlines are available, and the
// no-news technical/momentum template when the slice is empty.
// See ARCHITECTURE.md §6.2 — exact prompt templates.
func (c *ClaudeClient) buildPrompt(ticker, direction string, changePercent float64, headlines []string) string {
	if len(headlines) > 0 {
		// Build a numbered list of headlines, one per line.
		var sb strings.Builder
		for i, h := range headlines {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, h)
		}
		headlineList := strings.TrimRight(sb.String(), "\n")

		return fmt.Sprintf(
			"You are a financial analyst assistant.\n"+
				"Given these recent news headlines for %s:\n\n"+
				"%s\n\n"+
				"The stock is moving %s %.2f%% today.\n"+
				"Write exactly one sentence explaining why.\n"+
				"Be specific, factual, and concise.\n"+
				"Maximum 30 words.",
			ticker, headlineList, direction, changePercent,
		)
	}

	// No headlines available — ask Claude to frame it as a technical move.
	return fmt.Sprintf(
		"You are a financial analyst assistant.\n"+
			"%s is moving %s %.2f%% today\n"+
			"with no specific news catalyst found.\n"+
			"Write exactly one sentence describing this as a\n"+
			"technical or momentum-driven move on elevated volume.\n"+
			"Maximum 20 words.",
		ticker, direction, changePercent,
	)
}
