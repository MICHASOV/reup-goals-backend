package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// ---------------------------------------------------------
// OpenAI Client
// ---------------------------------------------------------

type OpenAIClient struct {
	APIKey          string
	Model           string
	ProxyURL        string
	MaxOutputTokens int
}

func New(apiKey, model string, proxyURL ...string) *OpenAIClient {
	selectedProxyURL := "socks5://127.0.0.1:10808"
	if len(proxyURL) > 0 && strings.TrimSpace(proxyURL[0]) != "" {
		selectedProxyURL = strings.TrimSpace(proxyURL[0])
	}
	return &OpenAIClient{
		APIKey:   apiKey,
		Model:    model,
		ProxyURL: selectedProxyURL,
	}
}

func (c *OpenAIClient) WithMaxOutputTokens(maxOutputTokens int) *OpenAIClient {
	if maxOutputTokens > 0 {
		c.MaxOutputTokens = maxOutputTokens
	}
	return c
}

func (c *OpenAIClient) newHTTPClient() (*http.Client, error) {
	if isDirectProxy(c.ProxyURL) {
		return &http.Client{Timeout: 75 * time.Second}, nil
	}

	proxyURL, err := url.Parse(c.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("proxy url parse error: %w", err)
	}
	if proxyURL.Scheme != "socks5" {
		return nil, fmt.Errorf("unsupported proxy scheme: %s", proxyURL.Scheme)
	}

	dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("socks5 dialer error: %w", err)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
	}

	return &http.Client{
		Timeout:   75 * time.Second,
		Transport: transport,
	}, nil
}

func isDirectProxy(value string) bool {
	normalized := strings.TrimSpace(strings.ToLower(value))
	return normalized == "" || normalized == "direct" || normalized == "none" || normalized == "off"
}

// ---------------------------------------------------------
// Responses API models
// ---------------------------------------------------------

type responsesRequest struct {
	Model           string                 `json:"model"`
	Input           string                 `json:"input"`
	Instructions    string                 `json:"instructions"`
	Text            map[string]interface{} `json:"text"`
	MaxOutputTokens int                    `json:"max_output_tokens,omitempty"`
}

type responsesResponse struct {
	Output []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

// ---------------------------------------------------------
// EvaluateTask — Path B
// ---------------------------------------------------------

func (c *OpenAIClient) EvaluateTask(
	ctx context.Context,
	input string, // <-- строка (обязательно)
) (json.RawMessage, error) {
	return c.GenerateJSON(ctx, SystemPrompt, input)
}

func (c *OpenAIClient) GenerateJSON(ctx context.Context, instructions string, input string) (json.RawMessage, error) {
	httpClient, err := c.newHTTPClient()
	if err != nil {
		return nil, fmt.Errorf("proxy init error: %w", err)
	}

	jsonInput := "Return valid JSON only.\n\nInput JSON:\n" + input

	// ❗ Правильный формат Responses API (Dec 2025)
	reqBody := responsesRequest{
		Model:           c.Model,
		Input:           jsonInput,
		Instructions:    instructions,
		MaxOutputTokens: c.MaxOutputTokens,
		Text: map[string]interface{}{
			"format": map[string]string{
				"type": "json_object",
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.openai.com/v1/responses",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai error (%d): %s", resp.StatusCode, string(raw))
	}

	var parsed responsesResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("json decode error: %w | body: %s", err, string(raw))
	}

	if len(parsed.Output) == 0 ||
		len(parsed.Output[0].Content) == 0 ||
		parsed.Output[0].Content[0].Text == "" {

		return nil, fmt.Errorf("empty model output")
	}

	return json.RawMessage(parsed.Output[0].Content[0].Text), nil
}
