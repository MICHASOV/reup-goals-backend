package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/proxy"
)

// ---------------------------------------------------------
// Клиент OpenAI Responses API
// ---------------------------------------------------------

type OpenAIClient struct {
	APIKey string
	Model  string
}

// SOCKS5 proxy = 127.0.0.1:10808
func newHTTPClientWithProxy() (*http.Client, error) {
	dialer, err := proxy.SOCKS5(
		"tcp",
		"127.0.0.1:10808",
		nil,
		proxy.Direct,
	)
	if err != nil {
		return nil, fmt.Errorf("socks5 dialer error: %w", err)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
	}

	client := &http.Client{
		Timeout:   120 * time.Second,
		Transport: transport,
	}

	return client, nil
}

func New(apiKey, model string) *OpenAIClient {
	return &OpenAIClient{
		APIKey: apiKey,
		Model:  model,
	}
}

// ---------------------------------------------------------
// API models
// ---------------------------------------------------------

type responseRequest struct {
	Model string      `json:"model"`
	Input interface{} `json:"input"`
}

type responseResponse struct {
	Output []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

// ---------------------------------------------------------
// Основной метод — EvaluateTask
// ---------------------------------------------------------

func (c *OpenAIClient) EvaluateTask(ctx context.Context, input map[string]interface{}) (json.RawMessage, error) {

	client, err := newHTTPClientWithProxy()
	if err != nil {
		return nil, fmt.Errorf("proxy init error: %w", err)
	}

	payload := responseRequest{
		Model: c.Model,
		Input: input,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx,
		"POST",
		"https://api.openai.com/v1/responses",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return nil, fmt.Errorf("request create error: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai error (%d): %s", resp.StatusCode, string(raw))
	}

	var parsed responseResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("json decode error: %w | body: %s", err, string(raw))
	}

	if len(parsed.Output) == 0 ||
		len(parsed.Output[0].Content) == 0 ||
		parsed.Output[0].Content[0].Text == "" {
		return nil, fmt.Errorf("no output from model")
	}

	return json.RawMessage(parsed.Output[0].Content[0].Text), nil
}
