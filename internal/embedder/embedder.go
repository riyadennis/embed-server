// Package embedder calls a local Ollama server to produce vector embeddings.
//
// Ollama exposes POST /api/embeddings with JSON {"model": "...", "prompt": "..."}
// and returns {"embedding": [...]}. We hit it directly to avoid pulling in the
// Ollama Go SDK for one endpoint.
package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a thin wrapper around Ollama's embeddings endpoint.
type Client struct {
	BaseURL string
	Model   string
	HTTP    *http.Client
}

// New returns a Client pointed at the given Ollama instance.
// Pass "" for baseURL to use the default localhost:11434.
func New(baseURL, model string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &Client{
		BaseURL: baseURL,
		Model:   model,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

type embedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type embedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// Embed produces a single embedding vector for the given text.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(embedRequest{Model: c.Model, Prompt: text})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(b))
	}

	var out embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(out.Embedding) == 0 {
		return nil, fmt.Errorf("ollama returned empty embedding")
	}
	return out.Embedding, nil
}

// EmbedBatch embeds many texts sequentially. Ollama doesn't have a true
// batch endpoint, but a small concurrency pool would be a worthwhile
// improvement if you're embedding huge files.
func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := c.Embed(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("embed chunk %d: %w", i, err)
		}
		out[i] = v
	}
	return out, nil
}
