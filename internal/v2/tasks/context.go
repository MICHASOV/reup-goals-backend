package tasks

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"

	"reup-goals-backend/internal/v2/strategicmemory"
)

type taskContextBuilder struct {
	store  *Store
	memory *strategicmemory.Store
}

func newTaskContextBuilder(dbx *sql.DB) *taskContextBuilder {
	return &taskContextBuilder{store: NewStore(dbx), memory: strategicmemory.NewStore(dbx)}
}

func (b *taskContextBuilder) Build(
	ctx context.Context,
	workspaceID int,
	workstreamID int,
	historyLimit int,
) (taskContextPack, []string, string, error) {
	state, err := b.store.Workstream(ctx, workspaceID, workstreamID)
	if err != nil {
		return taskContextPack{}, nil, "", err
	}
	if state.Workstream == nil || state.Course == nil || state.TacticalPlan == nil {
		return taskContextPack{}, nil, "", ErrForbidden
	}

	snapshot, err := b.memory.LatestSnapshot(ctx, workspaceID)
	if err != nil {
		return taskContextPack{}, nil, "", err
	}
	communication, err := b.memory.CommunicationProfile(ctx, workspaceID)
	if err != nil {
		return taskContextPack{}, nil, "", err
	}
	businessDocuments := []compactContextDocument{}
	if snapshot == nil {
		documents, documentsErr := b.memory.ListDocuments(ctx, workspaceID)
		if documentsErr != nil {
			return taskContextPack{}, nil, "", documentsErr
		}
		for _, document := range documents {
			businessDocuments = append(businessDocuments, compactContextDocument{
				Type: document.DocumentType, Title: document.Title, Content: truncateRunes(document.Markdown, 900),
			})
		}
	}

	strategySummary, strategyDocuments, err := b.strategyContext(ctx, workspaceID, state.TacticalPlan.StrategyID)
	if err != nil {
		return taskContextPack{}, nil, "", err
	}
	messages := []BrainstormMessage{}
	if historyLimit > 0 {
		messages, err = b.store.BrainstormMessages(ctx, workspaceID, workstreamID, historyLimit)
		if err != nil {
			return taskContextPack{}, nil, "", err
		}
	}
	workstream := *state.Workstream
	workstream.TopTasks = nil
	workstream.Projects = nil
	workstream.Risks = nil
	workstream.Opportunities = nil
	creationOptions, err := b.creationOptions(ctx, workspaceID)
	if err != nil {
		return taskContextPack{}, nil, "", err
	}

	pack := taskContextPack{
		BusinessDocuments: businessDocuments,
		StrategySummary:   strategySummary,
		StrategyDocuments: strategyDocuments,
		Course:            state.Course,
		TacticalPlan:      state.TacticalPlan,
		Workstream:        &workstream,
		Projects:          state.Projects,
		Risks:             state.Risks,
		Opportunities:     state.Opportunities,
		ExistingTasks:     compactTasks(state.Tasks),
		CreationOptions:   creationOptions,
		Communication:     communication,
		RecentMessages:    messages,
	}
	if snapshot != nil {
		pack.BusinessSnapshot = snapshot.Snapshot
		pack.BusinessStage = snapshot.BusinessStage
	}

	files, err := b.memory.ListFiles(ctx, workspaceID)
	if err != nil {
		return taskContextPack{}, nil, "", err
	}
	vectorStoreIDs := uniqueVectorStoreIDs(files)
	fingerprintPack := pack
	fingerprintPack.RecentMessages = nil
	raw, _ := json.Marshal(fingerprintPack)
	hash := sha256.Sum256(raw)
	return pack, vectorStoreIDs, hex.EncodeToString(hash[:]), nil
}

