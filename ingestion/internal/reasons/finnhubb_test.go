package reasons

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestFetchHeadlinesMaxThree(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			articles := []NewsArticle{
				{Headline: "Headline 1", Datetime: 5},
				{Headline: "Headline 2", Datetime: 4},
				{Headline: "Headline 3", Datetime: 3},
				{Headline: "Headline 4", Datetime: 2},
				{Headline: "Headline 5", Datetime: 1},
			}
			json.NewEncoder(w).Encode(articles)
		}))
	defer server.Close()

	client := &FinnhubClient{
		apiKey:     "test",
		baseURL:    server.URL,
		httpClient: server.Client(),
		logger:     log.New(os.Stderr, "[finnhub-test] ", log.LstdFlags),
	}

	headlines, err := client.FetchHeadlines(
		context.Background(), "AMTX")

	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if len(headlines) != 3 {
		t.Errorf("expected 3 headlines, got %d", len(headlines))
	}
}

func TestFetchHeadlinesEmpty(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]NewsArticle{})
		}))
	defer server.Close()

	client := &FinnhubClient{
		apiKey:     "test",
		baseURL:    server.URL,
		httpClient: server.Client(),
		logger:     log.New(os.Stderr, "[finnhub-test] ", log.LstdFlags),
	}

	headlines, err := client.FetchHeadlines(
		context.Background(), "EONR")

	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if len(headlines) != 0 {
		t.Errorf("expected empty slice, got %v", headlines)
	}
}

func TestFetchHeadlinesSortedByDate(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			articles := []NewsArticle{
				{Headline: "Old news", Datetime: 1},
				{Headline: "Latest news", Datetime: 100},
				{Headline: "Middle news", Datetime: 50},
			}
			json.NewEncoder(w).Encode(articles)
		}))
	defer server.Close()

	client := &FinnhubClient{
		apiKey:     "test",
		baseURL:    server.URL,
		httpClient: server.Client(),
		logger:     log.New(os.Stderr, "[finnhub-test] ", log.LstdFlags),
	}

	headlines, err := client.FetchHeadlines(
		context.Background(), "AMTX")

	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if len(headlines) == 0 {
		t.Fatal("expected headlines, got empty slice")
	}
	if headlines[0] != "Latest news" {
		t.Errorf("expected most recent headline first, got: %s",
			headlines[0])
	}
}