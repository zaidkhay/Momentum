package reasons

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"log"
	"os"
	"testing"
)

func TestGenerateReasonWithHeadlines(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-api-key") == "" {
				t.Error("missing x-api-key header")
			}
			if r.Header.Get("anthropic-version") == "" {
				t.Error("missing anthropic-version header")
			}
			resp := ClaudeResponse{
				Content: []ContentBlock{
					{Type: "text", Text: "DOE grant triggered short squeeze."},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
	defer server.Close()

	client := &ClaudeClient{
		apiKey:     "test",
		baseURL:    server.URL,
		model:      "claude-haiku-4-5-20251001",
		httpClient: server.Client(),
		logger:     log.New(os.Stderr, "[claude-test] ", log.LstdFlags),
	}

	reason, err := client.GenerateReason(
		context.Background(),
		"AMTX",
		"up",
		34.2,
		[]string{"DOE awards grant", "Short interest high"},
	)

	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestGenerateReasonNoHeadlines(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := ClaudeResponse{
				Content: []ContentBlock{
					{Type: "text",
						Text: "Technical momentum move on elevated volume."},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
	defer server.Close()

	client := &ClaudeClient{
		apiKey:     "test",
		baseURL:    server.URL,
		model:      "claude-haiku-4-5-20251001",
		httpClient: server.Client(),
		logger:     log.New(os.Stderr, "[claude-test] ", log.LstdFlags),
	}

	reason, err := client.GenerateReason(
		context.Background(),
		"EONR",
		"up",
		18.5,
		[]string{},
	)

	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if reason == "" {
		t.Error("expected fallback reason")
	}
}

func TestModelIsHaiku(t *testing.T) {
	var capturedModel string

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req ClaudeRequest
			json.NewDecoder(r.Body).Decode(&req)
			capturedModel = req.Model

			resp := ClaudeResponse{
				Content: []ContentBlock{
					{Type: "text", Text: "Some reason."},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
	defer server.Close()

	client := &ClaudeClient{
		apiKey:     "test",
		baseURL:    server.URL,
		model:      "claude-haiku-4-5-20251001",
		httpClient: server.Client(),
		logger:     log.New(os.Stderr, "[claude-test] ", log.LstdFlags),
	}

	client.GenerateReason(
		context.Background(),
		"AMTX", "up", 34.2,
		[]string{"headline one"},
	)

	if capturedModel != "claude-haiku-4-5-20251001" {
		t.Errorf("expected haiku model, got: %s", capturedModel)
	}
}