func (b *taskContextBuilder) creationOptions(ctx context.Context, workspaceID int) (taskCreationOptions, error) {
	result := taskCreationOptions{
		Departments: []taskDepartmentOption{},
		Members:     []taskMemberOption{},
	}
	departmentRows, err := b.store.dbx.QueryContext(ctx, `
		SELECT id, name
		FROM v2_departments
		WHERE workspace_id=$1 AND archived_at IS NULL AND status='active'
		ORDER BY sort_order, lower(name), id
	`, workspaceID)
	if err != nil {
		return result, err
	}
	defer departmentRows.Close()
	for departmentRows.Next() {
		var item taskDepartmentOption
		if err := departmentRows.Scan(&item.ID, &item.Name); err != nil {
			return result, err
		}
		item.Name = strings.TrimSpace(item.Name)
		result.Departments = append(result.Departments, item)
	}
	if err := departmentRows.Err(); err != nil {
		return result, err
	}

	memberRows, err := b.store.dbx.QueryContext(ctx, `
		SELECT users.id, COALESCE(users.name, ''), users.email, COALESCE(users.company_role, '')
		FROM workspace_memberships membership
		JOIN users ON users.id=membership.user_id
		WHERE membership.workspace_id=$1 AND membership.status='active'
		ORDER BY CASE WHEN membership.role='owner' THEN 0 ELSE 1 END,
			lower(COALESCE(NULLIF(users.name, ''), users.email)), users.id
	`, workspaceID)
	if err != nil {
		return result, err
	}
	defer memberRows.Close()
	for memberRows.Next() {
		var item taskMemberOption
		if err := memberRows.Scan(&item.UserID, &item.Name, &item.Email, &item.CompanyRole); err != nil {
			return result, err
		}
		item.Name = strings.TrimSpace(item.Name)
		item.Email = strings.TrimSpace(item.Email)
		item.CompanyRole = strings.TrimSpace(item.CompanyRole)
		result.Members = append(result.Members, item)
	}
	return result, memberRows.Err()
}

func (b *taskContextBuilder) strategyContext(ctx context.Context, workspaceID int, strategyID int) (string, []compactContextDocument, error) {
	var summary string
	err := b.store.dbx.QueryRowContext(ctx, `
		SELECT summary
		FROM v2_strategies
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, strategyID, workspaceID).Scan(&summary)
	if err != nil && err != sql.ErrNoRows {
		return "", nil, err
	}

	rows, err := b.store.dbx.QueryContext(ctx, `
		SELECT document.document_type,
			COALESCE(NULLIF(document.display_title, ''), document.title),
			COALESCE(NULLIF(document.formatted_markdown, ''), document.content_json::TEXT)
		FROM v2_strategy_synthesis_documents document
		JOIN v2_strategy_synthesis_runs run ON run.id=document.run_id
		WHERE document.workspace_id=$1 AND run.strategy_id=$2 AND run.status='completed'
			AND run.id=(
				SELECT id FROM v2_strategy_synthesis_runs
				WHERE workspace_id=$1 AND strategy_id=$2 AND status='completed'
				ORDER BY created_at DESC, id DESC LIMIT 1
			)
		ORDER BY document.sort_order ASC, document.id ASC
	`, workspaceID, strategyID)
	if err != nil {
		return "", nil, err
	}
	documents := []compactContextDocument{}
	for rows.Next() {
		var item compactContextDocument
		if err := rows.Scan(&item.Type, &item.Title, &item.Content); err != nil {
			_ = rows.Close()
			return "", nil, err
		}
		item.Content = truncateRunes(item.Content, 1200)
		documents = append(documents, item)
	}
	if err := rows.Close(); err != nil {
		return "", nil, err
	}
	if len(documents) > 0 {
		return strings.TrimSpace(summary), documents, nil
	}

	fallbackRows, err := b.store.dbx.QueryContext(ctx, `
		SELECT type, title, content
		FROM v2_strategy_artifacts
		WHERE workspace_id=$1 AND strategy_id=$2
		ORDER BY sort_order ASC, id ASC
	`, workspaceID, strategyID)
	if err != nil {
		return "", nil, err
	}
	defer fallbackRows.Close()
	for fallbackRows.Next() {
		var item compactContextDocument
		if err := fallbackRows.Scan(&item.Type, &item.Title, &item.Content); err != nil {
			return "", nil, err
		}
		item.Content = truncateRunes(item.Content, 1200)
		documents = append(documents, item)
	}
	return strings.TrimSpace(summary), documents, fallbackRows.Err()
}

func compactTasks(tasks []Task) []taskContextItem {
	items := make([]taskContextItem, 0, len(tasks))
	for _, task := range tasks {
		recommendation := ""
		if task.Evaluation != nil {
			recommendation = task.Evaluation.Recommendation
		}
		items = append(items, taskContextItem{
			ID: task.ID, ProjectID: task.ProjectID, RiskID: task.RiskID, OpportunityID: task.OpportunityID,
			Title: task.Title, Description: task.Description, ExpectedResult: task.ExpectedResult,
			SuccessCriteria: task.SuccessCriteria, Status: task.Status,
			EffectivePriorityScore: task.EffectivePriorityScore, EffectivePriorityTier: task.EffectivePriorityTier,
			Recommendation: recommendation, Flags: task.Flags, BacklogCategory: task.BacklogCategory,
		})
	}
	return items
}

func uniqueVectorStoreIDs(files []strategicmemory.StrategicFile) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, file := range files {
		id := strings.TrimSpace(file.VectorStoreID)
		if id == "" || file.Status != "completed" || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
