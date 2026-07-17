package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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
	governance      Governance
}

func (c *OpenAIClient) ModelName() string {
	return c.Model
}

func (c *OpenAIClient) ForModel(model string) Provider {
	clone := *c
	clone.Model = strings.TrimSpace(model)
	return &clone
}

func (c *OpenAIClient) WithGovernance(governance Governance) *OpenAIClient {
	c.governance = governance
	return c
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
	Model              string                   `json:"model"`
	Input              string                   `json:"input"`
	Instructions       string                   `json:"instructions"`
	Text               map[string]interface{}   `json:"text,omitempty"`
	MaxOutputTokens    int                      `json:"max_output_tokens,omitempty"`
	PreviousResponseID string                   `json:"previous_response_id,omitempty"`
	ContextManagement  []map[string]interface{} `json:"context_management,omitempty"`
	Tools              []map[string]interface{} `json:"tools,omitempty"`
	PromptCacheKey     string                   `json:"prompt_cache_key,omitempty"`
}

type responsesResponse struct {
	ID     string `json:"id"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage Usage `json:"usage"`
}

type transcriptionResponse struct {
	Text  string `json:"text"`
	Usage Usage  `json:"usage"`
}

type Usage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`
	InputTokenDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func (u Usage) CachedInputTokens() int {
	return u.InputTokenDetails.CachedTokens
}

type TextResult struct {
	Text       string
	ResponseID string
	Usage      Usage
}

type ResponseContextOptions struct {
	PreviousResponseID   string
	VectorStoreIDs       []string
	CompactThreshold     int
	PromptCacheKey       string
	MaxFileSearchResults int
	MaxOutputTokens      int
	RequestTimeout       time.Duration
	Model                string
}

type OpenAIFile struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Purpose  string `json:"purpose"`
	Bytes    int64  `json:"bytes"`
	Status   string `json:"status"`
}

