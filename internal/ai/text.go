package ai

import (
	"encoding/json"
	"strings"
)

// LooksLikeJSONObject catches a structured payload accidentally serialized
// into a user-visible message field.
func LooksLikeJSONObject(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value[0] != '{' || value[len(value)-1] != '}' {
		return false
	}
	var object map[string]any
	return json.Unmarshal([]byte(value), &object) == nil && len(object) > 0
}
