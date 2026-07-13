package strategy

import "testing"

func TestNormalizeSynthesisOutputUsesOnlyCatalogSourcesAndReturnsAllDocuments(t *testing.T) {
	sourceIndex := map[string]strategySynthesisSourceCatalogItem{
		"strategy_message:42": {
			Key:        "strategy_message:42",
			SourceType: "strategy_message",
			SourceID:   "42",
			Label:      "Сообщение участника",
			Href:       "/strategy?message=42",
		},
	}
	output := strategySynthesisModelOutput{
		Summary: "Сессия собрана",
		Documents: []strategySynthesisModelDocument{
			{
				DocumentType: "strategic_diagnosis",
				Title:        "Диагноз",
				Status:       "filled",
				ContentBlocks: []StrategySynthesisContentBlock{
					{
						Text:       "Компания ограничена нестабильной экономикой продаж.",
						SourceKeys: []string{"strategy_message:42", "strategy_message:999"},
						SourceNote: "Подтверждает ограничение",
					},
				},
			},
		},
	}

	documents := normalizeSynthesisOutput(7, 11, output, sourceIndex)
	if len(documents) != len(strategySynthesisDocumentDefinitions) {
		t.Fatalf("expected %d documents, got %d", len(strategySynthesisDocumentDefinitions), len(documents))
	}

	diagnosis := documents[0]
	if diagnosis.Status != SynthesisDocumentFilled {
		t.Fatalf("expected filled diagnosis, got %q", diagnosis.Status)
	}
	if len(diagnosis.ContentBlocks) != 1 {
		t.Fatalf("expected one content block, got %d", len(diagnosis.ContentBlocks))
	}
	if len(diagnosis.ContentBlocks[0].SourceKeys) != 1 || diagnosis.ContentBlocks[0].SourceKeys[0] != "strategy_message:42" {
		t.Fatalf("unexpected validated source keys: %#v", diagnosis.ContentBlocks[0].SourceKeys)
	}
	if len(diagnosis.SourceRefs) != 1 || diagnosis.SourceRefs[0].Href != "/strategy?message=42" {
		t.Fatalf("unexpected source refs: %#v", diagnosis.SourceRefs)
	}
	if documents[1].Status != SynthesisDocumentInsufficientData || len(documents[1].ContentBlocks) != 0 {
		t.Fatalf("missing document must remain empty: %#v", documents[1])
	}
}

func TestNormalizeSynthesisOutputDoesNotKeepFilledStatusWithoutContent(t *testing.T) {
	output := strategySynthesisModelOutput{
		Documents: []strategySynthesisModelDocument{
			{DocumentType: "key_challenge", Status: "filled"},
		},
	}
	documents := normalizeSynthesisOutput(1, 1, output, nil)
	if documents[1].Status != SynthesisDocumentInsufficientData {
		t.Fatalf("expected insufficient_data, got %q", documents[1].Status)
	}
}

func TestExtractSynthesisURLsKeepsOnlyValidUniqueHTTPLinks(t *testing.T) {
	urls := extractSynthesisURLs([]string{
		"Исследование: https://example.com/report.pdf. Дубль https://example.com/report.pdf",
		"Другая ссылка https://data.example.org/a?q=1, и не ссылка example.org",
	})
	if len(urls) != 2 {
		t.Fatalf("expected two URLs, got %#v", urls)
	}
	if urls[0] != "https://example.com/report.pdf" {
		t.Fatalf("unexpected first URL %q", urls[0])
	}
	if urls[1] != "https://data.example.org/a?q=1" {
		t.Fatalf("unexpected second URL %q", urls[1])
	}
}

func TestNormalizeFormattedSynthesisDocumentsKeepsValidatedSources(t *testing.T) {
	documents := []StrategySynthesisDocument{
		{
			DocumentType: "goals_and_metrics",
			Title:        "Цели и ключевые метрики",
			Status:       SynthesisDocumentFilled,
			ContentBlocks: []StrategySynthesisContentBlock{
				{Text: "Компания хочет выйти на 100 платящих клиентов за 90 дней."},
			},
			SourceRefs: []StrategySynthesisSourceRef{
				{Key: "strategy_message:42", Label: "Сообщение участника", Href: "/strategy?message=42"},
			},
			SortOrder: 5,
		},
	}
	output := strategyArtifactFormatterModelOutput{
		Artifacts: []strategyArtifactFormatterModelArtifact{
			{
				ArtifactKey:       "goals_and_metrics",
				DisplayTitle:      "100 платящих клиентов за 90 дней",
				FrameTitle:        "100 клиентов",
				FrameSubtitle:     "Главный ориентир ближайшего курса",
				PrimarySignal:     "100 платящих клиентов",
				Status:            "complete",
				FormattedDocument: "## 100 платящих клиентов за 90 дней\n\nЭто основной измеримый ориентир.",
				SourceRefs: []strategyArtifactFormatterSourceRef{
					{SourceKey: "strategy_message:42", Label: "Подтверждённое сообщение", Reason: "Подтверждает цель"},
					{SourceKey: "strategy_message:999", Label: "Лишнее", Reason: "Не должно попасть"},
				},
				OpenQuestions: []string{"Какой CAC допустим?"},
			},
		},
	}

	formatted := normalizeFormattedSynthesisDocuments(documents, output)
	if formatted[0].DisplayTitle != "100 платящих клиентов за 90 дней" {
		t.Fatalf("unexpected display title: %q", formatted[0].DisplayTitle)
	}
	if formatted[0].FrameTitle != "100 клиентов" || formatted[0].PrimarySignal != "100 платящих клиентов" {
		t.Fatalf("unexpected frame fields: %#v", formatted[0])
	}
	if formatted[0].VisualStatus != "complete" {
		t.Fatalf("unexpected visual status: %q", formatted[0].VisualStatus)
	}
	if len(formatted[0].SourceRefs) != 1 || formatted[0].SourceRefs[0].Key != "strategy_message:42" {
		t.Fatalf("unexpected source refs: %#v", formatted[0].SourceRefs)
	}
	if formatted[0].SourceRefs[0].Supports != "Подтверждает цель" {
		t.Fatalf("expected formatter source reason, got %q", formatted[0].SourceRefs[0].Supports)
	}
	if len(formatted[0].OpenQuestions) != 1 {
		t.Fatalf("unexpected open questions: %#v", formatted[0].OpenQuestions)
	}
}

func TestNormalizeFormattedSynthesisDocumentsFallsBackWhenArtifactMissing(t *testing.T) {
	documents := []StrategySynthesisDocument{
		{
			DocumentType: "key_challenge",
			Title:        "Ключевой вызов компании",
			Status:       SynthesisDocumentFilled,
			ContentBlocks: []StrategySynthesisContentBlock{
				{Text: "Главный вызов - перейти от роста выручки к прибыльной модели."},
			},
		},
	}

	formatted := normalizeFormattedSynthesisDocuments(documents, strategyArtifactFormatterModelOutput{})
	if formatted[0].FrameTitle == "" {
		t.Fatalf("expected fallback frame title")
	}
	if formatted[0].FormattedDocument == "" {
		t.Fatalf("expected fallback formatted document")
	}
	if formatted[0].VisualStatus != "complete" {
		t.Fatalf("unexpected visual status: %q", formatted[0].VisualStatus)
	}
}
