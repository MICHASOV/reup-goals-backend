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
// Модели Responses API
// ---------------------------------------------------------

type ResponseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ResponseRequest struct {
	Model          string            `json:"model"`
	Messages       []ResponseMessage `json:"messages"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

type ResponseOutput struct {
	Output []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

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

// Используем SOCKS5 — как у тебя было
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
// EvaluateTask — принимает MESSAGES, а не JSON input
// ---------------------------------------------------------

func (c *OpenAIClient) EvaluateTask(
	ctx context.Context,
	messages []map[string]string, // <-- то, что формирует BuildChatPrompt
) (json.RawMessage, error) {

	httpClient, err := newHTTPClientWithProxy()
	if err != nil {
		return nil, fmt.Errorf("proxy init error: %w", err)
	}

	// Преобразуем messages в формат Responses API
	var formatted []ResponseMessage
	for _, m := range messages {
		formatted = append(formatted, ResponseMessage{
			Role:    m["role"],
			Content: m["content"],
		})
	}

	// Сборка тела запроса (новый формат Responses API)
	reqBody := map[string]interface{}{
		"model":    c.Model,
		"messages": formatted,
		"text": map[string]interface{}{
			"format": "json",
		},
	}

	body, err := json.Marshal(reqBody)

	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	// Формируем запрос
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		"https://api.openai.com/v1/responses",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return nil, fmt.Errorf("request create error: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	// Отправка
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai error (%d): %s", resp.StatusCode, string(raw))
	}

	var parsed ResponseOutput
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
