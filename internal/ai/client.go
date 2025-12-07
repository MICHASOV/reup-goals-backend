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
// OpenAI Client
// ---------------------------------------------------------

type OpenAIClient struct {
	APIKey string
	Model  string
}

func New(apiKey, model string) *OpenAIClient {
	return &OpenAIClient{
		APIKey: apiKey,
		Model:  model,
	}
}

// SOCKS5 proxy
func newHTTPClientWithProxy() (*http.Client, error) {
	dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:10808", nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("socks5 dialer error: %w", err)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
	}

	return &http.Client{
		Timeout:   120 * time.Second,
		Transport: transport,
	}, nil
}

// ---------------------------------------------------------
// Responses API models
// ---------------------------------------------------------

type responsesRequest struct {
	Model        string                 `json:"model"`
	Input        interface{}            `json:"input"`
	Instructions string                 `json:"instructions"`
	Text         map[string]interface{} `json:"text"`
}

type responsesResponse struct {
	Output []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

// ---------------------------------------------------------
// EvaluateTask — Path B (input + instructions)
// ---------------------------------------------------------

func (c *OpenAIClient) EvaluateTask(
	ctx context.Context,
	input string, // теперь просто строка
) (json.RawMessage, error) {

	httpClient, err := newHTTPClientWithProxy()
	if err != nil {
		return nil, fmt.Errorf("proxy init error: %w", err)
	}

	reqBody := responsesRequest{
		Model:        c.Model,
		Input:        input,        // <-- строка
		Instructions: SystemPrompt, // <-- системный промпт
		Text: map[string]interface{}{
			"format": "json_object",
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