type OpenAIVectorStore struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type OpenAIVectorStoreFile struct {
	ID            string `json:"id"`
	FileID        string `json:"file_id"`
	VectorStoreID string `json:"vector_store_id"`
	Status        string `json:"status"`
	LastError     struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"last_error"`
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
	result, err := c.generateResponseText(ctx, instructions, "Return valid JSON only.\n\nInput JSON:\n"+input, map[string]interface{}{
		"format": map[string]string{
			"type": "json_object",
		},
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(result.Text), nil
}

func (c *OpenAIClient) GenerateText(ctx context.Context, instructions string, input string) (string, error) {
	result, err := c.GenerateTextDetailed(ctx, instructions, input)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

func (c *OpenAIClient) GenerateTextDetailed(ctx context.Context, instructions string, input string) (TextResult, error) {
	return c.generateResponseTextWithOptions(ctx, instructions, input, nil, ResponseContextOptions{})
}

func (c *OpenAIClient) GenerateTextNative(ctx context.Context, instructions string, input string, options ResponseContextOptions) (TextResult, error) {
	return c.generateResponseTextWithOptions(ctx, instructions, input, nil, options)
}

func (c *OpenAIClient) GenerateJSONNative(ctx context.Context, instructions string, input string, options ResponseContextOptions) (TextResult, error) {
	return c.generateResponseTextWithOptions(ctx, instructions, "Return valid JSON only.\n\nInput JSON:\n"+input, map[string]interface{}{
		"format": map[string]string{
			"type": "json_object",
		},
	}, options)
}

func (c *OpenAIClient) TranscribeAudio(ctx context.Context, filename string, language string, audio io.Reader) (text string, resultErr error) {
	model := strings.TrimSpace(c.Model)
	if model == "" {
		model = "gpt-4o-transcribe"
	}
	prompt := "Русская разговорная речь пользователя о бизнесе, продукте, стратегии, целях, клиентах, продажах, маркетинге, CRM, SaaS, метриках, LTV, CAC, юнит-экономике, проблемах и ограничениях. Сохраняй смысл, термины, названия компаний и стиль речи."
	resolved := ResolvedCall{
		Metadata: CallMetadataFromContext(ctx), Instructions: prompt, Model: model, Provider: "openai",
	}
	if c.governance != nil {
		var err error
		resolved, err = c.governance.BeforeCall(ctx, resolved.Metadata, prompt, model)
		if err != nil {
			return "", err
		}
	}
	started := time.Now()
	var usage Usage
	if c.governance != nil {
		defer func() {
			logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			c.governance.AfterCall(logCtx, resolved, CallResult{
				Usage: usage, LatencyMS: time.Since(started).Milliseconds(), Err: resultErr,
			})
		}()
	}

	httpClient, err := c.newHTTPClient()
	if err != nil {
		return "", fmt.Errorf("proxy init error: %w", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	fileWriter, err := writer.CreateFormFile("file", safeAudioFilename(filename))
	if err != nil {
		return "", fmt.Errorf("multipart file error: %w", err)
	}
	if _, err := io.Copy(fileWriter, audio); err != nil {
		return "", fmt.Errorf("audio copy error: %w", err)
	}

	_ = writer.WriteField("model", resolved.Model)
	_ = writer.WriteField("response_format", "json")
	if strings.TrimSpace(language) != "" {
		_ = writer.WriteField("language", strings.TrimSpace(language))
	}
	_ = writer.WriteField("prompt", resolved.Instructions)

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("multipart close error: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.openai.com/v1/audio/transcriptions",
		body,
	)
	if err != nil {
		return "", fmt.Errorf("request error: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("openai transcription error (%d): %s", resp.StatusCode, string(raw))
	}

	var parsed transcriptionResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("transcription json decode error: %w | body: %s", err, string(raw))
	}
	usage = parsed.Usage

	text = strings.TrimSpace(parsed.Text)
	if text == "" {
		return "", fmt.Errorf("empty transcription")
	}
	return text, nil
}

func safeAudioFilename(filename string) string {
	name := strings.TrimSpace(filename)
	if name == "" {
		return "voice.webm"
	}
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	return name
}

func (c *OpenAIClient) generateResponseText(ctx context.Context, instructions string, input string, textFormat map[string]interface{}) (TextResult, error) {
	return c.generateResponseTextWithOptions(ctx, instructions, input, textFormat, ResponseContextOptions{})
}

func (c *OpenAIClient) generateResponseTextWithOptions(ctx context.Context, instructions string, input string, textFormat map[string]interface{}, options ResponseContextOptions) (result TextResult, resultErr error) {
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = c.Model
	}
	resolved := ResolvedCall{
		Metadata:     CallMetadataFromContext(ctx),
		Instructions: instructions,
		Model:        model,
		Provider:     "openai",
	}
	if c.governance != nil {
		var err error
		resolved, err = c.governance.BeforeCall(ctx, resolved.Metadata, instructions, model)
		if err != nil {
			return TextResult{}, err
		}
	}
	started := time.Now()
	if c.governance != nil {
		defer func() {
			logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			c.governance.AfterCall(logCtx, resolved, CallResult{
				ResponseID: result.ResponseID,
				Usage:      result.Usage,
				LatencyMS:  time.Since(started).Milliseconds(),
				Err:        resultErr,
			})
		}()
	}

	httpClient, err := c.newHTTPClient()
	if err != nil {
		return TextResult{}, fmt.Errorf("proxy init error: %w", err)
	}
	if options.RequestTimeout > 0 {
		httpClient.Timeout = options.RequestTimeout
	}

	reqBody := responsesRequest{
		Model:              resolved.Model,
		Input:              input,
		Instructions:       resolved.Instructions,
		MaxOutputTokens:    c.MaxOutputTokens,
		Text:               textFormat,
		PreviousResponseID: strings.TrimSpace(options.PreviousResponseID),
		PromptCacheKey:     strings.TrimSpace(options.PromptCacheKey),
	}
	if options.MaxOutputTokens > 0 {
		reqBody.MaxOutputTokens = options.MaxOutputTokens
	}
	if options.CompactThreshold > 0 {
		reqBody.ContextManagement = []map[string]interface{}{
			{
				"type":              "compaction",
				"compact_threshold": options.CompactThreshold,
			},
		}
	}
	if len(options.VectorStoreIDs) > 0 {
		fileSearchTool := map[string]interface{}{
			"type":             "file_search",
			"vector_store_ids": cleanStringList(options.VectorStoreIDs),
		}
		if options.MaxFileSearchResults > 0 {
			fileSearchTool["max_num_results"] = options.MaxFileSearchResults
		}
		reqBody.Tools = []map[string]interface{}{fileSearchTool}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return TextResult{}, fmt.Errorf("marshal error: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.openai.com/v1/responses",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return TextResult{}, fmt.Errorf("request error: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return TextResult{}, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return TextResult{}, fmt.Errorf("openai error (%d): %s", resp.StatusCode, string(raw))
	}

	var parsed responsesResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return TextResult{}, fmt.Errorf("json decode error: %w | body: %s", err, string(raw))
	}

	text := firstResponseText(parsed)
	if text == "" {
		return TextResult{}, fmt.Errorf("empty model output")
	}

	return TextResult{
		Text:       text,
		ResponseID: parsed.ID,
		Usage:      parsed.Usage,
	}, nil
}

func (c *OpenAIClient) UploadFile(ctx context.Context, filename string, purpose string, file io.Reader) (OpenAIFile, error) {
	httpClient, err := c.newHTTPClient()
	if err != nil {
		return OpenAIFile{}, fmt.Errorf("proxy init error: %w", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fileWriter, err := writer.CreateFormFile("file", safeAudioFilename(filename))
	if err != nil {
		return OpenAIFile{}, fmt.Errorf("multipart file error: %w", err)
	}
	if _, err := io.Copy(fileWriter, file); err != nil {
		return OpenAIFile{}, fmt.Errorf("file copy error: %w", err)
	}
	if strings.TrimSpace(purpose) == "" {
		purpose = "assistants"
	}
	_ = writer.WriteField("purpose", purpose)
	if err := writer.Close(); err != nil {
		return OpenAIFile{}, fmt.Errorf("multipart close error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/files", body)
	if err != nil {
		return OpenAIFile{}, fmt.Errorf("request error: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return OpenAIFile{}, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return OpenAIFile{}, fmt.Errorf("openai file upload error (%d): %s", resp.StatusCode, string(raw))
	}

	var parsed OpenAIFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return OpenAIFile{}, fmt.Errorf("file json decode error: %w | body: %s", err, string(raw))
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return OpenAIFile{}, fmt.Errorf("empty uploaded file id")
	}
	return parsed, nil
}

func (c *OpenAIClient) CreateVectorStore(ctx context.Context, name string) (OpenAIVectorStore, error) {
	payload := map[string]string{"name": strings.TrimSpace(name)}
	var parsed OpenAIVectorStore
	if err := c.doJSON(ctx, http.MethodPost, "https://api.openai.com/v1/vector_stores", payload, &parsed); err != nil {
		return OpenAIVectorStore{}, err
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return OpenAIVectorStore{}, fmt.Errorf("empty vector store id")
	}
	return parsed, nil
}

func (c *OpenAIClient) AddFileToVectorStore(ctx context.Context, vectorStoreID string, fileID string) (OpenAIVectorStoreFile, error) {
	endpoint := "https://api.openai.com/v1/vector_stores/" + url.PathEscape(vectorStoreID) + "/files"
	payload := map[string]string{"file_id": strings.TrimSpace(fileID)}
	var parsed OpenAIVectorStoreFile
	if err := c.doJSON(ctx, http.MethodPost, endpoint, payload, &parsed); err != nil {
		return OpenAIVectorStoreFile{}, err
	}
	return parsed, nil
}

func (c *OpenAIClient) ListVectorStoreFiles(ctx context.Context, vectorStoreID string) ([]OpenAIVectorStoreFile, error) {
	endpoint := "https://api.openai.com/v1/vector_stores/" + url.PathEscape(vectorStoreID) + "/files"
	var parsed struct {
		Data []OpenAIVectorStoreFile `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &parsed); err != nil {
		return nil, err
	}
	return parsed.Data, nil
}

