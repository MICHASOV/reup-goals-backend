package workspacedocs

import "testing"

func TestValidDocument(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		content string
		status  string
		valid   bool
	}{
		{name: "draft", title: "Sales playbook", status: "draft", valid: true},
		{name: "published", title: "Operating model", status: "published", valid: true},
		{name: "empty title", title: "", status: "draft", valid: false},
		{name: "unknown status", title: "Document", status: "ready", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validDocument(test.title, test.content, test.status); got != test.valid {
				t.Fatalf("validDocument() = %v, want %v", got, test.valid)
			}
		})
	}
}

func TestNormalizedIDs(t *testing.T) {
	input := []int{4, 0, 2, 4, -1, 2, 8}
	got := normalizedIDs(&input)
	want := []int{4, 2, 8}
	if len(got) != len(want) {
		t.Fatalf("normalizedIDs() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("normalizedIDs() = %v, want %v", got, want)
		}
	}
}

func TestDocumentIDSuffix(t *testing.T) {
	tests := []struct {
		path string
		id   int64
		ok   bool
	}{
		{path: "/api/v2/workspace-documents/42", id: 42, ok: true},
		{path: "/api/v2/workspace-documents/42/", id: 42, ok: true},
		{path: "/api/v2/workspace-documents", ok: false},
		{path: "/api/v2/workspace-documents/nope", ok: false},
		{path: "/api/v2/workspace-documents/1/history", ok: false},
	}
	for _, test := range tests {
		gotID, gotOK := documentIDSuffix(test.path)
		if gotID != test.id || gotOK != test.ok {
			t.Fatalf("documentIDSuffix(%q) = (%d, %v), want (%d, %v)", test.path, gotID, gotOK, test.id, test.ok)
		}
	}
}
