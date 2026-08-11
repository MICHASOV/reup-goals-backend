package ai

import "testing"

func TestOpenAIClientUsesConfiguredGatewayBaseURL(t *testing.T) {
	client := New("gateway-secret", "model", "direct").WithBaseURL("https://ai.example.com/openai/v1/")
	if got := client.endpoint("responses"); got != "https://ai.example.com/openai/v1/responses" {
		t.Fatalf("unexpected gateway endpoint: %s", got)
	}
}

func TestOpenAIClientDefaultsToOfficialAPI(t *testing.T) {
	client := New("openai-key", "model", "direct")
	if got := client.endpoint("files/file_1"); got != "https://api.openai.com/v1/files/file_1" {
		t.Fatalf("unexpected default endpoint: %s", got)
	}
}
