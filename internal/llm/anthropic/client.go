package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"lioncli/internal/llm"
	"net/http"
	"time"
)

const (
	defaultBaseURL   = "https://api.anthropic.com/v1"
	defaultModel     = "claude-sonnet-4-6"
	anthropicVersion = "2023-06-01"
)

type Client struct {
	apiKey       string
	httpClient   *http.Client
	baseURL      string
	defaultModel string
}

type Option func(*Client)

func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = url }
}

func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

func WithDefaultModel(model string) Option {
	return func(c *Client) { c.defaultModel = model }
}

func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:       apiKey,
		baseURL:      defaultBaseURL,
		defaultModel: defaultModel,
		httpClient:   &http.Client{Timeout: 60 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Chat 实现 llm.Client 接口
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if req.Model == "" {
		req.Model = c.defaultModel
	}
	apiReq := toAPI(req)
	apiResp, err := c.doRequest(ctx, apiReq)
	if err != nil {
		return nil, err
	}
	return fromAPIResponse(*apiResp), nil
}

func (c *Client) doRequest(ctx context.Context, req apiRequest) (*apiResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: http call: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read body: %w", err)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		var apiErr apiErrorResponse
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("anthropic: %s (status %d): %s", apiErr.Error.Type, httpResp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("anthropic: http %d: %s", httpResp.StatusCode, string(respBody))
	}

	var resp apiResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("anthropic: unmarshal response: %w", err)
	}
	return &resp, nil
}
