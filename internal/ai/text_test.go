package ai

import "testing"

func TestLooksLikeJSONObject(t *testing.T) {
	if !LooksLikeJSONObject(`{"next_question":"Что проверяем?"}`) {
		t.Fatal("serialized object must be detected")
	}
	if LooksLikeJSONObject("Проверим {экономику} этого выбора.") {
		t.Fatal("natural prose must not be rejected")
	}
}
