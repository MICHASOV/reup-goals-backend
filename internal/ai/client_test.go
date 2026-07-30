package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildResponsesRequestUsesConversationWithoutDuplicatingPrompt(t *testing.T) {
	resolved := ResolvedCall{Model: "gpt-5.4", Instructions: "stable business prompt"}
	request := buildResponsesRequest(resolved, "latest user message", nil, ResponseContextOptions{
		PreviousResponseID:   "resp_old",
		UseConversation:      true,
		VectorStoreIDs:       []string{"", "vs_workspace", "vs_workspace"},
		CompactThreshold:     120000,
		MaxFileSearchResults: 8,
	}, 4000, " conv_workspace ")

	if request.Conversation != "conv_workspace" {
		t.Fatalf("conversation = %q", request.Conversation)
	}
	if request.Instructions != "" {
		t.Fatalf("instructions must not be repeated for a conversation: %q", request.Instructions)
	}
	if request.PreviousResponseID != "" {
		t.Fatalf("previous_response_id must not be combined with conversation: %q", request.PreviousResponseID)
	}
	if !request.Background {
		t.Fatal("stateful GPT-5 conversations must run in background mode")
	}
	if len(request.ContextManagement) != 1 {
		t.Fatalf("expected server-side compaction configuration, got %#v", request.ContextManagement)
	}
	if len(request.Tools) != 1 {
		t.Fatalf("expected one file_search tool, got %#v", request.Tools)
	}
	vectorStoreIDs, ok := request.Tools[0]["vector_store_ids"].([]string)
	if !ok || len(vectorStoreIDs) != 1 || vectorStoreIDs[0] != "vs_workspace" {
		t.Fatalf("unexpected vector stores: %#v", request.Tools[0]["vector_store_ids"])
	}

	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "stable business prompt") || strings.Contains(string(raw), "resp_old") {
		t.Fatalf("conversation turn repeats stable context: %s", raw)
	}
}

func TestBuildResponsesRequestKeepsInstructionsForOneShotCall(t *testing.T) {
	resolved := ResolvedCall{Model: "gpt-test", Instructions: "one-shot prompt"}
	request := buildResponsesRequest(resolved, "target delta", nil, ResponseContextOptions{
		PreviousResponseID: "resp_previous",
		ReasoningEffort:    "none",
	}, 4000, "")

	if request.Conversation != "" {
		t.Fatalf("unexpected conversation: %q", request.Conversation)
	}
	if request.Instructions != resolved.Instructions {
		t.Fatalf("instructions = %q", request.Instructions)
	}
	if request.PreviousResponseID != "resp_previous" {
		t.Fatalf("previous_response_id = %q", request.PreviousResponseID)
	}
	if request.Reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %#v", request.Reasoning)
	}
	if request.Background {
		t.Fatal("one-shot call must remain synchronous")
	}
}

func TestBuildResponsesRequestCapsPromptCacheKey(t *testing.T) {
	longKey := "reupgoals-" + strings.Repeat("длинный-ключ-", 8)
	first := buildResponsesRequest(ResolvedCall{Model: "gpt-test"}, "input", nil, ResponseContextOptions{
		PromptCacheKey: longKey,
	}, 4000, "")
	second := buildResponsesRequest(ResolvedCall{Model: "gpt-test"}, "input", nil, ResponseContextOptions{
		PromptCacheKey: longKey,
	}, 4000, "")

	if len(first.PromptCacheKey) != 64 {
		t.Fatalf("prompt cache key length = %d, want 64", len(first.PromptCacheKey))
	}
	if first.PromptCacheKey != second.PromptCacheKey {
		t.Fatalf("prompt cache key must be deterministic: %q != %q", first.PromptCacheKey, second.PromptCacheKey)
	}
	if first.PromptCacheKey == strings.TrimSpace(longKey) {
		t.Fatal("overlong prompt cache key must be normalized")
	}
}

func TestBuildConversationRequestPinsResolvedPrompt(t *testing.T) {
	resolved := ResolvedCall{
		Instructions: "resolved prompt from registry",
		Metadata: CallMetadata{
			Module:        "strategy_facilitator",
			PromptName:    "strategy_facilitator",
			PromptVersion: "v7",
		},
	}
	request := buildConversationRequest(resolved)

	if len(request.Items) != 1 {
		t.Fatalf("items = %#v", request.Items)
	}
	item := request.Items[0]
	if item.Type != "message" || item.Role != "developer" || item.Content != resolved.Instructions {
		t.Fatalf("unexpected pinned prompt item: %#v", item)
	}
	if request.Metadata["prompt_version"] != "v7" {
		t.Fatalf("metadata = %#v", request.Metadata)
	}
}

func TestIsConversationStateError(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want bool
	}{
		{name: "missing conversation", err: "openai error (404): conversation conv_old not found", want: true},
		{name: "invalid conversation", err: "openai error (400): invalid conversation identifier", want: true},
		{name: "quota", err: "openai error (429): insufficient_quota", want: false},
		{name: "server", err: "openai error (500): internal server error", want: false},
		{name: "unrelated not found", err: "openai error (404): file not found", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsConversationStateError(assertionError(test.err)); got != test.want {
				t.Fatalf("IsConversationStateError(%q) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestResponseRequestContextHasNoImplicitDeadline(t *testing.T) {
	ctx, cancel := responseRequestContext(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("default response context must not have an artificial deadline")
	}

	ctx, cancel = responseRequestContext(context.Background(), time.Second)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("explicit request timeout must create a deadline")
	}
}

func TestConversationItemsBeforeCompactionPreserveDeveloperPrompt(t *testing.T) {
	items := conversationItemList{}
	items.Data = append(items.Data,
		struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Role string `json:"role"`
		}{ID: "developer", Type: "message", Role: "developer"},
		struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Role string `json:"role"`
		}{ID: "user-old", Type: "message", Role: "user"},
		struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Role string `json:"role"`
		}{ID: "assistant-old", Type: "message", Role: "assistant"},
		struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Role string `json:"role"`
		}{ID: "compact", Type: "compaction"},
		struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Role string `json:"role"`
		}{ID: "assistant-new", Type: "message", Role: "assistant"},
	)

	got := conversationItemIDsBeforeCompaction(items, "compact")
	if len(got) != 2 || got[0] != "user-old" || got[1] != "assistant-old" {
		t.Fatalf("items to prune = %#v", got)
	}
}

type assertionError string

func (e assertionError) Error() string { return string(e) }