func (c *OpenAIClient) WaitVectorStoreFileReady(ctx context.Context, vectorStoreID string, vectorStoreFileID string, fileID string, timeout time.Duration) (OpenAIVectorStoreFile, error) {
	deadline := time.Now().Add(timeout)
	for {
		files, err := c.ListVectorStoreFiles(ctx, vectorStoreID)
		if err != nil {
			return OpenAIVectorStoreFile{}, err
		}
		for _, item := range files {
			if item.ID != vectorStoreFileID && item.ID != fileID && item.FileID != fileID {
				continue
			}
			switch item.Status {
			case "completed":
				return item, nil
			case "failed", "cancelled", "expired":
				if strings.TrimSpace(item.LastError.Message) != "" {
					return item, fmt.Errorf("vector store file %s: %s", item.Status, item.LastError.Message)
				}
				return item, fmt.Errorf("vector store file %s", item.Status)
			}
			if time.Now().After(deadline) {
				return item, nil
			}
		}
		if time.Now().After(deadline) {
			return OpenAIVectorStoreFile{ID: vectorStoreFileID, FileID: fileID, VectorStoreID: vectorStoreID, Status: "processing"}, nil
		}
		select {
		case <-ctx.Done():
			return OpenAIVectorStoreFile{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *OpenAIClient) doJSON(ctx context.Context, method string, endpoint string, payload any, out any) error {
	httpClient, err := c.newHTTPClient()
	if err != nil {
		return fmt.Errorf("proxy init error: %w", err)
	}

	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal error: %w", err)
		}
		body = bytes.NewBuffer(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("openai error (%d): %s", resp.StatusCode, string(raw))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("json decode error: %w | body: %s", err, string(raw))
	}
	return nil
}

func cleanStringList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func firstResponseText(parsed responsesResponse) string {
	for _, output := range parsed.Output {
		for _, content := range output.Content {
			text := strings.TrimSpace(content.Text)
			if text != "" {
				return text
			}
		}
	}
	return ""
}
