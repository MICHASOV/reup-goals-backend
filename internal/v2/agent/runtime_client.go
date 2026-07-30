package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type RuntimeClient struct {
	baseURL string
	secret  string
	client  *http.Client
}

func NewRuntimeClient(baseURL string, secret string) *RuntimeClient {
	return &RuntimeClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		client: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 0,
			IdleConnTimeout:       90 * time.Second,
		}},
	}
}

func (c *RuntimeClient) Execute(ctx context.Context, payload any) (RuntimeResult, error) {
	return c.call(ctx, "/v1/runs/execute", payload)
}

func (c *RuntimeClient) Resume(ctx context.Context, payload any) (RuntimeResult, error) {
	return c.call(ctx, "/v1/runs/resume", payload)
}

func (c *RuntimeClient) call(ctx context.Context, path string, payload any) (RuntimeResult, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return RuntimeResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return RuntimeResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.secret)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return RuntimeResult{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return RuntimeResult{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return RuntimeResult{}, fmt.Errorf("agent_runtime_http_%d:%s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var result RuntimeResult
	if err := json.Unmarshal(body, &result); err != nil {
		return RuntimeResult{}, fmt.Errorf("decode agent runtime: %w", err)
	}
	return result, nil
}
