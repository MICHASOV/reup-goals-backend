package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ---------------------------------------------------------
// Клиент OpenAI Responses API
// ---------------------------------------------------------

type OpenAIClient struct {
	APIKey string
	Model  string // сюда передаём Assistant ID (или модель)
}

func New(apiKey, model string) *OpenAIClient {
	return &OpenAIClient{
		APIKey: apiKey,
		Model:  model,
	}
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

	// Формируем payload для /v1/responses
	payload := responseRequest{
		Model: c.Model, // ассистент работает как модель
		Input: input,   // сюда отправляем JSON из бэка
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

	client := &http.Client{Timeout: 90 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	// Чтение тела ответа
	raw, _ := io.ReadAll(resp.Body)

	// Обработка статус-кода
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai error (%d): %s", resp.StatusCode, string(raw))
	}

	var parsed responseResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("json decode error: %w | body: %s", err, string(raw))
	}

	// Проверяем, что модель вернула текст
	if len(parsed.Output) == 0 ||
		len(parsed.Output[0].Content) == 0 ||
		parsed.Output[0].Content[0].Text == "" {
		return nil, fmt.Errorf("no output from model")
	}

	assistantText := parsed.Output[0].Content[0].Text

	// Возвращаем как JSON-фрагмент
	return json.RawMessage(assistantText), nil
}
