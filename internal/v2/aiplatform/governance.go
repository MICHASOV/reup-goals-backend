package aiplatform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/operations"
)

type Limits struct {
	RequestsPerMinute int
	DailyBudgetUSD    float64
	MonthlyBudgetUSD  float64
}

type Governance struct {
	dbx      *sql.DB
	defaults Limits
}

func NewGovernance(dbx *sql.DB, defaults Limits) *Governance {
	return &Governance{dbx: dbx, defaults: defaults}
}

func (g *Governance) BeforeCall(ctx context.Context, metadata ai.CallMetadata, fallbackInstructions string, fallbackModel string) (ai.ResolvedCall, error) {
	resolved := ai.ResolvedCall{
		Metadata: metadata, Instructions: fallbackInstructions, Model: fallbackModel, Provider: "openai",
	}
	if resolved.Metadata.PromptName == "" {
		resolved.Metadata.PromptName = resolved.Metadata.Module
	}
	if resolved.Metadata.PromptVersion == "" {
		resolved.Metadata.PromptVersion = "code_fallback"
	}
	if resolved.Metadata.PromptName != "" {
		g.registerFallback(ctx, resolved.Metadata, fallbackInstructions, fallbackModel)
		var template, model, version, provider string
		err := g.dbx.QueryRowContext(ctx, `
			SELECT template, model, prompt_version, provider
			FROM v2_ai_prompt_configs
			WHERE prompt_name=$1 AND status='active'
			LIMIT 1
		`, resolved.Metadata.PromptName).Scan(&template, &model, &version, &provider)
		if err == nil {
			if strings.TrimSpace(template) != "" {
				resolved.Instructions = template
			}
			if strings.TrimSpace(model) != "" {
				resolved.Model = model
			}
			if strings.TrimSpace(provider) != "" {
				resolved.Provider = provider
			}
			resolved.Metadata.PromptVersion = version
		} else if !errors.Is(err, sql.ErrNoRows) {
			return ai.ResolvedCall{}, err
		}
	}
	if resolved.Provider != "openai" {
		return ai.ResolvedCall{}, fmt.Errorf("ai_provider_not_configured:%s", resolved.Provider)
	}
	if err := g.checkLimits(ctx, metadata.WorkspaceID); err != nil {
		g.logRejected(ctx, resolved, err)
		return ai.ResolvedCall{}, err
	}
	return resolved, nil
}

func (g *Governance) AfterCall(ctx context.Context, call ai.ResolvedCall, result ai.CallResult) {
	status := "success"
	errorText := ""
	if result.Err != nil {
		status = "failed"
		errorText = result.Err.Error()
	}
	cost := estimateCost(call.Model, result.Usage)
	_, err := g.dbx.ExecContext(ctx, `
		INSERT INTO v2_ai_call_logs (
			workspace_id, user_id, ai_module, prompt_name, prompt_version, provider, model,
			status, error, latency_ms, token_usage_input, token_usage_output, token_usage_total,
			cached_input_tokens, estimated_cost, request_id, response_id
		)
		VALUES (
			NULLIF($1, 0), NULLIF($2, 0), $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		)
	`, call.Metadata.WorkspaceID, call.Metadata.UserID, call.Metadata.Module, call.Metadata.PromptName,
		call.Metadata.PromptVersion, call.Provider, call.Model, status, truncate(errorText, 4000),
		result.LatencyMS, result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.TotalTokens,
		result.Usage.CachedInputTokens(), cost, operations.RequestID(ctx), result.ResponseID)
	if err != nil {
		log.Printf("[ERROR] ai call log insert failed workspace_id=%d module=%s: %v", call.Metadata.WorkspaceID, call.Metadata.Module, err)
	}
}

func (g *Governance) registerFallback(ctx context.Context, metadata ai.CallMetadata, instructions string, model string) {
	_, _ = g.dbx.ExecContext(ctx, `
		INSERT INTO v2_ai_prompt_configs (
			prompt_name, prompt_version, model, template, status, provider, notes, updated_at
		)
		VALUES ($1, $2, $3, $4, 'draft', 'openai', 'Automatically registered code fallback.', NOW())
		ON CONFLICT (prompt_name, prompt_version) DO NOTHING
	`, metadata.PromptName, metadata.PromptVersion, model, instructions)
}

func (g *Governance) checkLimits(ctx context.Context, workspaceID int) error {
	if workspaceID <= 0 {
		return nil
	}
	limits := g.defaults
	var rpm int
	var daily, monthly float64
	err := g.dbx.QueryRowContext(ctx, `
		SELECT requests_per_minute, daily_budget_usd, monthly_budget_usd
		FROM v2_ai_usage_policies
		WHERE status='active' AND (workspace_id=$1 OR workspace_id IS NULL)
		ORDER BY (workspace_id=$1) DESC
		LIMIT 1
	`, workspaceID).Scan(&rpm, &daily, &monthly)
	if err == nil {
		limits = Limits{RequestsPerMinute: rpm, DailyBudgetUSD: daily, MonthlyBudgetUSD: monthly}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var calls int
	var dailyCost, monthlyCost float64
	err = g.dbx.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '1 minute'),
			COALESCE(SUM(estimated_cost) FILTER (WHERE created_at >= date_trunc('day', NOW())), 0),
			COALESCE(SUM(estimated_cost) FILTER (WHERE created_at >= date_trunc('month', NOW())), 0)
		FROM v2_ai_call_logs
		WHERE workspace_id=$1
	`, workspaceID).Scan(&calls, &dailyCost, &monthlyCost)
	if err != nil {
		return err
	}
	if limits.RequestsPerMinute > 0 && calls >= limits.RequestsPerMinute {
		return fmt.Errorf("ai_rate_limit_exceeded")
	}
	if limits.DailyBudgetUSD > 0 && dailyCost >= limits.DailyBudgetUSD {
		return fmt.Errorf("ai_daily_budget_exceeded")
	}
	if limits.MonthlyBudgetUSD > 0 && monthlyCost >= limits.MonthlyBudgetUSD {
		return fmt.Errorf("ai_monthly_budget_exceeded")
	}
	return nil
}

func (g *Governance) logRejected(ctx context.Context, call ai.ResolvedCall, callErr error) {
	_, _ = g.dbx.ExecContext(ctx, `
		INSERT INTO v2_ai_call_logs (
			workspace_id, user_id, ai_module, prompt_name, prompt_version, provider, model,
			status, error, request_id, latency_ms, token_usage_input, token_usage_output, token_usage_total, estimated_cost
		)
		VALUES (NULLIF($1, 0), NULLIF($2, 0), $3, $4, $5, $6, $7, 'rejected', $8, $9, 0, 0, 0, 0, 0)
	`, call.Metadata.WorkspaceID, call.Metadata.UserID, call.Metadata.Module, call.Metadata.PromptName,
		call.Metadata.PromptVersion, call.Provider, call.Model, truncate(callErr.Error(), 4000), operations.RequestID(ctx))
}

func estimateCost(model string, usage ai.Usage) float64 {
	return ai.EstimateCost(model, usage)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
