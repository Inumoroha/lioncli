package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxEmbeddingInputChars = 2000

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type EmbeddingClient struct {
	Provider   string
	Model      string
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func NewEmbeddingClientFromEnv() *EmbeddingClient {
	provider := envOrDefault("EMBEDDING_PROVIDER", "ollama")
	return &EmbeddingClient{
		Provider: provider,
		Model:    envOrDefault("EMBEDDING_MODEL", "nomic-embed-text:latest"),
		BaseURL:  envOrDefault("EMBEDDING_BASE_URL", inferDefaultURL(provider)),
		APIKey:   envOrDefault("EMBEDDING_API_KEY", ""),
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if c == nil {
		return nil, errors.New("embedding client is nil")
	}
	if text == "" {
		return nil, nil
	}

	input := truncateRunes(text, maxEmbeddingInputChars)
	switch strings.ToLower(c.Provider) {
	case "ollama":
		return c.embedOllama(ctx, input)
	case "openai", "zhipu", "glm":
		return c.embedOpenAICompatible(ctx, input)
	default:
		return c.embedOllama(ctx, input)
	}
}

func (c *EmbeddingClient) embedOllama(ctx context.Context, text string) ([]float32, error) {
	body := map[string]any{
		"model":  c.Model,
		"prompt": text,
	}
	var response struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := c.postJSON(ctx, strings.TrimRight(c.BaseURL, "/")+"/api/embeddings", body, false, &response); err != nil {
		return nil, err
	}
	return toFloat32(response.Embedding), nil
}

func (c *EmbeddingClient) embedOpenAICompatible(ctx context.Context, text string) ([]float32, error) {
	body := map[string]any{
		"model": c.Model,
		"input": text,
	}
	var response struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := c.postJSON(ctx, strings.TrimRight(c.BaseURL, "/")+"/embeddings", body, true, &response); err != nil {
		return nil, err
	}
	if len(response.Data) == 0 {
		return nil, errors.New("embedding API returned empty data")
	}
	return toFloat32(response.Data[0].Embedding), nil
}

func (c *EmbeddingClient) postJSON(ctx context.Context, url string, payload any, useAuth bool, target any) error {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 120 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if useAuth && c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("embedding API request failed [" + resp.Status + "]: " + string(respBody))
	}
	return json.Unmarshal(respBody, target)
}

func envOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func inferDefaultURL(provider string) string {
	switch strings.ToLower(provider) {
	case "ollama":
		return "http://localhost:11434"
	case "zhipu", "glm":
		return "https://open.bigmodel.cn/api/paas/v4"
	case "openai":
		return "https://api.openai.com/v1"
	default:
		return "http://localhost:11434"
	}
}

func truncateRunes(input string, limit int) string {
	runes := []rune(input)
	if len(runes) <= limit {
		return input
	}
	return string(runes[:limit])
}

func toFloat32(values []float64) []float32 {
	result := make([]float32, len(values))
	for i, value := range values {
		result[i] = float32(value)
	}
	return result
}
