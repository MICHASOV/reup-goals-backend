package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
)

type OpenAIClient struct {
	APIKey      string
	AssistantID string
}

func New(apiKey, assistantID string) *OpenAIClient {
	return &OpenAIClient{
		APIKey:      apiKey,
		AssistantID: assistantID,
	}
}

func (c *OpenAIClient) EvaluateTask(ctx context.Context, input map[string]interface{}) (json.RawMessage, error) {

	// -------------------------------
	// 1. Create Thread
	// -------------------------------
	threadBody := map[string]interface{}{}
	threadJSON, _ := json.Marshal(threadBody)

	reqThread, _ := http.NewRequest(
		"POST",
		"https://api.openai.com/v1/threads",
		bytes.NewBuffer(threadJSON),
	)
	reqThread.Header.Set("Authorization", "Bearer "+c.APIKey)
	reqThread.Header.Set("Content-Type", "application/json")

	resThread, err := http.DefaultClient.Do(reqThread)
	if err != nil {
		return nil, err
	}
	defer resThread.Body.Close()

	var threadResp struct {
		ID string `json:"id"`
	}
	bodyThread, _ := ioutil.ReadAll(resThread.Body)
	json.Unmarshal(bodyThread, &threadResp)

	// -------------------------------
	// 2. Add message to thread
	// -------------------------------
	messageBody := map[string]interface{}{
		"role": "user",
		"content": []map[string]interface{}{
			{
				"type": "input_text",
				"text": mustJSON(input), // send JSON as plain text inside request
			},
		},
	}

	msgJSON, _ := json.Marshal(messageBody)

	reqMsg, _ := http.NewRequest(
		"POST",
		fmt.Sprintf("https://api.openai.com/v1/threads/%s/messages", threadResp.ID),
		bytes.NewBuffer(msgJSON),
	)
	reqMsg.Header.Set("Authorization", "Bearer "+c.APIKey)
	reqMsg.Header.Set("Content-Type", "application/json")

	_, err = http.DefaultClient.Do(reqMsg)
	if err != nil {
		return nil, err
	}

	// -------------------------------
	// 3. Run the assistant
	// -------------------------------
	runBody := map[string]interface{}{
		"assistant_id": c.AssistantID,
	}

	runJSON, _ := json.Marshal(runBody)

	reqRun, _ := http.NewRequest(
		"POST",
		fmt.Sprintf("https://api.openai.com/v1/threads/%s/runs", threadResp.ID),
		bytes.NewBuffer(runJSON),
	)
	reqRun.Header.Set("Authorization", "Bearer "+c.APIKey)
	reqRun.Header.Set("Content-Type", "application/json")

	runResp, err := http.DefaultClient.Do(reqRun)
	if err != nil {
		return nil, err
	}
	defer runResp.Body.Close()

	var runInfo struct {
		ID string `json:"id"`
	}
	bodyRun, _ := ioutil.ReadAll(runResp.Body)
	json.Unmarshal(bodyRun, &runInfo)

	// -------------------------------
	// 4. Poll result
	// -------------------------------
	for {
		time.Sleep(700 * time.Millisecond)

		reqCheck, _ := http.NewRequest(
			"GET",
			fmt.Sprintf("https://api.openai.com/v1/threads/%s/runs/%s", threadResp.ID, runInfo.ID),
			nil,
		)
		reqCheck.Header.Set("Authorization", "Bearer "+c.APIKey)

		checkResp, _ := http.DefaultClient.Do(reqCheck)
		buf, _ := ioutil.ReadAll(checkResp.Body)
		checkResp.Body.Close()

		var check struct {
			Status string `json:"status"`
		}
		json.Unmarshal(buf, &check)

		if check.Status == "completed" {
			break
		}
	}

	// -------------------------------
	// 5. Fetch messages
	// -------------------------------
	reqMsgs, _ := http.NewRequest(
		"GET",
		fmt.Sprintf("https://api.openai.com/v1/threads/%s/messages", threadResp.ID),
		nil,
	)
	reqMsgs.Header.Set("Authorization", "Bearer "+c.APIKey)

	msgsResp, err := http.DefaultClient.Do(reqMsgs)
	if err != nil {
		return nil, err
	}
	defer msgsResp.Body.Close()

	msgsBody, _ := ioutil.ReadAll(msgsResp.Body)

	// Extract assistant text
	var msgs struct {
		Data []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"data"`
	}
	json.Unmarshal(msgsBody, &msgs)

	for _, m := range msgs.Data {
		if m.Role == "assistant" && len(m.Content) > 0 {
			return json.RawMessage(m.Content[0].Text), nil
		}
	}

	return nil, fmt.Errorf("assistant did not return text")
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
