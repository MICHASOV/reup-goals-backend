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
	Model  string // Assistant ID или gpt-4.1-mini
}

func New(apiKey, model string) *OpenAIClient {
	return &OpenAIClient{
		APIKey: apiKey,
		Model:  model,
	}
}

// ---------------------------------------------------------
// HTTP Client с SOCKS5 через XRay
// ---------------------------------------------------------

func buildProxiedHTTPClient() (*http.Client, error) {
	// Адрес SOCKS5 Xray
	proxyURL := "127.0.0.1:10808"

	dialer, err := proxy.SOCKS5("tcp", proxyURL, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("failed to create socks5 dialer: %w", err)
	}

	// Превращаем SOCKS5 dialer в net.Dialer совместимый объект
	netDialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.Dial(network, address)
	}

	transport := &http.Transport{
		DialContext: netDialer,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   90 * time.Second,
	}

	return client, nil
}

// ---------------------------------------------------------
// Запрос к Responses API
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

	payload := responseRequest{
		Model: c.Model,
		Input: input,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

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

	// ⭐ ВАЖНО — создаём клиент с SOCKS5 + XRay
	httpClient, err := buildProxiedHTTPClient()
	if err != nil {
		return nil, fmt.Errorf("proxy client error: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	// Проверка на ошибки API
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai error (%d): %s", resp.StatusCode, string(raw))
	}

	var parsed responseResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("json decode error: %w | body: %s", err, string(raw))
	}

	// Проверяем наличие текста
	if len(parsed.Output) == 0 ||
		len(parsed.Output[0].Content) == 0 ||
		parsed.Output[0].Content[0].Text == "" {
		return nil, fmt.Errorf("no output from model")
	}

	return json.RawMessage(parsed.Output[0].Content[0].Text), nil
}
