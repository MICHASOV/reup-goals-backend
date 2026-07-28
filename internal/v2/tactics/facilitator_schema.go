package tactics

func tacticsResponseSchemaName(requiredDraftType string) string {
	if requiredDraftType == "" {
		return ""
	}
	return "tactics_" + requiredDraftType + "_proposal"
}

func tacticsFacilitatorOutputSchema(requiredDraftType string) map[string]any {
	if requiredDraftType == "" {
		return nil
	}

	draftProperties := map[string]any{
		"apply": map[string]any{
			"type":        "boolean",
			"description": "True shows this draft to the user for confirmation. It never applies automatically.",
		},
		"operation":   map[string]any{"type": "string", "enum": []string{"create"}},
		"entity_type": map[string]any{"type": "string", "enum": []string{requiredDraftType}},
		"entity_id":   map[string]any{"type": []string{"integer", "null"}},
		"draft_key":   map[string]any{"type": "string"},
		"parent_entity_type": map[string]any{
			"type": "string",
			"enum": []string{"", EntityPlan, EntityWorkstream, EntityProject},
		},
		"parent_entity_id": map[string]any{"type": []string{"integer", "null"}},
		"parent_draft_key": map[string]any{"type": "string"},
		"title":            map[string]any{"type": "string"},
		"description":      map[string]any{"type": "string"},
		"reason":           map[string]any{"type": "string"},
		"metric_name":      map[string]any{"type": "string"},
		"coverage_status":  map[string]any{"type": "string", "enum": []string{"", "uncovered", "partially_covered", "covered", "accepted", "ignored"}},
	}
	required := []string{
		"apply", "operation", "entity_type", "entity_id", "draft_key",
		"parent_entity_type", "parent_entity_id", "parent_draft_key",
		"title", "description", "reason", "metric_name", "coverage_status",
	}

	switch requiredDraftType {
	case EntityWorkstream:
		draftProperties["goal"] = map[string]any{"type": "string"}
		draftProperties["ckp"] = map[string]any{"type": "string"}
		draftProperties["closes_risk"] = map[string]any{"type": "string"}
		draftProperties["metric_current"] = map[string]any{"type": "string"}
		draftProperties["metric_target"] = map[string]any{"type": "string"}
		draftProperties["metrics"] = metricListSchema()
		required = append(required, "goal", "ckp", "closes_risk", "metric_current", "metric_target", "metrics")
	case EntityProject:
		draftProperties["why_needed"] = map[string]any{"type": "string"}
		draftProperties["success_criteria"] = map[string]any{"type": "string"}
		draftProperties["failure_criteria"] = map[string]any{"type": "string"}
		draftProperties["expected_value"] = map[string]any{"type": "string"}
		required = append(required, "why_needed", "success_criteria", "failure_criteria", "expected_value")
	case EntityRisk:
		draftProperties["severity"] = map[string]any{"type": "string", "enum": []string{"", "low", "medium", "high", "critical"}}
		draftProperties["probability"] = map[string]any{"type": "string", "enum": []string{"", "low", "medium", "high"}}
		draftProperties["probability_value"] = map[string]any{"type": []string{"integer", "null"}}
		draftProperties["impact_score"] = map[string]any{"type": []string{"integer", "null"}}
		draftProperties["mitigation_plan"] = map[string]any{"type": "string"}
		required = append(required, "severity", "probability", "probability_value", "impact_score", "mitigation_plan")
	case EntityHypothesis:
		draftProperties["statement"] = map[string]any{"type": "string"}
		draftProperties["expected_effect"] = map[string]any{"type": "string"}
		draftProperties["test_method"] = map[string]any{"type": "string"}
		draftProperties["hypothesis_status"] = map[string]any{"type": "string", "enum": []string{"draft", "ready", "testing", "confirmed", "disproved", "inconclusive"}}
		required = append(required, "statement", "expected_effect", "test_method", "hypothesis_status")
	}

	draftSchema := map[string]any{
		"type":                 "object",
		"properties":           draftProperties,
		"required":             required,
		"additionalProperties": false,
	}

	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message":        map[string]any{"type": "string"},
			"session_status": map[string]any{"type": "string", "enum": []string{"in_progress", "candidate_ready", "blocked"}},
			"status_reason":  map[string]any{"type": "string"},
			"current_focus": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"entity_type":   map[string]any{"type": "string", "enum": []string{"tactical_plan", "workstream", "project", "risk", "hypothesis", "open_question"}},
					"entity_id":     map[string]any{"type": []string{"integer", "null"}},
					"title":         map[string]any{"type": "string"},
					"research_goal": map[string]any{"type": "string"},
				},
				"required":             []string{"entity_type", "entity_id", "title", "research_goal"},
				"additionalProperties": false,
			},
			"decisions_detected":     stringListSchema(12),
			"open_questions":         stringListSchema(20),
			"needs_strategy_review":  map[string]any{"type": "boolean"},
			"strategy_review_reason": map[string]any{"type": "string"},
			"draft_changes": map[string]any{
				"type":     "array",
				"items":    draftSchema,
				"minItems": 1,
				"maxItems": maxDraftChangesPerTurn,
			},
		},
		"required": []string{
			"message", "session_status", "status_reason", "current_focus",
			"decisions_detected", "open_questions", "needs_strategy_review",
			"strategy_review_reason", "draft_changes",
		},
		"additionalProperties": false,
	}
}

func stringListSchema(maxItems int) map[string]any {
	return map[string]any{
		"type":     "array",
		"items":    map[string]any{"type": "string"},
		"maxItems": maxItems,
	}
}

func metricListSchema() map[string]any {
	return map[string]any{
		"type":     "array",
		"maxItems": 3,
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":    map[string]any{"type": "string"},
				"current": map[string]any{"type": "string"},
				"target":  map[string]any{"type": "string"},
			},
			"required":             []string{"name", "current", "target"},
			"additionalProperties": false,
		},
	}
}
