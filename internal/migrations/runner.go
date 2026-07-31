package migrations

import (
	"database/sql"
	"fmt"
)

type Migration struct {
	ID  string
	SQL string
}

var migrations = []Migration{
	{
		ID: "20260623_001_v2_workspaces",
		SQL: `
			CREATE TABLE IF NOT EXISTS workspaces (
				id SERIAL PRIMARY KEY,
				name TEXT NOT NULL,
				display_name TEXT NULL,
				owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				status TEXT NOT NULL DEFAULT 'active',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_workspaces_owner_user_id
				ON workspaces (owner_user_id);

			CREATE INDEX IF NOT EXISTS idx_workspaces_status
				ON workspaces (status);

			CREATE TABLE IF NOT EXISTS workspace_memberships (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				role TEXT NOT NULL DEFAULT 'owner',
				status TEXT NOT NULL DEFAULT 'active',
				is_default BOOLEAN NOT NULL DEFAULT TRUE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id, user_id)
			);

			CREATE INDEX IF NOT EXISTS idx_workspace_memberships_workspace_id
				ON workspace_memberships (workspace_id);

			CREATE INDEX IF NOT EXISTS idx_workspace_memberships_user_id
				ON workspace_memberships (user_id);

			CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_memberships_one_default_active
				ON workspace_memberships (user_id)
				WHERE is_default = TRUE AND status = 'active';
		`,
	},
	{
		ID: "20260623_002_v2_knowledge_base_blocks",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_knowledge_base_blocks (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				type TEXT NOT NULL,
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				content TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'empty',
				sort_order INTEGER NOT NULL DEFAULT 0,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL,
				UNIQUE(workspace_id, type)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_knowledge_base_blocks_workspace
				ON v2_knowledge_base_blocks (workspace_id, sort_order);

			CREATE INDEX IF NOT EXISTS idx_v2_knowledge_base_blocks_status
				ON v2_knowledge_base_blocks (workspace_id, status);
		`,
	},
	{
		ID: "20260624_003_v2_strategies",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_strategies (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				status TEXT NOT NULL DEFAULT 'draft',
				version INTEGER NOT NULL DEFAULT 1,
				title TEXT NOT NULL DEFAULT 'Стратегия v1',
				summary TEXT NOT NULL DEFAULT '',
				source_type TEXT NOT NULL DEFAULT 'manual',
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				approved_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				approved_at TIMESTAMPTZ NULL,
				activated_at TIMESTAMPTZ NULL,
				archived_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_strategies_workspace
				ON v2_strategies (workspace_id, status, version);

			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_strategies_one_active
				ON v2_strategies (workspace_id)
				WHERE status = 'active' AND archived_at IS NULL;

			CREATE TABLE IF NOT EXISTS v2_strategy_artifacts (
				id SERIAL PRIMARY KEY,
				strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				type TEXT NOT NULL,
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				content TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'empty',
				sort_order INTEGER NOT NULL DEFAULT 0,
				confidence DOUBLE PRECISION NULL,
				source TEXT NOT NULL DEFAULT 'manual',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(strategy_id, type)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_strategy_artifacts_strategy
				ON v2_strategy_artifacts (strategy_id, sort_order);

			CREATE INDEX IF NOT EXISTS idx_v2_strategy_artifacts_workspace
				ON v2_strategy_artifacts (workspace_id, type);
		`,
	},
	{
		ID: "20260624_004_v2_tactics",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_tactical_plans (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
				course_id INTEGER NULL,
				status TEXT NOT NULL DEFAULT 'draft',
				title TEXT NOT NULL DEFAULT 'Тактический план',
				summary TEXT NOT NULL DEFAULT '',
				source TEXT NOT NULL DEFAULT 'manual',
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				activated_at TIMESTAMPTZ NULL,
				archived_at TIMESTAMPTZ NULL,
				UNIQUE(workspace_id, strategy_id)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactical_plans_workspace
				ON v2_tactical_plans (workspace_id, status, strategy_id);

			CREATE TABLE IF NOT EXISTS v2_tactical_workstreams (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				tactical_plan_id INTEGER NOT NULL REFERENCES v2_tactical_plans(id) ON DELETE CASCADE,
				strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
				course_id INTEGER NULL,
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				goal TEXT NOT NULL DEFAULT '',
				ckp TEXT NOT NULL DEFAULT '',
				reason TEXT NOT NULL DEFAULT '',
				closes_risk TEXT NOT NULL DEFAULT '',
				metric_name TEXT NOT NULL DEFAULT '',
				metric_current TEXT NOT NULL DEFAULT '',
				metric_target TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'active',
				health_status TEXT NOT NULL DEFAULT 'В работе',
				contribution_type TEXT NOT NULL DEFAULT '',
				confidence DOUBLE PRECISION NULL,
				source TEXT NOT NULL DEFAULT 'manual',
				sort_order INTEGER NOT NULL DEFAULT 0,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactical_workstreams_plan
				ON v2_tactical_workstreams (workspace_id, tactical_plan_id, sort_order);

			CREATE TABLE IF NOT EXISTS v2_tactical_projects (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				workstream_id INTEGER NOT NULL REFERENCES v2_tactical_workstreams(id) ON DELETE CASCADE,
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				why_needed TEXT NOT NULL DEFAULT '',
				success_criteria TEXT NOT NULL DEFAULT '',
				failure_criteria TEXT NOT NULL DEFAULT '',
				metric_name TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'active',
				confidence DOUBLE PRECISION NULL,
				source TEXT NOT NULL DEFAULT 'manual',
				sort_order INTEGER NOT NULL DEFAULT 0,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactical_projects_workstream
				ON v2_tactical_projects (workspace_id, workstream_id, sort_order);

			CREATE TABLE IF NOT EXISTS v2_tactical_risks (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				tactical_plan_id INTEGER NOT NULL REFERENCES v2_tactical_plans(id) ON DELETE CASCADE,
				entity_type TEXT NOT NULL,
				entity_id INTEGER NOT NULL,
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				severity TEXT NOT NULL DEFAULT 'medium',
				status TEXT NOT NULL DEFAULT 'active',
				coverage_status TEXT NOT NULL DEFAULT 'uncovered',
				source TEXT NOT NULL DEFAULT 'manual',
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactical_risks_plan
				ON v2_tactical_risks (workspace_id, tactical_plan_id, entity_type, entity_id);

			CREATE TABLE IF NOT EXISTS v2_tactical_opportunities (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				tactical_plan_id INTEGER NOT NULL REFERENCES v2_tactical_plans(id) ON DELETE CASCADE,
				entity_type TEXT NOT NULL,
				entity_id INTEGER NOT NULL,
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				potential_impact TEXT NOT NULL DEFAULT 'medium',
				status TEXT NOT NULL DEFAULT 'active',
				coverage_status TEXT NOT NULL DEFAULT 'uncovered',
				source TEXT NOT NULL DEFAULT 'manual',
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactical_opportunities_plan
				ON v2_tactical_opportunities (workspace_id, tactical_plan_id, entity_type, entity_id);
		`,
	},
	{
		ID: "20260630_005_v2_courses",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_courses (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
				title TEXT NOT NULL DEFAULT 'Курс компании',
				direction TEXT NOT NULL DEFAULT '',
				strategic_goal TEXT NOT NULL DEFAULT '',
				meaning TEXT NOT NULL DEFAULT '',
				horizon INTEGER NOT NULL DEFAULT 90,
				horizon_unit TEXT NOT NULL DEFAULT 'days',
				start_date DATE NOT NULL DEFAULT CURRENT_DATE,
				end_date DATE NULL,
				key_metric TEXT NOT NULL DEFAULT '',
				success_criterion TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'active',
				source TEXT NOT NULL DEFAULT 'from_strategy',
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				activated_at TIMESTAMPTZ NULL,
				archived_at TIMESTAMPTZ NULL,
				UNIQUE(workspace_id, strategy_id)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_courses_workspace
				ON v2_courses (workspace_id, status, strategy_id);

			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_courses_one_active
				ON v2_courses (workspace_id)
				WHERE status = 'active' AND archived_at IS NULL;

			DO $$
			BEGIN
				IF NOT EXISTS (
					SELECT 1
					FROM information_schema.table_constraints
					WHERE constraint_name = 'fk_v2_tactical_plans_course'
						AND table_name = 'v2_tactical_plans'
				) THEN
					ALTER TABLE v2_tactical_plans
						ADD CONSTRAINT fk_v2_tactical_plans_course
						FOREIGN KEY (course_id) REFERENCES v2_courses(id) ON DELETE SET NULL;
				END IF;
			END $$;

			DO $$
			BEGIN
				IF NOT EXISTS (
					SELECT 1
					FROM information_schema.table_constraints
					WHERE constraint_name = 'fk_v2_tactical_workstreams_course'
						AND table_name = 'v2_tactical_workstreams'
				) THEN
					ALTER TABLE v2_tactical_workstreams
						ADD CONSTRAINT fk_v2_tactical_workstreams_course
						FOREIGN KEY (course_id) REFERENCES v2_courses(id) ON DELETE SET NULL;
				END IF;
			END $$;
		`,
	},
	{
		ID: "20260630_006_v2_tasks",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_tasks (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				course_id INTEGER NOT NULL REFERENCES v2_courses(id) ON DELETE CASCADE,
				tactical_plan_id INTEGER NOT NULL REFERENCES v2_tactical_plans(id) ON DELETE CASCADE,
				workstream_id INTEGER NOT NULL REFERENCES v2_tactical_workstreams(id) ON DELETE CASCADE,
				project_id INTEGER NULL REFERENCES v2_tactical_projects(id) ON DELETE SET NULL,
				risk_id INTEGER NULL REFERENCES v2_tactical_risks(id) ON DELETE SET NULL,
				opportunity_id INTEGER NULL REFERENCES v2_tactical_opportunities(id) ON DELETE SET NULL,
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'free',
				priority_order INTEGER NULL,
				owner_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				due_date DATE NULL,
				source_type TEXT NOT NULL DEFAULT 'manual',
				source_id INTEGER NULL,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				updated_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				started_at TIMESTAMPTZ NULL,
				completed_at TIMESTAMPTZ NULL,
				archived_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tasks_workspace_status
				ON v2_tasks (workspace_id, status, updated_at DESC);

			CREATE INDEX IF NOT EXISTS idx_v2_tasks_workstream_status
				ON v2_tasks (workspace_id, workstream_id, status, priority_order, id);

			CREATE INDEX IF NOT EXISTS idx_v2_tasks_project
				ON v2_tasks (workspace_id, project_id)
				WHERE project_id IS NOT NULL;

			CREATE INDEX IF NOT EXISTS idx_v2_tasks_risk
				ON v2_tasks (workspace_id, risk_id)
				WHERE risk_id IS NOT NULL;

			CREATE INDEX IF NOT EXISTS idx_v2_tasks_opportunity
				ON v2_tasks (workspace_id, opportunity_id)
				WHERE opportunity_id IS NOT NULL;
		`,
	},
	{
		ID: "20260702_007_v2_knowledge_intake",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_knowledge_documents (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				title TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id, document_type)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_knowledge_documents_workspace
				ON v2_knowledge_documents (workspace_id, document_type);

			CREATE TABLE IF NOT EXISTS v2_knowledge_document_entries (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_id INTEGER NOT NULL REFERENCES v2_knowledge_documents(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				text TEXT NOT NULL,
				statement_type TEXT NOT NULL DEFAULT 'statement',
				source_type TEXT NOT NULL DEFAULT 'manual',
				source_session_id INTEGER NULL,
				source_message_id TEXT NULL,
				source_quote TEXT NOT NULL DEFAULT '',
				position INTEGER NOT NULL DEFAULT 0,
				status TEXT NOT NULL DEFAULT 'active',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_knowledge_entries_document
				ON v2_knowledge_document_entries (workspace_id, document_id, status, position, id);

			CREATE TABLE IF NOT EXISTS v2_knowledge_document_entry_versions (
				id SERIAL PRIMARY KEY,
				entry_id INTEGER NOT NULL REFERENCES v2_knowledge_document_entries(id) ON DELETE CASCADE,
				old_text TEXT NOT NULL,
				new_text TEXT NOT NULL,
				changed_by_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				source_session_id INTEGER NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE TABLE IF NOT EXISTS v2_knowledge_intake_sessions (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				raw_text TEXT NOT NULL,
				input_summary TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'processing',
				router_prompt_version TEXT NOT NULL DEFAULT '',
				reconciler_prompt_version TEXT NOT NULL DEFAULT '',
				router_raw_response_json JSONB NULL,
				error_message TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_knowledge_intake_sessions_workspace
				ON v2_knowledge_intake_sessions (workspace_id, status, created_at DESC);

			CREATE TABLE IF NOT EXISTS v2_proposed_knowledge_items (
				id SERIAL PRIMARY KEY,
				session_id INTEGER NOT NULL REFERENCES v2_knowledge_intake_sessions(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				client_item_id TEXT NOT NULL,
				source_quote TEXT NOT NULL DEFAULT '',
				clean_text TEXT NOT NULL,
				statement_type TEXT NOT NULL,
				target_document_type TEXT NOT NULL,
				routing_reason TEXT NOT NULL DEFAULT '',
				confidence TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(session_id, client_item_id)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_proposed_knowledge_items_session
				ON v2_proposed_knowledge_items (session_id, target_document_type);

			CREATE TABLE IF NOT EXISTS v2_proposed_document_patches (
				id SERIAL PRIMARY KEY,
				session_id INTEGER NOT NULL REFERENCES v2_knowledge_intake_sessions(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_id INTEGER NOT NULL REFERENCES v2_knowledge_documents(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				patch_type TEXT NOT NULL,
				target_entry_id INTEGER NULL REFERENCES v2_knowledge_document_entries(id) ON DELETE SET NULL,
				source_item_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
				existing_text TEXT NOT NULL DEFAULT '',
				new_text TEXT NOT NULL,
				reason TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'suggested',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				applied_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_proposed_patches_session
				ON v2_proposed_document_patches (session_id, document_type, status);

			CREATE TABLE IF NOT EXISTS v2_proposed_document_conflicts (
				id SERIAL PRIMARY KEY,
				session_id INTEGER NOT NULL REFERENCES v2_knowledge_intake_sessions(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_id INTEGER NOT NULL REFERENCES v2_knowledge_documents(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				existing_entry_id INTEGER NULL REFERENCES v2_knowledge_document_entries(id) ON DELETE SET NULL,
				source_item_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
				existing_text TEXT NOT NULL DEFAULT '',
				new_text TEXT NOT NULL,
				question TEXT NOT NULL DEFAULT '',
				option_a_text TEXT NOT NULL DEFAULT '',
				option_b_text TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'active',
				selected_option TEXT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				resolved_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_proposed_conflicts_session
				ON v2_proposed_document_conflicts (session_id, document_type, status);

			CREATE TABLE IF NOT EXISTS v2_ignored_knowledge_items (
				id SERIAL PRIMARY KEY,
				session_id INTEGER NOT NULL REFERENCES v2_knowledge_intake_sessions(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_id INTEGER NOT NULL REFERENCES v2_knowledge_documents(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				source_item_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
				clean_text TEXT NOT NULL DEFAULT '',
				reason TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
		`,
	},
	{
		ID: "20260703_008_v2_knowledge_guidance",
		SQL: `
			ALTER TABLE v2_knowledge_intake_sessions
				ADD COLUMN IF NOT EXISTS conversation_intent_json JSONB NULL,
				ADD COLUMN IF NOT EXISTS guidance_question_block_id INTEGER NULL;

			CREATE TABLE IF NOT EXISTS v2_ai_prompt_configs (
				id SERIAL PRIMARY KEY,
				prompt_name TEXT NOT NULL,
				prompt_version TEXT NOT NULL,
				model TEXT NOT NULL DEFAULT '',
				temperature DOUBLE PRECISION NOT NULL DEFAULT 0,
				template TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(prompt_name, prompt_version)
			);

			CREATE TABLE IF NOT EXISTS v2_ai_call_logs (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				ai_module TEXT NOT NULL,
				prompt_version TEXT NOT NULL DEFAULT '',
				model TEXT NOT NULL DEFAULT '',
				input_json JSONB NULL,
				output_json JSONB NULL,
				status TEXT NOT NULL DEFAULT 'processing',
				error TEXT NOT NULL DEFAULT '',
				latency_ms INTEGER NOT NULL DEFAULT 0,
				token_usage_input INTEGER NULL,
				token_usage_output INTEGER NULL,
				estimated_cost DOUBLE PRECISION NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_ai_call_logs_workspace
				ON v2_ai_call_logs (workspace_id, created_at DESC);

			CREATE TABLE IF NOT EXISTS v2_company_profiles (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				company_profile_text TEXT NOT NULL DEFAULT '',
				company_profile_status TEXT NOT NULL DEFAULT 'red',
				company_profile_version TEXT NOT NULL DEFAULT 'company_profile_collector_v1',
				company_profile_raw_json JSONB NULL,
				baseline_coverage_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id)
			);

			CREATE TABLE IF NOT EXISTS v2_knowledge_document_readiness (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_id INTEGER NOT NULL REFERENCES v2_knowledge_documents(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				readiness_status TEXT NOT NULL DEFAULT 'red',
				readiness_reason TEXT NOT NULL DEFAULT '',
				main_missing_areas_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				should_run_deep_evaluator BOOLEAN NOT NULL DEFAULT FALSE,
				confidence TEXT NOT NULL DEFAULT 'low',
				prompt_version TEXT NOT NULL DEFAULT 'document_readiness_preflight_v1',
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id, document_id)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_knowledge_readiness_workspace
				ON v2_knowledge_document_readiness (workspace_id, document_type);

			CREATE TABLE IF NOT EXISTS v2_guidance_question_blocks (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				source TEXT NOT NULL DEFAULT 'first_gate',
				guidance_status TEXT NOT NULL DEFAULT 'ask_next_question',
				question_type TEXT NOT NULL DEFAULT 'new_area_opening',
				intended_focus_summary TEXT NOT NULL DEFAULT '',
				intended_documents_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				selection_reason_internal TEXT NOT NULL DEFAULT '',
				title TEXT NOT NULL DEFAULT '',
				intro TEXT NOT NULL DEFAULT '',
				questions_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				handled_user_intent_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				confidence TEXT NOT NULL DEFAULT 'medium',
				status TEXT NOT NULL DEFAULT 'active',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				answered_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_guidance_question_blocks_active
				ON v2_guidance_question_blocks (workspace_id, status, created_at DESC);

			CREATE TABLE IF NOT EXISTS v2_knowledge_base_readiness (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				overall_status TEXT NOT NULL DEFAULT 'not_ready',
				overall_score INTEGER NOT NULL DEFAULT 0,
				strategy_transition_allowed BOOLEAN NOT NULL DEFAULT FALSE,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id)
			);
		`,
	},
	{
		ID: "20260707_009_v2_knowledge_document_views",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_knowledge_document_views (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_id INTEGER NOT NULL REFERENCES v2_knowledge_documents(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				title TEXT NOT NULL DEFAULT '',
				rendered_text TEXT NOT NULL DEFAULT '',
				sections_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				source_entry_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				composer_prompt_version TEXT NOT NULL DEFAULT '',
				composer_raw_json JSONB NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id, document_id)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_knowledge_document_views_workspace
				ON v2_knowledge_document_views (workspace_id, document_type);
		`,
	},
	{
		ID: "20260708_010_v2_knowledge_intake_progress",
		SQL: `
			ALTER TABLE v2_knowledge_intake_sessions
				ADD COLUMN IF NOT EXISTS guidance_result_json JSONB NULL;

			CREATE TABLE IF NOT EXISTS v2_knowledge_intake_progress_events (
				id SERIAL PRIMARY KEY,
				session_id INTEGER NOT NULL REFERENCES v2_knowledge_intake_sessions(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				stage TEXT NOT NULL,
				message TEXT NOT NULL,
				details_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_knowledge_intake_progress_session
				ON v2_knowledge_intake_progress_events (workspace_id, session_id, id);
		`,
	},
	{
		ID: "20260708_011_strategic_memory_v1",
		SQL: `
			CREATE TABLE IF NOT EXISTS strategic_raw_sources (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				source_type TEXT NOT NULL,
				content TEXT NOT NULL DEFAULT '',
				metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_strategic_raw_sources_workspace
				ON strategic_raw_sources (workspace_id, created_at DESC, id DESC);

			CREATE TABLE IF NOT EXISTS strategic_claims (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				claim_text TEXT NOT NULL,
				claim_type TEXT NOT NULL DEFAULT 'self_reported_fact',
				topic_key TEXT NOT NULL DEFAULT 'general',
				evidence_level TEXT NOT NULL DEFAULT 'self_reported',
				confidence TEXT NOT NULL DEFAULT 'medium',
				source_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				status TEXT NOT NULL DEFAULT 'active',
				superseded_by INTEGER NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_strategic_claims_workspace
				ON strategic_claims (workspace_id, status, topic_key, updated_at DESC);

			CREATE TABLE IF NOT EXISTS strategic_memory_snapshots (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				snapshot_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				business_stage TEXT NOT NULL DEFAULT 'unknown',
				version INTEGER NOT NULL DEFAULT 1,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_strategic_memory_snapshots_workspace
				ON strategic_memory_snapshots (workspace_id, version DESC, id DESC);

			CREATE TABLE IF NOT EXISTS strategic_research_agenda_items (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				topic_key TEXT NOT NULL DEFAULT 'general',
				question_goal TEXT NOT NULL DEFAULT '',
				why_it_matters TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'open',
				priority TEXT NOT NULL DEFAULT 'medium',
				linked_claim_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				last_asked_at TIMESTAMPTZ NULL,
				times_asked INTEGER NOT NULL DEFAULT 0,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id, topic_key, question_goal)
			);

			CREATE INDEX IF NOT EXISTS idx_strategic_research_agenda_workspace
				ON strategic_research_agenda_items (workspace_id, status, priority, updated_at DESC);

			CREATE TABLE IF NOT EXISTS strategic_communication_profiles (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				tone TEXT NOT NULL DEFAULT 'direct',
				address_style TEXT NOT NULL DEFAULT 'ты',
				detail_level TEXT NOT NULL DEFAULT 'normal',
				structure_preference TEXT NOT NULL DEFAULT 'free_dialogue',
				frustration_sensitivity TEXT NOT NULL DEFAULT 'medium',
				known_preferences_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id)
			);

			CREATE TABLE IF NOT EXISTS strategic_documents (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				title TEXT NOT NULL DEFAULT '',
				markdown TEXT NOT NULL DEFAULT '',
				source_claim_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				status TEXT NOT NULL DEFAULT 'draft',
				version INTEGER NOT NULL DEFAULT 1,
				generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id, document_type)
			);

			CREATE INDEX IF NOT EXISTS idx_strategic_documents_workspace
				ON strategic_documents (workspace_id, document_type);

			CREATE TABLE IF NOT EXISTS strategic_ai_runs (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				scenario TEXT NOT NULL,
				model TEXT NOT NULL DEFAULT '',
				prompt_version TEXT NOT NULL DEFAULT '',
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				duration_ms INTEGER NOT NULL DEFAULT 0,
				status TEXT NOT NULL DEFAULT 'success',
				error TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_strategic_ai_runs_workspace
				ON strategic_ai_runs (workspace_id, created_at DESC);
		`,
	},
	{
		ID: "20260708_012_strategic_dialogue_focus",
		SQL: `
			CREATE TABLE IF NOT EXISTS strategic_dialogue_focus (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				current_topic TEXT NOT NULL DEFAULT '',
				research_goal TEXT NOT NULL DEFAULT '',
				last_question TEXT NOT NULL DEFAULT '',
				expected_answer_type TEXT NOT NULL DEFAULT '',
				answer_status TEXT NOT NULL DEFAULT 'not_started',
				do_not_repeat_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				next_angles_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id)
			);
		`,
	},
	{
		ID: "20260709_013_strategic_openai_native_context",
		SQL: `
			CREATE TABLE IF NOT EXISTS strategic_openai_sessions (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				previous_response_id TEXT NOT NULL DEFAULT '',
				vector_store_id TEXT NOT NULL DEFAULT '',
				compact_threshold INTEGER NOT NULL DEFAULT 120000,
				prompt_cache_key TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id)
			);

			CREATE TABLE IF NOT EXISTS strategic_openai_files (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				raw_source_id INTEGER NULL REFERENCES strategic_raw_sources(id) ON DELETE SET NULL,
				openai_file_id TEXT NOT NULL,
				vector_store_id TEXT NOT NULL DEFAULT '',
				filename TEXT NOT NULL DEFAULT '',
				content_type TEXT NOT NULL DEFAULT '',
				size_bytes BIGINT NOT NULL DEFAULT 0,
				status TEXT NOT NULL DEFAULT 'uploaded',
				error TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_strategic_openai_files_workspace
				ON strategic_openai_files (workspace_id, created_at DESC);

			CREATE INDEX IF NOT EXISTS idx_strategic_openai_files_file_id
				ON strategic_openai_files (openai_file_id);
		`,
	},
	{
		ID: "20260711_014_strategic_quality_reports",
		SQL: `
			CREATE TABLE IF NOT EXISTS strategic_quality_reports (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				readiness_score INTEGER NOT NULL DEFAULT 0,
				readiness_status TEXT NOT NULL DEFAULT 'not_ready',
				changed_document_types_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				report_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_strategic_quality_reports_workspace
				ON strategic_quality_reports (workspace_id, created_at DESC, id DESC);
		`,
	},
	{
		ID: "20260713_015_strategy_facilitator_chat",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_strategy_chat_messages (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				role TEXT NOT NULL,
				content TEXT NOT NULL DEFAULT '',
				metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_strategy_chat_messages_workspace
				ON v2_strategy_chat_messages (workspace_id, created_at DESC, id DESC);

			CREATE TABLE IF NOT EXISTS v2_strategy_openai_sessions (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				previous_response_id TEXT NOT NULL DEFAULT '',
				compact_threshold INTEGER NOT NULL DEFAULT 120000,
				prompt_cache_key TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id)
			);
		`,
	},
	{
		ID: "20260713_016_strategy_synthesis",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_strategy_synthesis_runs (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
				version INTEGER NOT NULL DEFAULT 1,
				session_revision INTEGER NOT NULL DEFAULT 0,
				through_message_id INTEGER NOT NULL DEFAULT 0,
				status TEXT NOT NULL DEFAULT 'queued',
				model TEXT NOT NULL DEFAULT '',
				prompt_version TEXT NOT NULL DEFAULT '',
				summary TEXT NOT NULL DEFAULT '',
				openai_response_id TEXT NOT NULL DEFAULT '',
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				duration_ms BIGINT NOT NULL DEFAULT 0,
				error TEXT NOT NULL DEFAULT '',
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				started_at TIMESTAMPTZ NULL,
				completed_at TIMESTAMPTZ NULL,
				UNIQUE(workspace_id, version)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_strategy_synthesis_runs_workspace
				ON v2_strategy_synthesis_runs (workspace_id, created_at DESC, id DESC);

			CREATE TABLE IF NOT EXISTS v2_strategy_synthesis_documents (
				id SERIAL PRIMARY KEY,
				run_id INTEGER NOT NULL REFERENCES v2_strategy_synthesis_runs(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				title TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'insufficient_data',
				content_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				source_refs_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				sort_order INTEGER NOT NULL DEFAULT 0,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(run_id, document_type)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_strategy_synthesis_documents_run
				ON v2_strategy_synthesis_documents (run_id, sort_order, id);
		`,
	},
	{
		ID: "20260713_017_strategy_readiness_pipeline",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_strategy_readiness_runs (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
				session_revision INTEGER NOT NULL,
				validated_through_message_id INTEGER NOT NULL,
				status TEXT NOT NULL DEFAULT 'queued',
				verdict TEXT NOT NULL DEFAULT '',
				can_synthesize BOOLEAN NOT NULL DEFAULT FALSE,
				confidence TEXT NOT NULL DEFAULT '',
				report_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				model TEXT NOT NULL DEFAULT '',
				prompt_version TEXT NOT NULL DEFAULT '',
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				duration_ms BIGINT NOT NULL DEFAULT 0,
				error TEXT NOT NULL DEFAULT '',
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				started_at TIMESTAMPTZ NULL,
				completed_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_strategy_readiness_runs_workspace
				ON v2_strategy_readiness_runs (workspace_id, created_at DESC, id DESC);

			CREATE INDEX IF NOT EXISTS idx_v2_strategy_readiness_runs_active
				ON v2_strategy_readiness_runs (workspace_id, status, created_at DESC);

			CREATE TABLE IF NOT EXISTS v2_strategy_session_state (
				workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
				revision INTEGER NOT NULL DEFAULT 0,
				last_user_message_id INTEGER NOT NULL DEFAULT 0,
				last_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				facilitator_status TEXT NOT NULL DEFAULT 'continue',
				status_reason TEXT NOT NULL DEFAULT '',
				remaining_uncertainties_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				last_audited_revision INTEGER NOT NULL DEFAULT 0,
				last_readiness_run_id INTEGER NULL REFERENCES v2_strategy_readiness_runs(id) ON DELETE SET NULL,
				last_synthesis_run_id INTEGER NULL REFERENCES v2_strategy_synthesis_runs(id) ON DELETE SET NULL,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE TABLE IF NOT EXISTS v2_strategy_readiness_queue (
				workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
				strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
				session_revision INTEGER NOT NULL,
				through_message_id INTEGER NOT NULL,
				requested_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				not_before TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_strategy_readiness_queue_due
				ON v2_strategy_readiness_queue (not_before, updated_at);
		`,
	},
	{
		ID: "20260713_018_strategy_artifact_formatting",
		SQL: `
			ALTER TABLE v2_strategy_synthesis_documents
				ADD COLUMN IF NOT EXISTS display_title TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS frame_title TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS frame_subtitle TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS primary_signal TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS visual_status TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS formatted_markdown TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS open_questions_json JSONB NOT NULL DEFAULT '[]'::jsonb;
		`,
	},
	{
		ID: "20260714_019_course_strategy_provenance",
		SQL: `
			ALTER TABLE v2_courses
				ADD COLUMN IF NOT EXISTS source_synthesis_run_id INTEGER NULL REFERENCES v2_strategy_synthesis_runs(id) ON DELETE SET NULL,
				ADD COLUMN IF NOT EXISTS source_session_revision INTEGER NOT NULL DEFAULT 0;

			ALTER TABLE v2_courses
				ALTER COLUMN status SET DEFAULT 'draft';

			UPDATE v2_courses course
			SET source_synthesis_run_id = (
					SELECT run.id
					FROM v2_strategy_synthesis_runs run
					WHERE run.workspace_id=course.workspace_id
						AND run.strategy_id=course.strategy_id
						AND run.status='completed'
					ORDER BY run.created_at DESC, run.id DESC
					LIMIT 1
				),
				source_session_revision = COALESCE((
					SELECT run.session_revision
					FROM v2_strategy_synthesis_runs run
					WHERE run.workspace_id=course.workspace_id
						AND run.strategy_id=course.strategy_id
						AND run.status='completed'
					ORDER BY run.created_at DESC, run.id DESC
					LIMIT 1
				), 0)
			WHERE source_synthesis_run_id IS NULL;

			CREATE INDEX IF NOT EXISTS idx_v2_courses_source_synthesis
				ON v2_courses (workspace_id, source_synthesis_run_id);
		`,
	},
	{
		ID: "20260715_020_tactics_facilitator_chat",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_tactics_chat_messages (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				role TEXT NOT NULL,
				content TEXT NOT NULL DEFAULT '',
				metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactics_chat_messages_workspace
				ON v2_tactics_chat_messages (workspace_id, created_at DESC, id DESC);

			CREATE TABLE IF NOT EXISTS v2_tactics_openai_sessions (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				previous_response_id TEXT NOT NULL DEFAULT '',
				compact_threshold INTEGER NOT NULL DEFAULT 120000,
				prompt_cache_key TEXT NOT NULL DEFAULT '',
				context_fingerprint TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id)
			);

			CREATE TABLE IF NOT EXISTS v2_tactics_session_state (
				workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
				revision INTEGER NOT NULL DEFAULT 0,
				last_user_message_id INTEGER NOT NULL DEFAULT 0,
				last_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				facilitator_status TEXT NOT NULL DEFAULT 'in_progress',
				status_reason TEXT NOT NULL DEFAULT '',
				current_focus_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				decisions_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				open_questions_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				needs_strategy_review BOOLEAN NOT NULL DEFAULT FALSE,
				strategy_review_reason TEXT NOT NULL DEFAULT '',
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
		`,
	},
	{
		ID: "20260715_021_tactics_readiness_pipeline",
		SQL: `
			ALTER TABLE v2_tactical_plans
				ADD COLUMN IF NOT EXISTS revision INTEGER NOT NULL DEFAULT 1;

			CREATE TABLE IF NOT EXISTS v2_tactics_readiness_runs (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				tactical_plan_id INTEGER NOT NULL REFERENCES v2_tactical_plans(id) ON DELETE CASCADE,
				strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
				course_id INTEGER NULL REFERENCES v2_courses(id) ON DELETE SET NULL,
				session_revision INTEGER NOT NULL,
				tactical_plan_revision INTEGER NOT NULL,
				validated_through_message_id INTEGER NOT NULL,
				status TEXT NOT NULL DEFAULT 'queued',
				verdict TEXT NOT NULL DEFAULT '',
				can_activate BOOLEAN NOT NULL DEFAULT FALSE,
				overall_score INTEGER NOT NULL DEFAULT 0,
				confidence TEXT NOT NULL DEFAULT '',
				report_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				model TEXT NOT NULL DEFAULT '',
				prompt_version TEXT NOT NULL DEFAULT '',
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				duration_ms BIGINT NOT NULL DEFAULT 0,
				error TEXT NOT NULL DEFAULT '',
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				started_at TIMESTAMPTZ NULL,
				completed_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactics_readiness_runs_workspace
				ON v2_tactics_readiness_runs (workspace_id, created_at DESC, id DESC);

			CREATE INDEX IF NOT EXISTS idx_v2_tactics_readiness_runs_active
				ON v2_tactics_readiness_runs (workspace_id, status, created_at DESC);

			CREATE TABLE IF NOT EXISTS v2_tactics_readiness_queue (
				workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
				tactical_plan_id INTEGER NOT NULL REFERENCES v2_tactical_plans(id) ON DELETE CASCADE,
				strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
				course_id INTEGER NULL REFERENCES v2_courses(id) ON DELETE SET NULL,
				session_revision INTEGER NOT NULL,
				tactical_plan_revision INTEGER NOT NULL,
				through_message_id INTEGER NOT NULL,
				requested_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				not_before TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactics_readiness_queue_due
				ON v2_tactics_readiness_queue (not_before, updated_at);

			CREATE OR REPLACE FUNCTION reup_touch_tactical_plan_revision()
			RETURNS TRIGGER AS $$
			DECLARE
				plan_id INTEGER;
			BEGIN
				IF TG_TABLE_NAME = 'v2_tactical_workstreams' THEN
					plan_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.tactical_plan_id ELSE NEW.tactical_plan_id END;
				ELSIF TG_TABLE_NAME = 'v2_tactical_projects' THEN
					SELECT tactical_plan_id INTO plan_id
					FROM v2_tactical_workstreams
					WHERE id = CASE WHEN TG_OP = 'DELETE' THEN OLD.workstream_id ELSE NEW.workstream_id END;
				ELSE
					plan_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.tactical_plan_id ELSE NEW.tactical_plan_id END;
				END IF;

				IF plan_id IS NOT NULL THEN
					UPDATE v2_tactical_plans
					SET revision=revision + 1, updated_at=NOW()
					WHERE id=plan_id;
				END IF;

				IF TG_OP = 'DELETE' THEN
					RETURN OLD;
				END IF;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql;

			DROP TRIGGER IF EXISTS trg_touch_tactical_plan_from_workstream ON v2_tactical_workstreams;
			CREATE TRIGGER trg_touch_tactical_plan_from_workstream
				AFTER INSERT OR UPDATE OR DELETE ON v2_tactical_workstreams
				FOR EACH ROW EXECUTE FUNCTION reup_touch_tactical_plan_revision();

			DROP TRIGGER IF EXISTS trg_touch_tactical_plan_from_project ON v2_tactical_projects;
			CREATE TRIGGER trg_touch_tactical_plan_from_project
				AFTER INSERT OR UPDATE OR DELETE ON v2_tactical_projects
				FOR EACH ROW EXECUTE FUNCTION reup_touch_tactical_plan_revision();

			DROP TRIGGER IF EXISTS trg_touch_tactical_plan_from_risk ON v2_tactical_risks;
			CREATE TRIGGER trg_touch_tactical_plan_from_risk
				AFTER INSERT OR UPDATE OR DELETE ON v2_tactical_risks
				FOR EACH ROW EXECUTE FUNCTION reup_touch_tactical_plan_revision();

			DROP TRIGGER IF EXISTS trg_touch_tactical_plan_from_opportunity ON v2_tactical_opportunities;
			CREATE TRIGGER trg_touch_tactical_plan_from_opportunity
				AFTER INSERT OR UPDATE OR DELETE ON v2_tactical_opportunities
				FOR EACH ROW EXECUTE FUNCTION reup_touch_tactical_plan_revision();
		`,
	},
	{
		ID: "20260716_022_tactics_completion_pipeline",
		SQL: `
			ALTER TABLE v2_tactical_plans
				ADD COLUMN IF NOT EXISTS activated_revision INTEGER NULL,
				ADD COLUMN IF NOT EXISTS activation_readiness_run_id INTEGER NULL REFERENCES v2_tactics_readiness_runs(id) ON DELETE SET NULL;

			CREATE TABLE IF NOT EXISTS v2_tactical_plan_versions (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				tactical_plan_id INTEGER NOT NULL REFERENCES v2_tactical_plans(id) ON DELETE CASCADE,
				revision INTEGER NOT NULL,
				readiness_run_id INTEGER NULL REFERENCES v2_tactics_readiness_runs(id) ON DELETE SET NULL,
				snapshot_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				activated_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				activated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(tactical_plan_id, revision)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactical_plan_versions_workspace
				ON v2_tactical_plan_versions (workspace_id, tactical_plan_id, revision DESC);

			CREATE TABLE IF NOT EXISTS v2_tactics_applied_changes (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				tactical_plan_id INTEGER NOT NULL REFERENCES v2_tactical_plans(id) ON DELETE CASCADE,
				source_message_id INTEGER NULL REFERENCES v2_tactics_chat_messages(id) ON DELETE SET NULL,
				operation TEXT NOT NULL,
				entity_type TEXT NOT NULL,
				entity_id INTEGER NOT NULL,
				title TEXT NOT NULL DEFAULT '',
				change_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactics_applied_changes_plan
				ON v2_tactics_applied_changes (workspace_id, tactical_plan_id, created_at DESC, id DESC);

			CREATE OR REPLACE FUNCTION reup_touch_tactical_plan_revision()
			RETURNS TRIGGER AS $$
			DECLARE
				plan_id INTEGER;
			BEGIN
				IF TG_TABLE_NAME = 'v2_tactical_workstreams' THEN
					plan_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.tactical_plan_id ELSE NEW.tactical_plan_id END;
				ELSIF TG_TABLE_NAME = 'v2_tactical_projects' THEN
					SELECT tactical_plan_id INTO plan_id
					FROM v2_tactical_workstreams
					WHERE id = CASE WHEN TG_OP = 'DELETE' THEN OLD.workstream_id ELSE NEW.workstream_id END;
				ELSE
					plan_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.tactical_plan_id ELSE NEW.tactical_plan_id END;
				END IF;

				IF plan_id IS NOT NULL THEN
					UPDATE v2_tactical_plans
					SET revision=revision + 1,
						status='draft',
						activated_at=NULL,
						updated_at=NOW()
					WHERE id=plan_id;
				END IF;

				IF TG_OP = 'DELETE' THEN
					RETURN OLD;
				END IF;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql;
		`,
	},
	{
		ID: "20260716_023_tasks_intelligence_v2",
		SQL: `
			ALTER TABLE v2_tasks
				ADD COLUMN IF NOT EXISTS expected_result TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS success_criteria TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS why_now TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS manual_priority_score INTEGER NULL,
				ADD COLUMN IF NOT EXISTS manual_priority_tier TEXT NULL;

			CREATE TABLE IF NOT EXISTS v2_task_brainstorm_messages (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				workstream_id INTEGER NOT NULL REFERENCES v2_tactical_workstreams(id) ON DELETE CASCADE,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				role TEXT NOT NULL,
				content TEXT NOT NULL DEFAULT '',
				actions_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_task_brainstorm_messages_scope
				ON v2_task_brainstorm_messages (workspace_id, workstream_id, created_at, id);

			CREATE TABLE IF NOT EXISTS v2_task_brainstorm_sessions (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				workstream_id INTEGER NOT NULL REFERENCES v2_tactical_workstreams(id) ON DELETE CASCADE,
				previous_response_id TEXT NOT NULL DEFAULT '',
				compact_threshold INTEGER NOT NULL DEFAULT 120000,
				prompt_cache_key TEXT NOT NULL DEFAULT '',
				context_fingerprint TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id, workstream_id)
			);

			CREATE TABLE IF NOT EXISTS v2_task_brainstorm_action_applications (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				workstream_id INTEGER NOT NULL REFERENCES v2_tactical_workstreams(id) ON DELETE CASCADE,
				message_id INTEGER NOT NULL REFERENCES v2_task_brainstorm_messages(id) ON DELETE CASCADE,
				action_index INTEGER NOT NULL,
				action_type TEXT NOT NULL,
				task_id INTEGER NULL REFERENCES v2_tasks(id) ON DELETE SET NULL,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				status TEXT NOT NULL DEFAULT 'applied',
				error_text TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(message_id, action_index)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_task_brainstorm_applications_scope
				ON v2_task_brainstorm_action_applications (workspace_id, workstream_id, created_at DESC);

			CREATE TABLE IF NOT EXISTS v2_task_evaluation_jobs (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
				requested_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				status TEXT NOT NULL DEFAULT 'queued',
				attempts INTEGER NOT NULL DEFAULT 0,
				revision INTEGER NOT NULL DEFAULT 1,
				running_revision INTEGER NOT NULL DEFAULT 0,
				not_before TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				error_text TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(task_id)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_task_evaluation_jobs_due
				ON v2_task_evaluation_jobs (status, not_before, id);

			CREATE TABLE IF NOT EXISTS v2_task_evaluations (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
				model TEXT NOT NULL DEFAULT '',
				prompt_version TEXT NOT NULL DEFAULT '',
				strategic_relevance INTEGER NOT NULL,
				course_alignment INTEGER NOT NULL,
				tactical_alignment INTEGER NOT NULL,
				expected_impact INTEGER NOT NULL,
				urgency INTEGER NOT NULL,
				effort INTEGER NOT NULL,
				confidence INTEGER NOT NULL,
				priority_score INTEGER NOT NULL,
				priority_tier TEXT NOT NULL,
				recommendation TEXT NOT NULL,
				priority_reason TEXT NOT NULL DEFAULT '',
				clarification_question TEXT NOT NULL DEFAULT '',
				missing_information_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				context_fingerprint TEXT NOT NULL DEFAULT '',
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				duration_ms BIGINT NOT NULL DEFAULT 0,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_task_evaluations_latest
				ON v2_task_evaluations (workspace_id, task_id, created_at DESC, id DESC);

			CREATE OR REPLACE FUNCTION reup_queue_task_evaluations_from_context()
			RETURNS TRIGGER AS $$
			BEGIN
				IF TG_TABLE_NAME = 'v2_courses' THEN
					INSERT INTO v2_task_evaluation_jobs (workspace_id, task_id, status, attempts, not_before, error_text)
					SELECT task.workspace_id, task.id, 'queued', 0, NOW(), ''
					FROM v2_tasks task
					WHERE task.course_id=NEW.id AND task.archived_at IS NULL
					ON CONFLICT (task_id) DO UPDATE SET
						status=CASE WHEN v2_task_evaluation_jobs.status='running' THEN 'running' ELSE 'queued' END,
						attempts=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.attempts ELSE 0 END,
						revision=v2_task_evaluation_jobs.revision + 1,
						not_before=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.not_before ELSE NOW() END,
						error_text='', updated_at=NOW();
				ELSIF TG_TABLE_NAME = 'v2_strategies' THEN
					INSERT INTO v2_task_evaluation_jobs (workspace_id, task_id, status, attempts, not_before, error_text)
					SELECT task.workspace_id, task.id, 'queued', 0, NOW(), ''
					FROM v2_tasks task
					JOIN v2_tactical_plans plan ON plan.id=task.tactical_plan_id
					WHERE plan.strategy_id=NEW.id AND task.archived_at IS NULL
					ON CONFLICT (task_id) DO UPDATE SET
						status=CASE WHEN v2_task_evaluation_jobs.status='running' THEN 'running' ELSE 'queued' END,
						attempts=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.attempts ELSE 0 END,
						revision=v2_task_evaluation_jobs.revision + 1,
						not_before=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.not_before ELSE NOW() END,
						error_text='', updated_at=NOW();
				ELSIF TG_TABLE_NAME = 'v2_tactical_workstreams' THEN
					INSERT INTO v2_task_evaluation_jobs (workspace_id, task_id, status, attempts, not_before, error_text)
					SELECT task.workspace_id, task.id, 'queued', 0, NOW(), ''
					FROM v2_tasks task
					WHERE task.workstream_id=NEW.id AND task.archived_at IS NULL
					ON CONFLICT (task_id) DO UPDATE SET
						status=CASE WHEN v2_task_evaluation_jobs.status='running' THEN 'running' ELSE 'queued' END,
						attempts=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.attempts ELSE 0 END,
						revision=v2_task_evaluation_jobs.revision + 1,
						not_before=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.not_before ELSE NOW() END,
						error_text='', updated_at=NOW();
				ELSIF TG_TABLE_NAME = 'v2_tactical_projects' THEN
					INSERT INTO v2_task_evaluation_jobs (workspace_id, task_id, status, attempts, not_before, error_text)
					SELECT task.workspace_id, task.id, 'queued', 0, NOW(), ''
					FROM v2_tasks task
					WHERE task.project_id=NEW.id AND task.archived_at IS NULL
					ON CONFLICT (task_id) DO UPDATE SET
						status=CASE WHEN v2_task_evaluation_jobs.status='running' THEN 'running' ELSE 'queued' END,
						attempts=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.attempts ELSE 0 END,
						revision=v2_task_evaluation_jobs.revision + 1,
						not_before=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.not_before ELSE NOW() END,
						error_text='', updated_at=NOW();
				ELSIF TG_TABLE_NAME = 'v2_tactical_risks' THEN
					INSERT INTO v2_task_evaluation_jobs (workspace_id, task_id, status, attempts, not_before, error_text)
					SELECT task.workspace_id, task.id, 'queued', 0, NOW(), ''
					FROM v2_tasks task
					WHERE task.risk_id=NEW.id AND task.archived_at IS NULL
					ON CONFLICT (task_id) DO UPDATE SET
						status=CASE WHEN v2_task_evaluation_jobs.status='running' THEN 'running' ELSE 'queued' END,
						attempts=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.attempts ELSE 0 END,
						revision=v2_task_evaluation_jobs.revision + 1,
						not_before=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.not_before ELSE NOW() END,
						error_text='', updated_at=NOW();
				ELSIF TG_TABLE_NAME = 'v2_tactical_opportunities' THEN
					INSERT INTO v2_task_evaluation_jobs (workspace_id, task_id, status, attempts, not_before, error_text)
					SELECT task.workspace_id, task.id, 'queued', 0, NOW(), ''
					FROM v2_tasks task
					WHERE task.opportunity_id=NEW.id AND task.archived_at IS NULL
					ON CONFLICT (task_id) DO UPDATE SET
						status=CASE WHEN v2_task_evaluation_jobs.status='running' THEN 'running' ELSE 'queued' END,
						attempts=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.attempts ELSE 0 END,
						revision=v2_task_evaluation_jobs.revision + 1,
						not_before=CASE WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.not_before ELSE NOW() END,
						error_text='', updated_at=NOW();
				END IF;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql;

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_course ON v2_courses;
			CREATE TRIGGER trg_queue_task_eval_from_course
				AFTER UPDATE OF direction, strategic_goal, key_metric, success_criterion, status ON v2_courses
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_strategy ON v2_strategies;
			CREATE TRIGGER trg_queue_task_eval_from_strategy
				AFTER UPDATE OF summary, status ON v2_strategies
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_workstream ON v2_tactical_workstreams;
			CREATE TRIGGER trg_queue_task_eval_from_workstream
				AFTER UPDATE OF title, description, goal, ckp, reason, metric_name, metric_current, metric_target, status ON v2_tactical_workstreams
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_project ON v2_tactical_projects;
			CREATE TRIGGER trg_queue_task_eval_from_project
				AFTER UPDATE OF title, description, why_needed, success_criteria, failure_criteria, metric_name, status ON v2_tactical_projects
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_risk ON v2_tactical_risks;
			CREATE TRIGGER trg_queue_task_eval_from_risk
				AFTER UPDATE OF title, description, severity, status, coverage_status ON v2_tactical_risks
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_opportunity ON v2_tactical_opportunities;
			CREATE TRIGGER trg_queue_task_eval_from_opportunity
				AFTER UPDATE OF title, description, potential_impact, status, coverage_status ON v2_tactical_opportunities
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();

			INSERT INTO v2_task_evaluation_jobs (workspace_id, task_id, requested_by, status, attempts, not_before, error_text)
			SELECT workspace_id, id, updated_by, 'queued', 0, NOW(), ''
			FROM v2_tasks
			WHERE archived_at IS NULL
			ON CONFLICT (task_id) DO NOTHING;
		`,
	},
	{
		ID: "20260716_024_strategic_document_chats",
		SQL: `
			CREATE TABLE IF NOT EXISTS strategic_document_chat_messages (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				role TEXT NOT NULL,
				content TEXT NOT NULL DEFAULT '',
				metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_strategic_document_chat_messages_scope
				ON strategic_document_chat_messages (workspace_id, document_type, created_at, id);

			CREATE TABLE IF NOT EXISTS strategic_document_chat_sessions (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				document_type TEXT NOT NULL,
				previous_response_id TEXT NOT NULL DEFAULT '',
				compact_threshold INTEGER NOT NULL DEFAULT 120000,
				prompt_cache_key TEXT NOT NULL DEFAULT '',
				context_fingerprint TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id, document_type)
			);
		`,
	},
	{
		ID: "20260716_025_tactics_action_confirmations",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_tactics_action_applications (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				tactical_plan_id INTEGER NOT NULL REFERENCES v2_tactical_plans(id) ON DELETE CASCADE,
				message_id INTEGER NOT NULL REFERENCES v2_tactics_chat_messages(id) ON DELETE CASCADE,
				action_index INTEGER NOT NULL,
				operation TEXT NOT NULL,
				entity_type TEXT NOT NULL,
				entity_id INTEGER NULL,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				status TEXT NOT NULL DEFAULT 'applying',
				error_text TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(message_id, action_index)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactics_action_applications_scope
				ON v2_tactics_action_applications (workspace_id, tactical_plan_id, created_at DESC);
		`,
	},
	{
		ID: "20260716_026_tactics_product_fields",
		SQL: `
			ALTER TABLE v2_tactical_workstreams
				ADD COLUMN IF NOT EXISTS metrics_json JSONB NOT NULL DEFAULT '[]'::jsonb;
			ALTER TABLE v2_tactical_projects
				ADD COLUMN IF NOT EXISTS expected_value TEXT NOT NULL DEFAULT '';
			ALTER TABLE v2_tactical_risks
				ADD COLUMN IF NOT EXISTS probability TEXT NOT NULL DEFAULT '';
			ALTER TABLE v2_tactical_opportunities
				ADD COLUMN IF NOT EXISTS urgency TEXT NOT NULL DEFAULT '';

			UPDATE v2_tactical_workstreams
			SET metrics_json=jsonb_build_array(jsonb_build_object(
				'name', metric_name,
				'current', metric_current,
				'target', metric_target
			))
			WHERE metrics_json='[]'::jsonb AND BTRIM(metric_name) <> '';

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_workstream ON v2_tactical_workstreams;
			CREATE TRIGGER trg_queue_task_eval_from_workstream
				AFTER UPDATE OF title, description, goal, ckp, reason, metric_name, metric_current, metric_target, metrics_json, status ON v2_tactical_workstreams
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_project ON v2_tactical_projects;
			CREATE TRIGGER trg_queue_task_eval_from_project
				AFTER UPDATE OF title, description, why_needed, success_criteria, failure_criteria, metric_name, expected_value, status ON v2_tactical_projects
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_risk ON v2_tactical_risks;
			CREATE TRIGGER trg_queue_task_eval_from_risk
				AFTER UPDATE OF title, description, severity, probability, status, coverage_status ON v2_tactical_risks
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_opportunity ON v2_tactical_opportunities;
			CREATE TRIGGER trg_queue_task_eval_from_opportunity
				AFTER UPDATE OF title, description, potential_impact, urgency, status, coverage_status ON v2_tactical_opportunities
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();
		`,
	},
	{
		ID: "20260716_027_task_product_fields",
		SQL: `
			ALTER TABLE v2_tasks
				ADD COLUMN IF NOT EXISTS blocked BOOLEAN NOT NULL DEFAULT FALSE,
				ADD COLUMN IF NOT EXISTS backlog_category TEXT NOT NULL DEFAULT '';

			ALTER TABLE v2_task_evaluations
				ADD COLUMN IF NOT EXISTS flags_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				ADD COLUMN IF NOT EXISTS backlog_category TEXT NOT NULL DEFAULT '';

			CREATE TABLE IF NOT EXISTS v2_task_secondary_workstreams (
				task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				workstream_id INTEGER NOT NULL REFERENCES v2_tactical_workstreams(id) ON DELETE CASCADE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (task_id, workstream_id)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_task_secondary_workstreams_scope
				ON v2_task_secondary_workstreams (workspace_id, workstream_id, task_id);

			CREATE INDEX IF NOT EXISTS idx_v2_tasks_workspace_backlog
				ON v2_tasks (workspace_id, backlog_category, status, updated_at DESC);
		`,
	},
	{
		ID: "20260716_028_strategy_research_requests",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_strategy_research_requests (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
				source_readiness_run_id INTEGER NULL REFERENCES v2_strategy_readiness_runs(id) ON DELETE SET NULL,
				area TEXT NOT NULL DEFAULT '',
				research_goal TEXT NOT NULL,
				why_it_matters TEXT NOT NULL DEFAULT '',
				context_to_carry TEXT NOT NULL DEFAULT '',
				priority TEXT NOT NULL DEFAULT 'medium',
				blocking BOOLEAN NOT NULL DEFAULT FALSE,
				status TEXT NOT NULL DEFAULT 'proposed',
				assignee_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				result_text TEXT NOT NULL DEFAULT '',
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				updated_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				accepted_at TIMESTAMPTZ NULL,
				started_at TIMESTAMPTZ NULL,
				completed_at TIMESTAMPTZ NULL,
				rejected_at TIMESTAMPTZ NULL,
				UNIQUE(strategy_id, area, research_goal)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_strategy_research_requests_scope
				ON v2_strategy_research_requests (workspace_id, strategy_id, status, priority, updated_at DESC);
		`,
	},
	{
		ID: "20260716_029_strategic_claim_lifecycle",
		SQL: `
			UPDATE strategic_claims
			SET status='confirmed'
			WHERE status='active';

			ALTER TABLE strategic_claims
				ALTER COLUMN status SET DEFAULT 'confirmed',
				ADD COLUMN IF NOT EXISTS status_reason TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS reviewed_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ NULL;

			ALTER TABLE strategic_claims
				DROP CONSTRAINT IF EXISTS strategic_claims_status_check;
			ALTER TABLE strategic_claims
				ADD CONSTRAINT strategic_claims_status_check
				CHECK (status IN ('suggested', 'confirmed', 'rejected', 'conflicted', 'outdated'));

			CREATE INDEX IF NOT EXISTS idx_strategic_claims_superseded_by
				ON strategic_claims (workspace_id, superseded_by)
				WHERE superseded_by IS NOT NULL;
		`,
	},
	{
		ID: "20260716_030_unified_ai_actions",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_ai_actions (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				scenario TEXT NOT NULL,
				scope_type TEXT NOT NULL,
				scope_id INTEGER NOT NULL,
				message_id INTEGER NOT NULL,
				action_index INTEGER NOT NULL,
				action_type TEXT NOT NULL,
				payload_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				status TEXT NOT NULL DEFAULT 'proposed',
				entity_type TEXT NOT NULL DEFAULT '',
				entity_id INTEGER NULL,
				proposed_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				confirmed_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				edited_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				rejected_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				error_text TEXT NOT NULL DEFAULT '',
				expires_at TIMESTAMPTZ NULL,
				confirmed_at TIMESTAMPTZ NULL,
				applied_at TIMESTAMPTZ NULL,
				rejected_at TIMESTAMPTZ NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(scenario, message_id, action_index),
				CHECK (status IN ('proposed', 'confirmed', 'applied', 'rejected', 'edited', 'expired', 'failed'))
			);

			CREATE INDEX IF NOT EXISTS idx_v2_ai_actions_scope
				ON v2_ai_actions (workspace_id, scenario, scope_type, scope_id, status, updated_at DESC);
			CREATE INDEX IF NOT EXISTS idx_v2_ai_actions_message
				ON v2_ai_actions (workspace_id, scenario, message_id, action_index);

			INSERT INTO v2_ai_actions (
				workspace_id, scenario, scope_type, scope_id, message_id, action_index,
				action_type, status, entity_type, entity_id, proposed_by, confirmed_by,
				confirmed_at, applied_at, error_text, created_at, updated_at
			)
			SELECT workspace_id, 'tactics_facilitator', 'tactical_plan', tactical_plan_id,
				message_id, action_index, operation || ':' || entity_type,
				CASE WHEN status='applied' THEN 'applied' WHEN status='failed' THEN 'failed' ELSE 'confirmed' END,
				entity_type, entity_id, created_by, created_by,
				created_at, CASE WHEN status='applied' THEN updated_at ELSE NULL END,
				error_text, created_at, updated_at
			FROM v2_tactics_action_applications
			ON CONFLICT (scenario, message_id, action_index) DO NOTHING;

			INSERT INTO v2_ai_actions (
				workspace_id, scenario, scope_type, scope_id, message_id, action_index,
				action_type, status, entity_type, entity_id, proposed_by, confirmed_by,
				confirmed_at, applied_at, error_text, created_at, updated_at
			)
			SELECT workspace_id, 'task_brainstorm', 'workstream', workstream_id,
				message_id, action_index, action_type,
				CASE WHEN status='applied' THEN 'applied' WHEN status='failed' THEN 'failed' ELSE 'confirmed' END,
				'task', task_id, created_by, created_by,
				created_at, CASE WHEN status='applied' THEN updated_at ELSE NULL END,
				error_text, created_at, updated_at
			FROM v2_task_brainstorm_action_applications
			ON CONFLICT (scenario, message_id, action_index) DO NOTHING;

			INSERT INTO v2_ai_actions (
				workspace_id, scenario, scope_type, scope_id, message_id, action_index,
				action_type, payload_json, status, created_at, updated_at
			)
			SELECT message.workspace_id, 'tactics_facilitator', 'tactical_plan', plan.id,
				message.id, item.ordinality - 1,
				COALESCE(item.payload->>'operation', '') || ':' || COALESCE(item.payload->>'entity_type', ''),
				item.payload, 'proposed', message.created_at, message.created_at
			FROM v2_tactics_chat_messages message
			JOIN LATERAL jsonb_array_elements(COALESCE(message.metadata_json->'draft_changes', '[]'::jsonb))
				WITH ORDINALITY AS item(payload, ordinality) ON TRUE
			JOIN LATERAL (
				SELECT id
				FROM v2_tactical_plans
				WHERE workspace_id=message.workspace_id AND archived_at IS NULL
				ORDER BY (status='active') DESC, updated_at DESC, id DESC
				LIMIT 1
			) plan ON TRUE
			WHERE message.role='assistant'
				AND COALESCE(item.payload->>'operation', '') <> ''
			ON CONFLICT (scenario, message_id, action_index) DO UPDATE SET
				action_type=EXCLUDED.action_type,
				payload_json=EXCLUDED.payload_json;

			INSERT INTO v2_ai_actions (
				workspace_id, scenario, scope_type, scope_id, message_id, action_index,
				action_type, payload_json, status, created_at, updated_at
			)
			SELECT message.workspace_id, 'task_brainstorm', 'workstream', message.workstream_id,
				message.id, item.ordinality - 1, COALESCE(item.payload->>'action_type', ''),
				item.payload, 'proposed', message.created_at, message.created_at
			FROM v2_task_brainstorm_messages message
			JOIN LATERAL jsonb_array_elements(COALESCE(message.actions_json, '[]'::jsonb))
				WITH ORDINALITY AS item(payload, ordinality) ON TRUE
			WHERE message.role='assistant'
				AND COALESCE(item.payload->>'action_type', '') <> ''
			ON CONFLICT (scenario, message_id, action_index) DO UPDATE SET
				action_type=EXCLUDED.action_type,
				payload_json=EXCLUDED.payload_json;

			UPDATE v2_ai_actions current_action
			SET status='expired', expires_at=NOW(), updated_at=NOW()
			WHERE current_action.status IN ('proposed', 'edited')
				AND current_action.message_id < (
					SELECT MAX(latest_action.message_id)
					FROM v2_ai_actions latest_action
					WHERE latest_action.workspace_id=current_action.workspace_id
						AND latest_action.scenario=current_action.scenario
						AND latest_action.scope_type=current_action.scope_type
						AND latest_action.scope_id=current_action.scope_id
						AND latest_action.status IN ('proposed', 'edited')
				);
		`,
	},
	{
		ID: "20260717_031_operational_foundation",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_background_jobs (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				job_type TEXT NOT NULL,
				dedupe_key TEXT NOT NULL DEFAULT '',
				payload_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				status TEXT NOT NULL DEFAULT 'queued',
				attempts INTEGER NOT NULL DEFAULT 0,
				max_attempts INTEGER NOT NULL DEFAULT 5,
				not_before TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				locked_at TIMESTAMPTZ NULL,
				locked_by TEXT NOT NULL DEFAULT '',
				last_error TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				completed_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_v2_background_jobs_due
				ON v2_background_jobs (status, not_before, id);

			CREATE INDEX IF NOT EXISTS idx_v2_background_jobs_workspace
				ON v2_background_jobs (workspace_id, created_at DESC);

			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_background_jobs_active_dedupe
				ON v2_background_jobs (job_type, workspace_id, dedupe_key)
				WHERE dedupe_key <> '' AND status='queued';

			ALTER TABLE v2_ai_prompt_configs
				ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'draft',
				ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 'openai',
				ADD COLUMN IF NOT EXISTS notes TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS parent_version TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS activated_at TIMESTAMPTZ NULL,
				ADD COLUMN IF NOT EXISTS activated_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL;

			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_ai_prompt_configs_one_active
				ON v2_ai_prompt_configs (prompt_name)
				WHERE status='active';

			ALTER TABLE v2_ai_call_logs
				ADD COLUMN IF NOT EXISTS request_id TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 'openai',
				ADD COLUMN IF NOT EXISTS prompt_name TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS response_id TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS token_usage_total INTEGER NULL,
				ADD COLUMN IF NOT EXISTS cached_input_tokens INTEGER NULL;

			CREATE INDEX IF NOT EXISTS idx_v2_ai_call_logs_module_created
				ON v2_ai_call_logs (ai_module, created_at DESC);

			CREATE TABLE IF NOT EXISTS v2_ai_usage_policies (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				requests_per_minute INTEGER NOT NULL DEFAULT 0,
				daily_budget_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
				monthly_budget_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
				status TEXT NOT NULL DEFAULT 'active',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_ai_usage_policies_workspace
				ON v2_ai_usage_policies ((COALESCE(workspace_id, 0)));

			CREATE TABLE IF NOT EXISTS v2_http_request_logs (
				id BIGSERIAL PRIMARY KEY,
				request_id TEXT NOT NULL,
				workspace_id INTEGER NULL REFERENCES workspaces(id) ON DELETE SET NULL,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				method TEXT NOT NULL,
				path TEXT NOT NULL,
				status_code INTEGER NOT NULL,
				latency_ms BIGINT NOT NULL,
				response_bytes BIGINT NOT NULL DEFAULT 0,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_http_request_logs_workspace_created
				ON v2_http_request_logs (workspace_id, created_at DESC);

			CREATE INDEX IF NOT EXISTS idx_v2_http_request_logs_path_created
				ON v2_http_request_logs (path, created_at DESC);

			CREATE TABLE IF NOT EXISTS v2_product_events (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NULL REFERENCES workspaces(id) ON DELETE SET NULL,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				event_name TEXT NOT NULL,
				source TEXT NOT NULL DEFAULT 'api',
				entity_type TEXT NOT NULL DEFAULT '',
				entity_id INTEGER NULL,
				properties_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_product_events_workspace_created
				ON v2_product_events (workspace_id, created_at DESC);

			CREATE INDEX IF NOT EXISTS idx_v2_product_events_name_created
				ON v2_product_events (event_name, created_at DESC);

			CREATE TABLE IF NOT EXISTS v2_system_warnings (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				warning_key TEXT NOT NULL,
				severity TEXT NOT NULL DEFAULT 'warning',
				title TEXT NOT NULL,
				message TEXT NOT NULL DEFAULT '',
				details_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				status TEXT NOT NULL DEFAULT 'active',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				resolved_at TIMESTAMPTZ NULL
			);

			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_system_warnings_active_key
				ON v2_system_warnings (workspace_id, warning_key)
				WHERE status='active';

			ALTER TABLE v2_tactics_chat_messages
				ADD COLUMN IF NOT EXISTS scope_type TEXT NOT NULL DEFAULT 'tactical_plan',
				ADD COLUMN IF NOT EXISTS scope_id INTEGER NOT NULL DEFAULT 0;

			CREATE INDEX IF NOT EXISTS idx_v2_tactics_chat_messages_scope
				ON v2_tactics_chat_messages (workspace_id, scope_type, scope_id, created_at DESC, id DESC);

			CREATE TABLE IF NOT EXISTS v2_tactics_scope_sessions (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				scope_type TEXT NOT NULL,
				scope_id INTEGER NOT NULL DEFAULT 0,
				previous_response_id TEXT NOT NULL DEFAULT '',
				compact_threshold INTEGER NOT NULL DEFAULT 120000,
				prompt_cache_key TEXT NOT NULL DEFAULT '',
				context_fingerprint TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id, scope_type, scope_id)
			);
		`,
	},
	{
		ID: "20260717_032_remove_inactive_knowledge_pipeline",
		SQL: `
			INSERT INTO v2_tactics_scope_sessions (
				workspace_id, scope_type, scope_id, previous_response_id, compact_threshold,
				prompt_cache_key, context_fingerprint, created_at, updated_at
			)
			SELECT workspace_id, 'tactical_plan', 0, previous_response_id, compact_threshold,
				prompt_cache_key, context_fingerprint, created_at, updated_at
			FROM v2_tactics_openai_sessions
			ON CONFLICT (workspace_id, scope_type, scope_id) DO NOTHING;

			DROP TABLE IF EXISTS v2_tactics_openai_sessions;
			DROP TABLE IF EXISTS v2_knowledge_intake_progress_events;
			DROP TABLE IF EXISTS v2_knowledge_document_views;
			DROP TABLE IF EXISTS v2_knowledge_document_readiness;
			DROP TABLE IF EXISTS v2_knowledge_base_readiness;
			DROP TABLE IF EXISTS v2_guidance_question_blocks;
			DROP TABLE IF EXISTS v2_company_profiles;
			DROP TABLE IF EXISTS v2_ignored_knowledge_items;
			DROP TABLE IF EXISTS v2_proposed_document_conflicts;
			DROP TABLE IF EXISTS v2_proposed_document_patches;
			DROP TABLE IF EXISTS v2_proposed_knowledge_items;
			DROP TABLE IF EXISTS v2_knowledge_intake_sessions;
			DROP TABLE IF EXISTS v2_knowledge_document_entry_versions;
			DROP TABLE IF EXISTS v2_knowledge_document_entries;
			DROP TABLE IF EXISTS v2_knowledge_documents;
			DROP TABLE IF EXISTS v2_knowledge_base_blocks;
		`,
	},
	{
		ID: "20260718_033_privacy_foundation",
		SQL: `
			ALTER TABLE users
				ADD COLUMN IF NOT EXISTS privacy_subject_id TEXT NULL;

			UPDATE users
			SET privacy_subject_id=md5(random()::text || clock_timestamp()::text || id::text)
			WHERE privacy_subject_id IS NULL;

			ALTER TABLE users
				ALTER COLUMN privacy_subject_id SET NOT NULL;

			CREATE UNIQUE INDEX IF NOT EXISTS idx_users_privacy_subject_id
				ON users (privacy_subject_id)
				WHERE privacy_subject_id IS NOT NULL;

			CREATE TABLE IF NOT EXISTS legal_acceptances (
				id BIGSERIAL PRIMARY KEY,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				subject_key TEXT NOT NULL,
				document_type TEXT NOT NULL,
				document_version TEXT NOT NULL,
				accepted BOOLEAN NOT NULL,
				legal_basis TEXT NOT NULL,
				source TEXT NOT NULL,
				request_id TEXT NOT NULL DEFAULT '',
				recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				withdrawn_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_legal_acceptances_user_document
				ON legal_acceptances (user_id, document_type, recorded_at DESC);

			CREATE INDEX IF NOT EXISTS idx_legal_acceptances_subject
				ON legal_acceptances (subject_key, recorded_at DESC);

			CREATE TABLE IF NOT EXISTS privacy_requests (
				id BIGSERIAL PRIMARY KEY,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				workspace_id INTEGER NULL REFERENCES workspaces(id) ON DELETE SET NULL,
				subject_key TEXT NOT NULL,
				request_type TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'received',
				details TEXT NOT NULL DEFAULT '',
				resolution_summary TEXT NOT NULL DEFAULT '',
				request_id TEXT NOT NULL DEFAULT '',
				received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				due_at TIMESTAMPTZ NOT NULL,
				completed_at TIMESTAMPTZ NULL,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_privacy_requests_user_received
				ON privacy_requests (user_id, received_at DESC);

			CREATE INDEX IF NOT EXISTS idx_privacy_requests_status_due
				ON privacy_requests (status, due_at);
		`,
	},
	{
		ID: "20260720_034_profile_workspace_billing",
		SQL: `
			ALTER TABLE users
				ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS avatar_url TEXT NOT NULL DEFAULT '';

			ALTER TABLE subscriptions
				ADD COLUMN IF NOT EXISTS workspace_id INTEGER NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				ADD COLUMN IF NOT EXISTS payment_method TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS payment_provider TEXT NOT NULL DEFAULT 'cloudpayments';

			UPDATE subscriptions subscription
			SET workspace_id=(
				SELECT workspace.id
				FROM workspaces workspace
				WHERE workspace.owner_user_id=subscription.user_id AND workspace.status='active'
				ORDER BY workspace.created_at ASC
				LIMIT 1
			)
			WHERE subscription.workspace_id IS NULL;

			CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_workspace
				ON subscriptions (workspace_id)
				WHERE workspace_id IS NOT NULL;

			CREATE TABLE IF NOT EXISTS user_profile_settings (
				user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
				interface_language TEXT NOT NULL DEFAULT 'ru',
				theme TEXT NOT NULL DEFAULT 'dark',
				date_format TEXT NOT NULL DEFAULT 'DD.MM.YYYY',
				ai_language TEXT NOT NULL DEFAULT 'ru',
				email_notifications BOOLEAN NOT NULL DEFAULT TRUE,
				in_product_notifications BOOLEAN NOT NULL DEFAULT TRUE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE TABLE IF NOT EXISTS workspace_invitations (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				email TEXT NOT NULL,
				role TEXT NOT NULL DEFAULT 'member',
				status TEXT NOT NULL DEFAULT 'pending',
				invited_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				accepted_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				token_hash TEXT NOT NULL,
				expires_at TIMESTAMPTZ NOT NULL,
				accepted_at TIMESTAMPTZ NULL,
				cancelled_at TIMESTAMPTZ NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_workspace_invitations_workspace
				ON workspace_invitations (workspace_id, status, created_at DESC);

			CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_invitations_pending_email
				ON workspace_invitations (workspace_id, lower(email))
				WHERE status='pending';

			CREATE TABLE IF NOT EXISTS workspace_billing_organizations (
				workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
				full_name TEXT NOT NULL,
				inn TEXT NOT NULL,
				kpp TEXT NOT NULL DEFAULT '',
				registration_number TEXT NOT NULL,
				legal_address TEXT NOT NULL,
				accounting_email TEXT NOT NULL,
				contact_person TEXT NOT NULL,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE TABLE IF NOT EXISTS workspace_billing_invoices (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				number TEXT NOT NULL UNIQUE,
				amount NUMERIC(12,2) NOT NULL,
				currency TEXT NOT NULL DEFAULT 'RUB',
				status TEXT NOT NULL DEFAULT 'waiting',
				organization_snapshot JSONB NOT NULL,
				recipient_email TEXT NOT NULL,
				issued_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				due_at TIMESTAMPTZ NOT NULL,
				paid_at TIMESTAMPTZ NULL,
				cancelled_at TIMESTAMPTZ NULL,
				emailed_at TIMESTAMPTZ NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_workspace_billing_invoices_workspace
				ON workspace_billing_invoices (workspace_id, issued_at DESC);

			CREATE TABLE IF NOT EXISTS workspace_billing_documents (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				invoice_id BIGINT NULL REFERENCES workspace_billing_invoices(id) ON DELETE CASCADE,
				kind TEXT NOT NULL,
				title TEXT NOT NULL,
				file_name TEXT NOT NULL,
				mime_type TEXT NOT NULL DEFAULT 'application/pdf',
				content BYTEA NOT NULL,
				period_start DATE NULL,
				period_end DATE NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_workspace_billing_documents_workspace
				ON workspace_billing_documents (workspace_id, created_at DESC);

			CREATE TABLE IF NOT EXISTS workspace_billing_payments (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				invoice_id BIGINT NULL REFERENCES workspace_billing_invoices(id) ON DELETE SET NULL,
				provider TEXT NOT NULL,
				external_id TEXT NOT NULL DEFAULT '',
				method TEXT NOT NULL,
				amount NUMERIC(12,2) NOT NULL,
				currency TEXT NOT NULL DEFAULT 'RUB',
				status TEXT NOT NULL,
				paid_at TIMESTAMPTZ NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_workspace_billing_payments_workspace
				ON workspace_billing_payments (workspace_id, created_at DESC);
		`,
	},
	{
		ID: "20260721_035_openai_conversations_and_workspace_context",
		SQL: `
			ALTER TABLE strategic_openai_sessions
				ADD COLUMN IF NOT EXISTS conversation_id TEXT NOT NULL DEFAULT '';
			ALTER TABLE v2_strategy_openai_sessions
				ADD COLUMN IF NOT EXISTS conversation_id TEXT NOT NULL DEFAULT '';
			ALTER TABLE strategic_document_chat_sessions
				ADD COLUMN IF NOT EXISTS conversation_id TEXT NOT NULL DEFAULT '';
			ALTER TABLE v2_tactics_scope_sessions
				ADD COLUMN IF NOT EXISTS conversation_id TEXT NOT NULL DEFAULT '';
			ALTER TABLE v2_task_brainstorm_sessions
				ADD COLUMN IF NOT EXISTS conversation_id TEXT NOT NULL DEFAULT '';

			CREATE TABLE IF NOT EXISTS v2_ai_workspace_context_files (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				content_hash TEXT NOT NULL,
				openai_file_id TEXT NOT NULL,
				vector_store_id TEXT NOT NULL,
				vector_store_file_id TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'processing',
				error TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE(workspace_id, content_hash)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_ai_workspace_context_active
				ON v2_ai_workspace_context_files (workspace_id, status, updated_at DESC);
		`,
	},
	{
		ID: "20260721_036_deferred_knowledge_compilation",
		SQL: `
			ALTER TABLE strategic_openai_sessions
				ADD COLUMN IF NOT EXISTS prompt_version TEXT NOT NULL DEFAULT '';

			CREATE TABLE IF NOT EXISTS strategic_knowledge_pipeline_state (
				workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
				status TEXT NOT NULL DEFAULT 'collecting',
				conversation_revision INTEGER NOT NULL DEFAULT 0,
				last_user_source_id INTEGER NOT NULL DEFAULT 0,
				last_extracted_source_id INTEGER NOT NULL DEFAULT 0,
				last_audited_source_id INTEGER NOT NULL DEFAULT 0,
				candidate_revision INTEGER NOT NULL DEFAULT 0,
				candidate_source_id INTEGER NOT NULL DEFAULT 0,
				ready_revision INTEGER NOT NULL DEFAULT 0,
				compiled_revision INTEGER NOT NULL DEFAULT 0,
				candidate_reason TEXT NOT NULL DEFAULT '',
				audit_feedback_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				candidate_report_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				feedback_delivered_revision INTEGER NOT NULL DEFAULT 0,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CHECK (status IN (
					'collecting', 'audit_candidate', 'extracting', 'reviewing',
					'needs_more_context', 'compiling_documents', 'ready'
				))
			);

			INSERT INTO strategic_knowledge_pipeline_state (
				workspace_id, status, conversation_revision, last_user_source_id,
				ready_revision, compiled_revision
			)
			SELECT
				session.workspace_id,
				CASE
					WHEN COALESCE((
						SELECT (report.report_json->'strategy_gate'->>'can_start_strategy')::boolean
						FROM strategic_quality_reports report
						WHERE report.workspace_id=session.workspace_id
						ORDER BY report.created_at DESC, report.id DESC
						LIMIT 1
					), false)
					AND EXISTS (
						SELECT 1 FROM strategic_documents document
						WHERE document.workspace_id=session.workspace_id AND BTRIM(document.markdown)<>''
					)
					THEN 'ready'
					ELSE 'collecting'
				END,
				COALESCE(source_stats.user_turns, 0),
				COALESCE(source_stats.last_user_source_id, 0),
				CASE WHEN EXISTS (
					SELECT 1 FROM strategic_documents document
					WHERE document.workspace_id=session.workspace_id AND BTRIM(document.markdown)<>''
				) THEN COALESCE(source_stats.user_turns, 0) ELSE 0 END,
				CASE WHEN EXISTS (
					SELECT 1 FROM strategic_documents document
					WHERE document.workspace_id=session.workspace_id AND BTRIM(document.markdown)<>''
				) THEN COALESCE(source_stats.user_turns, 0) ELSE 0 END
			FROM strategic_openai_sessions session
			LEFT JOIN LATERAL (
				SELECT COUNT(*)::integer AS user_turns, COALESCE(MAX(id), 0)::integer AS last_user_source_id
				FROM strategic_raw_sources source
				WHERE source.workspace_id=session.workspace_id AND source.source_type='user_message'
			) source_stats ON true
			ON CONFLICT (workspace_id) DO NOTHING;
		`,
	},
	{
		ID: "20260721_037_departments_and_responsibility",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_departments (
				id SERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				name TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				responsibility TEXT NOT NULL DEFAULT '',
				manager_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				kpis_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				status TEXT NOT NULL DEFAULT 'active',
				sort_order INTEGER NOT NULL DEFAULT 0,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL,
				CHECK (status IN ('active', 'archived'))
			);

			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_departments_workspace_name
				ON v2_departments (workspace_id, lower(name))
				WHERE archived_at IS NULL;
			CREATE INDEX IF NOT EXISTS idx_v2_departments_workspace
				ON v2_departments (workspace_id, status, sort_order, id);

			CREATE TABLE IF NOT EXISTS v2_department_members (
				department_id INTEGER NOT NULL REFERENCES v2_departments(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				role TEXT NOT NULL DEFAULT 'member',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (department_id, user_id),
				CHECK (role IN ('manager', 'member'))
			);
			CREATE INDEX IF NOT EXISTS idx_v2_department_members_workspace
				ON v2_department_members (workspace_id, user_id);

			CREATE TABLE IF NOT EXISTS v2_workstream_departments (
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				workstream_id INTEGER NOT NULL REFERENCES v2_tactical_workstreams(id) ON DELETE CASCADE,
				department_id INTEGER NOT NULL REFERENCES v2_departments(id) ON DELETE CASCADE,
				role TEXT NOT NULL DEFAULT 'participant',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (workstream_id, department_id),
				CHECK (role IN ('lead', 'participant'))
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_workstream_departments_lead
				ON v2_workstream_departments (workstream_id)
				WHERE role='lead';

			CREATE TABLE IF NOT EXISTS v2_project_departments (
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				project_id INTEGER NOT NULL REFERENCES v2_tactical_projects(id) ON DELETE CASCADE,
				department_id INTEGER NOT NULL REFERENCES v2_departments(id) ON DELETE CASCADE,
				role TEXT NOT NULL DEFAULT 'participant',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (project_id, department_id),
				CHECK (role IN ('lead', 'participant'))
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_project_departments_lead
				ON v2_project_departments (project_id)
				WHERE role='lead';

			CREATE TABLE IF NOT EXISTS v2_entity_document_links (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				entity_type TEXT NOT NULL,
				entity_id INTEGER NOT NULL,
				document_id INTEGER NOT NULL REFERENCES strategic_documents(id) ON DELETE CASCADE,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE (workspace_id, entity_type, entity_id, document_id),
				CHECK (entity_type IN ('department', 'workstream', 'project'))
			);
			CREATE INDEX IF NOT EXISTS idx_v2_entity_document_links_entity
				ON v2_entity_document_links (workspace_id, entity_type, entity_id);

			ALTER TABLE v2_tasks
				ADD COLUMN IF NOT EXISTS department_id INTEGER NULL REFERENCES v2_departments(id) ON DELETE RESTRICT;
			CREATE INDEX IF NOT EXISTS idx_v2_tasks_department
				ON v2_tasks (workspace_id, department_id, status, updated_at DESC);

			INSERT INTO v2_departments (
				workspace_id, name, description, responsibility, manager_user_id, status, sort_order, created_by
			)
			SELECT workspace.id, 'Компания', 'Общая команда workspace',
				'Ответственность по умолчанию до создания функциональных отделов.',
				workspace.owner_user_id, 'active', 0, workspace.owner_user_id
			FROM workspaces workspace
			WHERE workspace.archived_at IS NULL
			ON CONFLICT DO NOTHING;

			INSERT INTO v2_department_members (department_id, workspace_id, user_id, role)
			SELECT department.id, department.workspace_id, workspace.owner_user_id, 'manager'
			FROM v2_departments department
			JOIN workspaces workspace ON workspace.id=department.workspace_id
			WHERE department.archived_at IS NULL
			ON CONFLICT (department_id, user_id) DO NOTHING;

			UPDATE v2_tasks task
			SET department_id=(
				SELECT candidate.id
				FROM v2_departments candidate
				WHERE candidate.workspace_id=task.workspace_id AND candidate.archived_at IS NULL
				ORDER BY candidate.sort_order ASC, candidate.id ASC
				LIMIT 1
			)
			WHERE task.department_id IS NULL;

			INSERT INTO v2_workstream_departments (workspace_id, workstream_id, department_id, role)
			SELECT workstream.workspace_id, workstream.id, department.id, 'lead'
			FROM v2_tactical_workstreams workstream
			JOIN LATERAL (
				SELECT id
				FROM v2_departments candidate
				WHERE candidate.workspace_id=workstream.workspace_id AND candidate.archived_at IS NULL
				ORDER BY candidate.sort_order ASC, candidate.id ASC
				LIMIT 1
			) department ON true
			WHERE workstream.archived_at IS NULL
			ON CONFLICT (workstream_id, department_id) DO NOTHING;

			INSERT INTO v2_project_departments (workspace_id, project_id, department_id, role)
			SELECT project.workspace_id, project.id, department.id, 'lead'
			FROM v2_tactical_projects project
			JOIN LATERAL (
				SELECT id
				FROM v2_departments candidate
				WHERE candidate.workspace_id=project.workspace_id AND candidate.archived_at IS NULL
				ORDER BY candidate.sort_order ASC, candidate.id ASC
				LIMIT 1
			) department ON true
			WHERE project.archived_at IS NULL
			ON CONFLICT (project_id, department_id) DO NOTHING;

			CREATE OR REPLACE FUNCTION v2_attach_default_workstream_department()
			RETURNS TRIGGER AS $$
			BEGIN
				INSERT INTO v2_workstream_departments (workspace_id, workstream_id, department_id, role)
				SELECT NEW.workspace_id, NEW.id, department.id, 'lead'
				FROM v2_departments department
				WHERE department.workspace_id=NEW.workspace_id AND department.archived_at IS NULL
				ORDER BY department.sort_order ASC, department.id ASC
				LIMIT 1
				ON CONFLICT (workstream_id, department_id) DO NOTHING;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql;

			DROP TRIGGER IF EXISTS trg_v2_workstream_default_department ON v2_tactical_workstreams;
			CREATE TRIGGER trg_v2_workstream_default_department
			AFTER INSERT ON v2_tactical_workstreams
			FOR EACH ROW EXECUTE FUNCTION v2_attach_default_workstream_department();

			CREATE OR REPLACE FUNCTION v2_attach_default_project_department()
			RETURNS TRIGGER AS $$
			BEGIN
				INSERT INTO v2_project_departments (workspace_id, project_id, department_id, role)
				SELECT NEW.workspace_id, NEW.id, department.id, 'lead'
				FROM v2_departments department
				WHERE department.workspace_id=NEW.workspace_id AND department.archived_at IS NULL
				ORDER BY department.sort_order ASC, department.id ASC
				LIMIT 1
				ON CONFLICT (project_id, department_id) DO NOTHING;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql;

			DROP TRIGGER IF EXISTS trg_v2_project_default_department ON v2_tactical_projects;
			CREATE TRIGGER trg_v2_project_default_department
			AFTER INSERT ON v2_tactical_projects
			FOR EACH ROW EXECUTE FUNCTION v2_attach_default_project_department();
		`,
	},
	{
		ID: "20260722_038_workspace_documents",
		SQL: `
			CREATE TABLE IF NOT EXISTS workspace_documents (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				parent_id BIGINT NULL REFERENCES workspace_documents(id) ON DELETE SET NULL,
				title TEXT NOT NULL,
				content TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'draft',
				favorite BOOLEAN NOT NULL DEFAULT false,
				linked_department_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
				linked_workstream_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
				linked_project_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
				version INTEGER NOT NULL DEFAULT 1,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				updated_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL,
				CHECK (status IN ('draft', 'published', 'archived')),
				CHECK (char_length(title) BETWEEN 1 AND 240),
				CHECK (char_length(content) <= 1000000)
			);
			CREATE INDEX IF NOT EXISTS idx_workspace_documents_workspace
				ON workspace_documents (workspace_id, status, favorite DESC, updated_at DESC, id DESC);
			CREATE INDEX IF NOT EXISTS idx_workspace_documents_parent
				ON workspace_documents (workspace_id, parent_id, updated_at DESC);

			CREATE TABLE IF NOT EXISTS workspace_document_versions (
				id BIGSERIAL PRIMARY KEY,
				document_id BIGINT NOT NULL REFERENCES workspace_documents(id) ON DELETE CASCADE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				version INTEGER NOT NULL,
				title TEXT NOT NULL,
				content TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL,
				favorite BOOLEAN NOT NULL DEFAULT false,
				linked_department_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
				linked_workstream_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
				linked_project_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
				saved_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE (document_id, version)
			);
			CREATE INDEX IF NOT EXISTS idx_workspace_document_versions_document
				ON workspace_document_versions (workspace_id, document_id, version DESC);
		`,
	},
	{
		ID: "20260722_039_task_completion_context",
		SQL: `
			ALTER TABLE v2_tasks
				ADD COLUMN IF NOT EXISTS completion_result TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS completion_evidence TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS completion_learning TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS hypothesis_outcome TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS next_step TEXT NOT NULL DEFAULT '';

			ALTER TABLE v2_tasks
				DROP CONSTRAINT IF EXISTS v2_tasks_hypothesis_outcome_check;
			ALTER TABLE v2_tasks
				ADD CONSTRAINT v2_tasks_hypothesis_outcome_check
				CHECK (hypothesis_outcome IN ('', 'confirmed', 'disproved', 'unclear', 'not_applicable'));
		`,
	},
	{
		ID: "20260722_040_task_lifecycle_consistency",
		SQL: `
			UPDATE v2_tasks
			SET completed_at=NULL
			WHERE status NOT IN ('done', 'archived') AND completed_at IS NOT NULL;

			UPDATE v2_tasks
			SET archived_at=NULL
			WHERE status <> 'archived' AND archived_at IS NOT NULL;
		`,
	},
	{
		ID: "20260722_041_task_dependencies",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_task_dependencies (
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
				blocker_task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (task_id, blocker_task_id),
				CHECK (task_id <> blocker_task_id)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_task_dependencies_workspace_task
				ON v2_task_dependencies (workspace_id, task_id);
			CREATE INDEX IF NOT EXISTS idx_v2_task_dependencies_workspace_blocker
				ON v2_task_dependencies (workspace_id, blocker_task_id);
		`,
	},
	{
		ID: "20260722_042_task_completion_review_and_priority_scale",
		SQL: `
			UPDATE v2_task_evaluations
			SET strategic_relevance=strategic_relevance * 10,
				course_alignment=course_alignment * 10,
				tactical_alignment=tactical_alignment * 10,
				expected_impact=expected_impact * 10,
				urgency=urgency * 10,
				effort=effort * 10,
				confidence=confidence * 10,
				priority_score=priority_score * 10
			WHERE strategic_relevance <= 100
				AND course_alignment <= 100
				AND tactical_alignment <= 100
				AND expected_impact <= 100
				AND urgency <= 100
				AND effort <= 100
				AND confidence <= 100
				AND priority_score <= 100;

			UPDATE v2_tasks
			SET manual_priority_score=NULL,
				manual_priority_tier=''
			WHERE manual_priority_score IS NOT NULL OR manual_priority_tier <> '';

			CREATE TABLE IF NOT EXISTS v2_task_completion_files (
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
				strategic_file_id INTEGER NOT NULL REFERENCES strategic_openai_files(id) ON DELETE CASCADE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (task_id, strategic_file_id)
			);
			CREATE INDEX IF NOT EXISTS idx_v2_task_completion_files_workspace_task
				ON v2_task_completion_files (workspace_id, task_id);

			CREATE TABLE IF NOT EXISTS v2_task_completion_evaluations (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
				model TEXT NOT NULL DEFAULT '',
				prompt_version TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'ready',
				sufficient BOOLEAN NOT NULL DEFAULT false,
				quality_score INTEGER NOT NULL DEFAULT 0,
				reason TEXT NOT NULL DEFAULT '',
				missing_information_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				duration_ms BIGINT NOT NULL DEFAULT 0,
				error_text TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CHECK (status IN ('ready', 'failed')),
				CHECK (quality_score BETWEEN 0 AND 1000)
			);
			CREATE INDEX IF NOT EXISTS idx_v2_task_completion_evaluations_latest
				ON v2_task_completion_evaluations (workspace_id, task_id, created_at DESC, id DESC);
		`,
	},
	{
		ID: "20260723_043_strategic_claim_importance",
		SQL: `
			ALTER TABLE strategic_claims
				ADD COLUMN IF NOT EXISTS importance TEXT NOT NULL DEFAULT 'medium';

			ALTER TABLE strategic_claims
				DROP CONSTRAINT IF EXISTS strategic_claims_importance_check;
			ALTER TABLE strategic_claims
				ADD CONSTRAINT strategic_claims_importance_check
				CHECK (importance IN ('low', 'medium', 'high', 'critical'));

			CREATE INDEX IF NOT EXISTS idx_strategic_claims_workspace_importance
				ON strategic_claims (workspace_id, status, importance, updated_at DESC, id DESC);

			ALTER TABLE strategic_raw_sources
				ADD COLUMN IF NOT EXISTS entity_key TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS content_hash TEXT NOT NULL DEFAULT '';

			CREATE UNIQUE INDEX IF NOT EXISTS idx_strategic_raw_sources_entity_content
				ON strategic_raw_sources (workspace_id, source_type, entity_key, content_hash)
				WHERE entity_key <> '' AND content_hash <> '';
		`,
	},
	{
		ID: "20260723_044_knowledge_extractor_nano",
		SQL: `
			UPDATE v2_ai_prompt_configs
			SET model='gpt-5.4-nano', updated_at=NOW()
			WHERE prompt_name='knowledge_base_deferred_extractor'
				AND provider='openai'
				AND model='gpt-5.4-mini';
		`,
	},
	{
		ID: "20260723_045_personal_tactics_advisor_threads",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_tactics_advisor_threads (
				id BIGSERIAL PRIMARY KEY,
				workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				scope_type TEXT NOT NULL DEFAULT 'workspace',
				scope_id BIGINT NOT NULL DEFAULT 0,
				scope_label TEXT NOT NULL DEFAULT '',
				title TEXT NOT NULL DEFAULT 'Новый разговор',
				status TEXT NOT NULL DEFAULT 'active',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ,
				CHECK (scope_type IN ('workspace', 'workstream', 'project', 'department')),
				CHECK (status IN ('active', 'archived'))
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactics_advisor_threads_owner
				ON v2_tactics_advisor_threads (workspace_id, user_id, status, updated_at DESC, id DESC);
			CREATE INDEX IF NOT EXISTS idx_v2_tactics_advisor_threads_scope
				ON v2_tactics_advisor_threads (workspace_id, user_id, scope_type, scope_id, updated_at DESC);
		`,
	},
	{
		ID: "20260724_046_metrics_hypotheses_and_risk_links",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_workspace_metrics (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				template_key TEXT NOT NULL DEFAULT '',
				name TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				category TEXT NOT NULL DEFAULT 'custom',
				unit TEXT NOT NULL DEFAULT 'number',
				value_type TEXT NOT NULL DEFAULT 'number',
				better_direction TEXT NOT NULL DEFAULT 'increase',
				formula TEXT NOT NULL DEFAULT '',
				is_custom BOOLEAN NOT NULL DEFAULT false,
				status TEXT NOT NULL DEFAULT 'active',
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL,
				CHECK (value_type IN ('number', 'percent', 'currency', 'duration', 'ratio')),
				CHECK (better_direction IN ('increase', 'decrease', 'range')),
				CHECK (status IN ('active', 'archived'))
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_workspace_metrics_template
				ON v2_workspace_metrics (workspace_id, template_key)
				WHERE template_key <> '' AND archived_at IS NULL;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_workspace_metrics_name
				ON v2_workspace_metrics (workspace_id, LOWER(name))
				WHERE archived_at IS NULL;
			CREATE INDEX IF NOT EXISTS idx_v2_workspace_metrics_catalog
				ON v2_workspace_metrics (workspace_id, category, name);

			CREATE TABLE IF NOT EXISTS v2_metric_targets (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				metric_id BIGINT NOT NULL REFERENCES v2_workspace_metrics(id) ON DELETE CASCADE,
				scope_type TEXT NOT NULL,
				scope_id INTEGER NOT NULL,
				role TEXT NOT NULL DEFAULT 'supporting',
				baseline_value NUMERIC NULL,
				target_value NUMERIC NULL,
				target_date DATE NULL,
				display_unit TEXT NOT NULL DEFAULT '',
				cadence TEXT NOT NULL DEFAULT 'monthly',
				source_note TEXT NOT NULL DEFAULT '',
				owner_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL,
				CHECK (scope_type IN ('workspace', 'strategy', 'workstream', 'project')),
				CHECK (role IN ('primary', 'guardrail', 'supporting')),
				CHECK (cadence IN ('daily', 'weekly', 'monthly', 'quarterly', 'on_demand'))
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_metric_targets_scope_metric
				ON v2_metric_targets (workspace_id, metric_id, scope_type, scope_id)
				WHERE archived_at IS NULL;
			CREATE INDEX IF NOT EXISTS idx_v2_metric_targets_scope
				ON v2_metric_targets (workspace_id, scope_type, scope_id, role, id);

			CREATE TABLE IF NOT EXISTS v2_metric_observations (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				metric_id BIGINT NOT NULL REFERENCES v2_workspace_metrics(id) ON DELETE CASCADE,
				target_id BIGINT NULL REFERENCES v2_metric_targets(id) ON DELETE SET NULL,
				value NUMERIC NOT NULL,
				measured_at DATE NOT NULL DEFAULT CURRENT_DATE,
				source_type TEXT NOT NULL DEFAULT 'manual',
				source_note TEXT NOT NULL DEFAULT '',
				evidence_url TEXT NOT NULL DEFAULT '',
				confidence INTEGER NOT NULL DEFAULT 1000,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CHECK (source_type IN ('manual', 'task_result', 'integration', 'ai_suggestion')),
				CHECK (confidence BETWEEN 0 AND 1000)
			);
			CREATE INDEX IF NOT EXISTS idx_v2_metric_observations_history
				ON v2_metric_observations (workspace_id, metric_id, measured_at DESC, id DESC);
			CREATE INDEX IF NOT EXISTS idx_v2_metric_observations_target
				ON v2_metric_observations (workspace_id, target_id, measured_at DESC, id DESC);

			ALTER TABLE v2_tactical_risks
				ADD COLUMN IF NOT EXISTS probability_value INTEGER NULL,
				ADD COLUMN IF NOT EXISTS impact_score INTEGER NULL,
				ADD COLUMN IF NOT EXISTS economic_exposure NUMERIC NULL,
				ADD COLUMN IF NOT EXISTS currency TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS owner_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				ADD COLUMN IF NOT EXISTS leading_indicators TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS mitigation_plan TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS contingency_plan TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS realized_at TIMESTAMPTZ NULL;
			ALTER TABLE v2_tactical_risks
				DROP CONSTRAINT IF EXISTS v2_tactical_risks_probability_value_check;
			ALTER TABLE v2_tactical_risks
				ADD CONSTRAINT v2_tactical_risks_probability_value_check
				CHECK (probability_value IS NULL OR probability_value BETWEEN 0 AND 100);
			ALTER TABLE v2_tactical_risks
				DROP CONSTRAINT IF EXISTS v2_tactical_risks_impact_score_check;
			ALTER TABLE v2_tactical_risks
				ADD CONSTRAINT v2_tactical_risks_impact_score_check
				CHECK (impact_score IS NULL OR impact_score BETWEEN 1 AND 5);

			CREATE TABLE IF NOT EXISTS v2_tactical_hypotheses (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				tactical_plan_id INTEGER NOT NULL REFERENCES v2_tactical_plans(id) ON DELETE CASCADE,
				entity_type TEXT NOT NULL,
				entity_id INTEGER NOT NULL,
				title TEXT NOT NULL,
				statement TEXT NOT NULL DEFAULT '',
				expected_effect TEXT NOT NULL DEFAULT '',
				metric_target_id BIGINT NULL REFERENCES v2_metric_targets(id) ON DELETE SET NULL,
				test_method TEXT NOT NULL DEFAULT '',
				confidence INTEGER NOT NULL DEFAULT 500,
				status TEXT NOT NULL DEFAULT 'draft',
				evidence TEXT NOT NULL DEFAULT '',
				owner_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				source TEXT NOT NULL DEFAULT 'manual',
				legacy_opportunity_id INTEGER NULL REFERENCES v2_tactical_opportunities(id) ON DELETE SET NULL,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				archived_at TIMESTAMPTZ NULL,
				CHECK (entity_type IN ('workstream', 'project')),
				CHECK (confidence BETWEEN 0 AND 1000),
				CHECK (status IN ('draft', 'ready', 'testing', 'confirmed', 'disproved', 'inconclusive', 'archived'))
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_tactical_hypotheses_legacy
				ON v2_tactical_hypotheses (workspace_id, legacy_opportunity_id)
				WHERE legacy_opportunity_id IS NOT NULL;
			CREATE INDEX IF NOT EXISTS idx_v2_tactical_hypotheses_scope
				ON v2_tactical_hypotheses (workspace_id, tactical_plan_id, entity_type, entity_id, status);

			CREATE TABLE IF NOT EXISTS v2_task_risks (
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
				risk_id INTEGER NOT NULL REFERENCES v2_tactical_risks(id) ON DELETE CASCADE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (task_id, risk_id)
			);
			CREATE INDEX IF NOT EXISTS idx_v2_task_risks_risk
				ON v2_task_risks (workspace_id, risk_id, task_id);

			CREATE TABLE IF NOT EXISTS v2_task_hypotheses (
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
				hypothesis_id BIGINT NOT NULL REFERENCES v2_tactical_hypotheses(id) ON DELETE CASCADE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (task_id, hypothesis_id)
			);
			CREATE INDEX IF NOT EXISTS idx_v2_task_hypotheses_hypothesis
				ON v2_task_hypotheses (workspace_id, hypothesis_id, task_id);

			ALTER TABLE v2_tasks
				ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'execution';
			ALTER TABLE v2_tasks
				DROP CONSTRAINT IF EXISTS v2_tasks_purpose_check;
			ALTER TABLE v2_tasks
				ADD CONSTRAINT v2_tasks_purpose_check
				CHECK (purpose IN ('execution', 'hypothesis_test', 'risk_mitigation'));

			INSERT INTO v2_task_risks (workspace_id, task_id, risk_id)
			SELECT workspace_id, id, risk_id
			FROM v2_tasks
			WHERE risk_id IS NOT NULL
			ON CONFLICT DO NOTHING;

			INSERT INTO v2_tactical_hypotheses (
				workspace_id, tactical_plan_id, entity_type, entity_id, title, statement,
				expected_effect, confidence, status, source, legacy_opportunity_id, created_by,
				created_at, updated_at
			)
			SELECT workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
				potential_impact, 500,
				CASE WHEN status IN ('archived', 'completed') THEN 'archived' ELSE 'ready' END,
				source, id, created_by, created_at, updated_at
			FROM v2_tactical_opportunities
			WHERE archived_at IS NULL AND entity_type IN ('workstream', 'project')
			ON CONFLICT DO NOTHING;

			INSERT INTO v2_task_hypotheses (workspace_id, task_id, hypothesis_id)
			SELECT task.workspace_id, task.id, hypothesis.id
			FROM v2_tasks task
			JOIN v2_tactical_hypotheses hypothesis
				ON hypothesis.workspace_id=task.workspace_id
				AND hypothesis.legacy_opportunity_id=task.opportunity_id
			WHERE task.opportunity_id IS NOT NULL
			ON CONFLICT DO NOTHING;

			UPDATE v2_tasks
			SET purpose=CASE
				WHEN risk_id IS NOT NULL THEN 'risk_mitigation'
				WHEN opportunity_id IS NOT NULL THEN 'hypothesis_test'
				ELSE purpose
			END
			WHERE risk_id IS NOT NULL OR opportunity_id IS NOT NULL;

			INSERT INTO v2_workspace_metrics (
				workspace_id, name, category, unit, value_type, better_direction, formula,
				is_custom, status, created_by
			)
			SELECT DISTINCT ON (workstream.workspace_id, LOWER(BTRIM(metric.value->>'name')))
				workstream.workspace_id,
				BTRIM(metric.value->>'name'),
				'custom', 'number', 'number', 'increase', '', true, 'active', workstream.created_by
			FROM v2_tactical_workstreams workstream
			CROSS JOIN LATERAL jsonb_array_elements(workstream.metrics_json) metric(value)
			WHERE workstream.archived_at IS NULL AND BTRIM(metric.value->>'name') <> ''
			ORDER BY workstream.workspace_id, LOWER(BTRIM(metric.value->>'name')), workstream.id
			ON CONFLICT DO NOTHING;

			INSERT INTO v2_metric_targets (
				workspace_id, metric_id, scope_type, scope_id, role,
				baseline_value, target_value, cadence, source_note, created_by
			)
			SELECT
				workstream.workspace_id, workspace_metric.id, 'workstream', workstream.id, 'primary',
				CASE WHEN BTRIM(metric.value->>'current') ~ '^-?[0-9]+([.,][0-9]+)?$'
					THEN REPLACE(BTRIM(metric.value->>'current'), ',', '.')::NUMERIC ELSE NULL END,
				CASE WHEN BTRIM(metric.value->>'target') ~ '^-?[0-9]+([.,][0-9]+)?$'
					THEN REPLACE(BTRIM(metric.value->>'target'), ',', '.')::NUMERIC ELSE NULL END,
				'monthly',
				CASE WHEN BTRIM(metric.value->>'current') <> '' OR BTRIM(metric.value->>'target') <> ''
					THEN CONCAT('Импортировано: ', metric.value->>'current', ' → ', metric.value->>'target') ELSE '' END,
				workstream.created_by
			FROM v2_tactical_workstreams workstream
			CROSS JOIN LATERAL jsonb_array_elements(workstream.metrics_json) metric(value)
			JOIN v2_workspace_metrics workspace_metric
				ON workspace_metric.workspace_id=workstream.workspace_id
				AND LOWER(workspace_metric.name)=LOWER(BTRIM(metric.value->>'name'))
				AND workspace_metric.archived_at IS NULL
			WHERE workstream.archived_at IS NULL AND BTRIM(metric.value->>'name') <> ''
			ON CONFLICT DO NOTHING;

			INSERT INTO v2_workspace_metrics (
				workspace_id, name, category, unit, value_type, better_direction, formula,
				is_custom, status, created_by
			)
			SELECT DISTINCT ON (project.workspace_id, LOWER(BTRIM(project.metric_name)))
				project.workspace_id, BTRIM(project.metric_name), 'custom', 'number', 'number',
				'increase', '', true, 'active', project.created_by
			FROM v2_tactical_projects project
			WHERE project.archived_at IS NULL AND BTRIM(project.metric_name) <> ''
			ORDER BY project.workspace_id, LOWER(BTRIM(project.metric_name)), project.id
			ON CONFLICT DO NOTHING;

			INSERT INTO v2_metric_targets (
				workspace_id, metric_id, scope_type, scope_id, role, cadence, source_note, created_by
			)
			SELECT project.workspace_id, workspace_metric.id, 'project', project.id, 'primary',
				'monthly', project.expected_value, project.created_by
			FROM v2_tactical_projects project
			JOIN v2_workspace_metrics workspace_metric
				ON workspace_metric.workspace_id=project.workspace_id
				AND LOWER(workspace_metric.name)=LOWER(BTRIM(project.metric_name))
				AND workspace_metric.archived_at IS NULL
			WHERE project.archived_at IS NULL AND BTRIM(project.metric_name) <> ''
			ON CONFLICT DO NOTHING;
		`,
	},
	{
		ID: "20260724_047_task_focus_decisions",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_task_focus_decisions (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				strategy_id INTEGER NULL REFERENCES v2_strategies(id) ON DELETE SET NULL,
				user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
				scope_type TEXT NOT NULL CHECK (scope_type IN ('workspace', 'workstream', 'project')),
				scope_id INTEGER NOT NULL DEFAULT 0,
				chosen_score INTEGER NOT NULL DEFAULT 0 CHECK (chosen_score BETWEEN 0 AND 1000),
				top_score INTEGER NOT NULL DEFAULT 0 CHECK (top_score BETWEEN 0 AND 1000),
				chosen_rank INTEGER NOT NULL DEFAULT 1 CHECK (chosen_rank > 0),
				aligned BOOLEAN NOT NULL DEFAULT FALSE,
				top_task_id INTEGER NULL REFERENCES v2_tasks(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_task_focus_decisions_scope
				ON v2_task_focus_decisions (
					workspace_id, strategy_id, scope_type, scope_id, created_at DESC, id DESC
				);

			CREATE INDEX IF NOT EXISTS idx_v2_task_focus_decisions_task
				ON v2_task_focus_decisions (workspace_id, task_id, created_at DESC);
		`,
	},
	{
		ID: "20260725_048_tactical_entity_evaluations",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_tactical_entity_evaluations (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				entity_type TEXT NOT NULL CHECK (entity_type IN ('workstream', 'project')),
				entity_id INTEGER NOT NULL,
				model TEXT NOT NULL DEFAULT '',
				prompt_version TEXT NOT NULL DEFAULT '',
				strategic_relevance INTEGER NOT NULL CHECK (strategic_relevance BETWEEN 0 AND 1000),
				expected_impact INTEGER NOT NULL CHECK (expected_impact BETWEEN 0 AND 1000),
				clarity INTEGER NOT NULL CHECK (clarity BETWEEN 0 AND 1000),
				feasibility INTEGER NOT NULL CHECK (feasibility BETWEEN 0 AND 1000),
				measurability INTEGER NOT NULL CHECK (measurability BETWEEN 0 AND 1000),
				confidence INTEGER NOT NULL CHECK (confidence BETWEEN 0 AND 1000),
				priority_score INTEGER NOT NULL CHECK (priority_score BETWEEN 0 AND 1000),
				priority_tier TEXT NOT NULL CHECK (priority_tier IN ('P1', 'P2', 'P3', 'P4', 'P5')),
				priority_reason TEXT NOT NULL DEFAULT '',
				missing_information_json JSONB NOT NULL DEFAULT '[]'::jsonb,
				context_fingerprint TEXT NOT NULL DEFAULT '',
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				duration_ms BIGINT NOT NULL DEFAULT 0,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactical_entity_evaluations_latest
				ON v2_tactical_entity_evaluations (
					workspace_id, entity_type, entity_id, created_at DESC, id DESC
				);

			CREATE TABLE IF NOT EXISTS v2_tactical_entity_evaluation_jobs (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				entity_type TEXT NOT NULL CHECK (entity_type IN ('workstream', 'project')),
				entity_id INTEGER NOT NULL,
				requested_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				status TEXT NOT NULL DEFAULT 'queued',
				attempts INTEGER NOT NULL DEFAULT 0,
				revision INTEGER NOT NULL DEFAULT 1,
				running_revision INTEGER NOT NULL DEFAULT 0,
				not_before TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				error_text TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE (workspace_id, entity_type, entity_id)
			);

			CREATE INDEX IF NOT EXISTS idx_v2_tactical_entity_evaluation_jobs_due
				ON v2_tactical_entity_evaluation_jobs (status, not_before, id);
		`,
	},
	{
		ID: "20260725_049_workspace_team_and_entity_documents",
		SQL: `
			ALTER TABLE users
				ADD COLUMN IF NOT EXISTS company_role TEXT NOT NULL DEFAULT '';

			ALTER TABLE subscriptions
				ADD COLUMN IF NOT EXISTS member_limit INTEGER NOT NULL DEFAULT 5;

			UPDATE subscriptions
			SET member_limit=CASE
				WHEN LOWER(plan_name) LIKE '%individual%' OR LOWER(plan_name) LIKE '%solo%' THEN 1
				WHEN LOWER(plan_name) LIKE '%enterprise%' OR LOWER(plan_name) LIKE '%unlimited%' THEN 0
				ELSE 5
			END
			WHERE member_limit=5;

			UPDATE workspace_memberships
			SET role='member'
			WHERE role NOT IN ('owner', 'admin', 'member');

			UPDATE workspace_invitations
			SET role='member'
			WHERE role NOT IN ('admin', 'member');

			DO $$
			BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conname='chk_subscriptions_member_limit'
				) THEN
					ALTER TABLE subscriptions
						ADD CONSTRAINT chk_subscriptions_member_limit
						CHECK (member_limit=0 OR member_limit > 0);
				END IF;
				IF NOT EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conname='chk_workspace_memberships_role'
				) THEN
					ALTER TABLE workspace_memberships
						ADD CONSTRAINT chk_workspace_memberships_role
						CHECK (role IN ('owner', 'admin', 'member'));
				END IF;
				IF NOT EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conname='chk_workspace_invitations_role'
				) THEN
					ALTER TABLE workspace_invitations
						ADD CONSTRAINT chk_workspace_invitations_role
						CHECK (role IN ('admin', 'member'));
				END IF;
			END $$;

			INSERT INTO workspace_documents (
				workspace_id, title, content, status, linked_workstream_ids,
				created_by, updated_by
			)
			SELECT
				workstream.workspace_id,
				workstream.title || ' — документация',
				CONCAT(
					'# ', workstream.title, E'\n\n',
					'## Контекст направления', E'\n\n',
					COALESCE(NULLIF(workstream.description, ''), NULLIF(workstream.goal, ''), 'Контекст пока не заполнен.'), E'\n\n',
					CASE WHEN BTRIM(workstream.ckp) <> ''
						THEN CONCAT('## Ценный конечный продукт', E'\n\n', workstream.ckp, E'\n\n')
						ELSE '' END,
					CASE WHEN BTRIM(workstream.reason) <> ''
						THEN CONCAT('## Почему направление существует', E'\n\n', workstream.reason)
						ELSE '' END
				),
				'draft',
				jsonb_build_array(workstream.id),
				workstream.created_by,
				workstream.created_by
			FROM v2_tactical_workstreams workstream
			WHERE workstream.archived_at IS NULL
				AND NOT EXISTS (
					SELECT 1 FROM workspace_documents document
					WHERE document.workspace_id=workstream.workspace_id
						AND document.archived_at IS NULL
						AND document.linked_workstream_ids @> jsonb_build_array(workstream.id)
				);

			INSERT INTO workspace_documents (
				workspace_id, title, content, status, linked_project_ids,
				linked_workstream_ids, created_by, updated_by
			)
			SELECT
				project.workspace_id,
				project.title || ' — документация',
				CONCAT(
					'# ', project.title, E'\n\n',
					'## Контекст проекта', E'\n\n',
					COALESCE(NULLIF(project.description, ''), NULLIF(project.why_needed, ''), 'Контекст пока не заполнен.'), E'\n\n',
					CASE WHEN BTRIM(project.expected_value) <> ''
						THEN CONCAT('## Ожидаемая ценность', E'\n\n', project.expected_value, E'\n\n')
						ELSE '' END,
					CASE WHEN BTRIM(project.success_criteria) <> ''
						THEN CONCAT('## Критерий успеха', E'\n\n', project.success_criteria)
						ELSE '' END
				),
				'draft',
				jsonb_build_array(project.id),
				jsonb_build_array(project.workstream_id),
				project.created_by,
				project.created_by
			FROM v2_tactical_projects project
			WHERE project.archived_at IS NULL
				AND NOT EXISTS (
					SELECT 1 FROM workspace_documents document
					WHERE document.workspace_id=project.workspace_id
						AND document.archived_at IS NULL
						AND document.linked_project_ids @> jsonb_build_array(project.id)
				);
			`,
	},
	{
		ID: "20260727_050_auth_invitation_flow",
		SQL: `
			ALTER TABLE users
				ADD COLUMN IF NOT EXISTS workspace_onboarding_mode TEXT NOT NULL DEFAULT 'complete';

			ALTER TABLE workspace_invitations
				ADD COLUMN IF NOT EXISTS department_ids INTEGER[] NOT NULL DEFAULT '{}'::INTEGER[];

			DO $$
			BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conname='chk_users_workspace_onboarding_mode'
				) THEN
					ALTER TABLE users
						ADD CONSTRAINT chk_users_workspace_onboarding_mode
						CHECK (workspace_onboarding_mode IN ('create', 'join', 'complete'));
				END IF;
			END $$;
		`,
	},
	{
		ID: "20260727_051_verify_established_workspace_members",
		SQL: `
			UPDATE users account
			SET email_verified=TRUE
			WHERE account.email_verified=FALSE
				AND EXISTS (
					SELECT 1
					FROM workspace_memberships membership
					WHERE membership.user_id=account.id
						AND membership.status='active'
				);
		`,
	},
	{
		ID: "20260727_052_tasks_without_active_course",
		SQL: `
			ALTER TABLE v2_tasks
				ALTER COLUMN course_id DROP NOT NULL;

			ALTER TABLE v2_tasks
				DROP CONSTRAINT IF EXISTS v2_tasks_course_id_fkey;

			ALTER TABLE v2_tasks
				ADD CONSTRAINT v2_tasks_course_id_fkey
				FOREIGN KEY (course_id) REFERENCES v2_courses(id) ON DELETE SET NULL;
		`,
	},
	{
		ID: "20260727_053_workspace_billing_plans_and_ai_quotas",
		SQL: `
			ALTER TABLE workspaces
				ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT 'Europe/Moscow';

			CREATE TABLE IF NOT EXISTS billing_plans (
				code TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				monthly_amount NUMERIC(12,2) NOT NULL,
				annual_amount NUMERIC(12,2) NOT NULL,
				currency TEXT NOT NULL DEFAULT 'RUB',
				member_limit INTEGER NOT NULL,
				weekly_ai_limit INTEGER NOT NULL,
				reset_amount NUMERIC(12,2) NOT NULL,
				standard_responses_month INTEGER NOT NULL,
				equivalent_tokens_month BIGINT NOT NULL,
				active BOOLEAN NOT NULL DEFAULT TRUE,
				sort_order INTEGER NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CHECK (member_limit=0 OR member_limit > 0),
				CHECK (weekly_ai_limit > 0)
			);

			INSERT INTO billing_plans (
				code, name, monthly_amount, annual_amount, currency, member_limit,
				weekly_ai_limit, reset_amount, standard_responses_month,
				equivalent_tokens_month, sort_order
			) VALUES
				('founder', 'Founder', 3490, 33504, 'RUB', 1, 150, 890, 650, 5000000, 10),
				('team', 'Team', 11990, 115104, 'RUB', 5, 400, 2990, 1730, 12000000, 20),
				('company', 'Company', 29990, 287904, 'RUB', 0, 1200, 7490, 5200, 36000000, 30)
			ON CONFLICT (code) DO UPDATE SET
				name=EXCLUDED.name,
				monthly_amount=EXCLUDED.monthly_amount,
				annual_amount=EXCLUDED.annual_amount,
				currency=EXCLUDED.currency,
				member_limit=EXCLUDED.member_limit,
				weekly_ai_limit=EXCLUDED.weekly_ai_limit,
				reset_amount=EXCLUDED.reset_amount,
				standard_responses_month=EXCLUDED.standard_responses_month,
				equivalent_tokens_month=EXCLUDED.equivalent_tokens_month,
				sort_order=EXCLUDED.sort_order,
				updated_at=NOW();

			ALTER TABLE subscriptions
				ADD COLUMN IF NOT EXISTS plan_code TEXT NOT NULL DEFAULT 'founder',
				ADD COLUMN IF NOT EXISTS billing_period TEXT NOT NULL DEFAULT 'monthly',
				ADD COLUMN IF NOT EXISTS quota_anchor_at TIMESTAMPTZ NULL;

			UPDATE subscriptions
			SET plan_code=CASE
				WHEN member_limit=0 THEN 'company'
				WHEN member_limit=1 THEN 'founder'
				ELSE 'team'
			END
			WHERE plan_code='founder' AND member_limit <> 1;

			UPDATE subscriptions subscription
			SET
				plan_name=plan.name,
				amount=CASE
					WHEN subscription.billing_period='annual' THEN plan.annual_amount
					ELSE plan.monthly_amount
				END,
				currency=plan.currency,
				member_limit=plan.member_limit,
				quota_anchor_at=COALESCE(
					subscription.quota_anchor_at,
					subscription.current_period_start,
					subscription.created_at
				)
			FROM billing_plans plan
			WHERE plan.code=subscription.plan_code;

			DO $$
			BEGIN
					IF NOT EXISTS (
						SELECT 1 FROM pg_constraint
						WHERE conname='chk_subscriptions_billing_period'
					) THEN
						ALTER TABLE subscriptions
							ADD CONSTRAINT chk_subscriptions_billing_period
							CHECK (billing_period IN ('monthly', 'annual'));
					END IF;
					IF NOT EXISTS (
						SELECT 1 FROM pg_constraint
						WHERE conname='fk_subscriptions_plan_code'
					) THEN
						ALTER TABLE subscriptions
							ADD CONSTRAINT fk_subscriptions_plan_code
							FOREIGN KEY (plan_code) REFERENCES billing_plans(code);
					END IF;
				END $$;

			CREATE TABLE IF NOT EXISTS billing_seller_profiles (
				id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id=1),
				full_name TEXT NOT NULL,
				inn TEXT NOT NULL,
				kpp TEXT NOT NULL,
				registration_number TEXT NOT NULL,
				legal_address TEXT NOT NULL,
				bank_name TEXT NOT NULL,
				settlement_account TEXT NOT NULL,
				correspondent_account TEXT NOT NULL,
				bic TEXT NOT NULL,
				director_name TEXT NOT NULL,
				accounting_email TEXT NOT NULL,
				tax_label TEXT NOT NULL DEFAULT 'Без НДС',
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			INSERT INTO billing_seller_profiles (
				id, full_name, inn, kpp, registration_number, legal_address,
				bank_name, settlement_account, correspondent_account, bic,
				director_name, accounting_email, tax_label
			) VALUES (
				1, 'ООО "Реап"', '5262392668', '526201001', '1235200026995',
				'603105, Нижегородская область, г. Нижний Новгород, ул. Агрономическая, д. 136, кв. 32',
				'АО "Тинькофф Банк"', '40702810110001489655',
				'30101810145250000974', '044525974',
				'Михасов Никита Игоревич', 'nikitamichasov@yandex.ru', 'Без НДС'
			)
			ON CONFLICT (id) DO UPDATE SET
				full_name=EXCLUDED.full_name,
				inn=EXCLUDED.inn,
				kpp=EXCLUDED.kpp,
				registration_number=EXCLUDED.registration_number,
				legal_address=EXCLUDED.legal_address,
				bank_name=EXCLUDED.bank_name,
				settlement_account=EXCLUDED.settlement_account,
				correspondent_account=EXCLUDED.correspondent_account,
				bic=EXCLUDED.bic,
				director_name=EXCLUDED.director_name,
				accounting_email=EXCLUDED.accounting_email,
				tax_label=EXCLUDED.tax_label,
				updated_at=NOW();

			CREATE TABLE IF NOT EXISTS workspace_billing_orders (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				order_kind TEXT NOT NULL CHECK (order_kind IN ('subscription', 'quota_reset')),
				plan_code TEXT NOT NULL REFERENCES billing_plans(code),
				billing_period TEXT NOT NULL CHECK (billing_period IN ('monthly', 'annual')),
				amount NUMERIC(12,2) NOT NULL,
				currency TEXT NOT NULL DEFAULT 'RUB',
				status TEXT NOT NULL DEFAULT 'draft'
					CHECK (status IN ('draft', 'waiting', 'paid', 'cancelled', 'expired')),
				provider TEXT NOT NULL DEFAULT 'manual',
				external_id TEXT NOT NULL DEFAULT '',
				paid_at TIMESTAMPTZ NULL,
				metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_workspace_billing_orders_workspace
				ON workspace_billing_orders (workspace_id, created_at DESC);

			ALTER TABLE workspace_billing_invoices
				ADD COLUMN IF NOT EXISTS order_id BIGINT NULL REFERENCES workspace_billing_orders(id) ON DELETE SET NULL,
				ADD COLUMN IF NOT EXISTS order_kind TEXT NOT NULL DEFAULT 'subscription',
				ADD COLUMN IF NOT EXISTS plan_code TEXT NOT NULL DEFAULT 'founder',
				ADD COLUMN IF NOT EXISTS billing_period TEXT NOT NULL DEFAULT 'monthly',
				ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT 'Подписка REUP.goals',
				ADD COLUMN IF NOT EXISTS tax_label TEXT NOT NULL DEFAULT 'Без НДС',
				ADD COLUMN IF NOT EXISTS seller_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
				ADD COLUMN IF NOT EXISTS confirmed_by TEXT NOT NULL DEFAULT '';

			CREATE TABLE IF NOT EXISTS workspace_ai_quotas (
				workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
				plan_code TEXT NOT NULL REFERENCES billing_plans(code),
				window_started_at TIMESTAMPTZ NOT NULL,
				window_ends_at TIMESTAMPTZ NOT NULL,
				base_limit INTEGER NOT NULL,
				base_used INTEGER NOT NULL DEFAULT 0,
				purchased_balance INTEGER NOT NULL DEFAULT 0,
				warning_level INTEGER NOT NULL DEFAULT 0,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CHECK (base_limit > 0),
				CHECK (base_used >= 0),
				CHECK (purchased_balance >= 0),
				CHECK (warning_level IN (0, 70, 90, 100))
			);

			CREATE TABLE IF NOT EXISTS workspace_ai_quota_events (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				reservation_key TEXT NOT NULL UNIQUE,
				event_type TEXT NOT NULL,
				source TEXT NOT NULL,
				amount INTEGER NOT NULL,
				status TEXT NOT NULL CHECK (status IN ('reserved', 'consumed', 'refunded')),
				ai_module TEXT NOT NULL DEFAULT '',
				metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				settled_at TIMESTAMPTZ NULL
			);

			CREATE INDEX IF NOT EXISTS idx_workspace_ai_quota_events_workspace
				ON workspace_ai_quota_events (workspace_id, created_at DESC);
		`,
	},
	{
		ID: "20260727_054_user_product_tour",
		SQL: `
			ALTER TABLE users
				ADD COLUMN IF NOT EXISTS product_tour_status TEXT NOT NULL DEFAULT 'not_started',
				ADD COLUMN IF NOT EXISTS product_tour_step INTEGER NOT NULL DEFAULT 0,
				ADD COLUMN IF NOT EXISTS product_tour_completed_at TIMESTAMPTZ NULL;

			DO $$
			BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conname='chk_users_product_tour_status'
				) THEN
					ALTER TABLE users
						ADD CONSTRAINT chk_users_product_tour_status
						CHECK (product_tour_status IN ('not_started', 'in_progress', 'completed', 'skipped'));
				END IF;
				IF NOT EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conname='chk_users_product_tour_step'
				) THEN
					ALTER TABLE users
						ADD CONSTRAINT chk_users_product_tour_step
						CHECK (product_tour_step BETWEEN 0 AND 5);
				END IF;
			END $$;
		`,
	},
	{
		ID: "20260727_055_token_based_ai_quotas",
		SQL: `
			UPDATE billing_plans
			SET weekly_ai_limit=CASE code
				WHEN 'founder' THEN 1250000
				WHEN 'team' THEN 3000000
				WHEN 'company' THEN 9000000
				ELSE weekly_ai_limit
			END,
			updated_at=NOW()
			WHERE code IN ('founder', 'team', 'company');
		`,
	},
	{
		ID: "20260728_056_billing_invoice_idempotency",
		SQL: `
			ALTER TABLE workspace_billing_orders
				ADD COLUMN IF NOT EXISTS idempotency_key TEXT NOT NULL DEFAULT '';

			CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_billing_orders_idempotency
				ON workspace_billing_orders (workspace_id, idempotency_key)
				WHERE idempotency_key <> '';

			ALTER TABLE user_profile_settings
				ALTER COLUMN theme SET DEFAULT 'light';

			UPDATE user_profile_settings SET theme='light' WHERE theme='dark';
		`,
	},
	{
		ID: "20260728_057_ai_quota_reservation_metadata",
		SQL: `
			ALTER TABLE workspace_ai_quota_events
				ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				ADD COLUMN IF NOT EXISTS settled_at TIMESTAMPTZ NULL;
		`,
	},
	{
		ID: "20260728_058_release_stuck_ai_quota_reservations",
		SQL: `
			WITH stuck AS (
				SELECT
					workspace_id,
					SUM(COALESCE(
						NULLIF(metadata_json->>'reserved_base', '')::INTEGER,
						CASE WHEN source='base' THEN amount ELSE 0 END
					))::INTEGER AS reserved_base,
					SUM(COALESCE(
						NULLIF(metadata_json->>'reserved_purchased', '')::INTEGER,
						CASE WHEN source='purchased' THEN amount ELSE 0 END
					))::INTEGER AS reserved_purchased
				FROM workspace_ai_quota_events
				WHERE status='reserved'
				GROUP BY workspace_id
			)
			UPDATE workspace_ai_quotas quota
			SET
				base_used=GREATEST(0, quota.base_used-stuck.reserved_base),
				purchased_balance=quota.purchased_balance+stuck.reserved_purchased,
				warning_level=0,
				updated_at=NOW()
			FROM stuck
			WHERE quota.workspace_id=stuck.workspace_id;

			UPDATE workspace_ai_quota_events
			SET
				status='refunded',
				settled_at=NOW(),
				metadata_json=metadata_json || jsonb_build_object(
					'recovery_reason', 'settlement_parameter_type_fix'
				)
			WHERE status='reserved';
		`,
	},
	{
		ID: "20260728_059_trust_knowledge_interviewer_readiness",
		SQL: `
			UPDATE strategic_knowledge_pipeline_state
			SET
				status='ready',
				ready_revision=GREATEST(ready_revision, candidate_revision),
				audit_feedback_json='[]'::jsonb,
				updated_at=NOW()
			WHERE candidate_revision > 0
				AND ready_revision=0;

			INSERT INTO v2_background_jobs (
				workspace_id, job_type, dedupe_key, payload_json, status,
				attempts, max_attempts, not_before, updated_at
			)
			SELECT
				workspace_id,
				'knowledge_base.context_refresh',
				'pending',
				jsonb_build_object('latest_source_id', last_user_source_id),
				'queued',
				0,
				5,
				NOW(),
				NOW()
			FROM strategic_knowledge_pipeline_state
			WHERE ready_revision > 0
				AND last_extracted_source_id < last_user_source_id
			ON CONFLICT (job_type, workspace_id, dedupe_key)
				WHERE dedupe_key <> '' AND status='queued'
			DO UPDATE SET
				payload_json=EXCLUDED.payload_json,
				max_attempts=GREATEST(v2_background_jobs.max_attempts, EXCLUDED.max_attempts),
				not_before=LEAST(v2_background_jobs.not_before, EXCLUDED.not_before),
				updated_at=NOW();
		`,
	},
	{
		ID: "20260729_060_feature_onboarding",
		SQL: `
			ALTER TABLE users
				ADD COLUMN IF NOT EXISTS feature_onboarding_json JSONB NOT NULL DEFAULT '{}'::jsonb;
		`,
	},
	{
		ID: "20260729_061_course_reviews",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_course_reviews (
				id BIGSERIAL PRIMARY KEY,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				course_id INTEGER NOT NULL REFERENCES v2_courses(id) ON DELETE CASCADE,
				result TEXT NOT NULL,
				metric_result TEXT NOT NULL DEFAULT '',
				outcome TEXT NOT NULL,
				decision TEXT NOT NULL,
				created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CHECK (outcome IN ('achieved', 'partially_achieved', 'not_achieved', 'changed')),
				CHECK (decision IN ('continue', 'revise', 'complete'))
			);

			CREATE INDEX IF NOT EXISTS idx_v2_course_reviews_course
				ON v2_course_reviews (workspace_id, course_id, created_at DESC);
		`,
	},
	{
		ID: "20260730_062_recover_completed_strategy_synthesis",
		SQL: `
			UPDATE v2_strategies strategy
			SET status='ready_for_review', updated_at=NOW()
			WHERE strategy.status='draft'
				AND strategy.archived_at IS NULL
				AND EXISTS (
					SELECT 1
					FROM v2_strategy_synthesis_runs run
					JOIN v2_strategy_session_state session
						ON session.workspace_id=run.workspace_id
						AND session.revision=run.session_revision
						AND session.last_user_message_id=run.through_message_id
					JOIN v2_strategy_readiness_runs readiness
						ON readiness.workspace_id=run.workspace_id
						AND readiness.strategy_id=run.strategy_id
						AND readiness.session_revision=run.session_revision
						AND readiness.validated_through_message_id=run.through_message_id
						AND readiness.status='completed'
						AND readiness.verdict='ready'
						AND readiness.can_synthesize=TRUE
					WHERE run.workspace_id=strategy.workspace_id
						AND run.strategy_id=strategy.id
						AND run.status='completed'
				);
		`,
	},
	{
		ID: "20260730_063_unified_agent_runs",
		SQL: `
			CREATE TABLE IF NOT EXISTS v2_agent_runs (
				id BIGSERIAL PRIMARY KEY,
				public_id TEXT NOT NULL UNIQUE,
				workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				thread_id INTEGER NOT NULL REFERENCES v2_tactics_advisor_threads(id) ON DELETE CASCADE,
				user_message_id INTEGER NULL REFERENCES v2_tactics_chat_messages(id) ON DELETE SET NULL,
				assistant_message_id INTEGER NULL REFERENCES v2_tactics_chat_messages(id) ON DELETE SET NULL,
				scope_type TEXT NOT NULL DEFAULT 'workspace',
				scope_id INTEGER NOT NULL DEFAULT 0,
				scope_label TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'queued',
				model TEXT NOT NULL,
				prompt_version TEXT NOT NULL DEFAULT 'executive_advisor_v1',
				input_text TEXT NOT NULL,
				output_text TEXT NOT NULL DEFAULT '',
				partial_output TEXT NOT NULL DEFAULT '',
				previous_response_id TEXT NOT NULL DEFAULT '',
				conversation_id TEXT NOT NULL DEFAULT '',
				vector_store_id TEXT NOT NULL DEFAULT '',
				state_ciphertext TEXT NOT NULL DEFAULT '',
				error_text TEXT NOT NULL DEFAULT '',
				reservation_id TEXT NOT NULL DEFAULT '',
				usage_requests INTEGER NOT NULL DEFAULT 0,
				usage_input_tokens INTEGER NOT NULL DEFAULT 0,
				usage_output_tokens INTEGER NOT NULL DEFAULT 0,
				usage_total_tokens INTEGER NOT NULL DEFAULT 0,
				started_at TIMESTAMPTZ NULL,
				completed_at TIMESTAMPTZ NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CHECK (status IN ('queued', 'running', 'waiting_approval', 'completed', 'failed', 'canceled'))
			);

			CREATE INDEX IF NOT EXISTS idx_v2_agent_runs_thread
				ON v2_agent_runs (workspace_id, user_id, thread_id, created_at DESC);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_agent_runs_one_active_thread
				ON v2_agent_runs (workspace_id, user_id, thread_id)
				WHERE status IN ('queued', 'running', 'waiting_approval');
			CREATE INDEX IF NOT EXISTS idx_v2_agent_runs_status
				ON v2_agent_runs (status, updated_at)
				WHERE status IN ('queued', 'running', 'waiting_approval');

			CREATE TABLE IF NOT EXISTS v2_agent_run_events (
				id BIGSERIAL PRIMARY KEY,
				run_id BIGINT NOT NULL REFERENCES v2_agent_runs(id) ON DELETE CASCADE,
				event_type TEXT NOT NULL,
				stage TEXT NOT NULL DEFAULT '',
				title TEXT NOT NULL,
				detail TEXT NOT NULL DEFAULT '',
				tool_name TEXT NOT NULL DEFAULT '',
				tool_call_id TEXT NOT NULL DEFAULT '',
				payload_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE INDEX IF NOT EXISTS idx_v2_agent_run_events_run
				ON v2_agent_run_events (run_id, id);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_agent_run_events_tool_stage
				ON v2_agent_run_events (run_id, event_type, tool_call_id)
				WHERE tool_call_id <> '';

			CREATE TABLE IF NOT EXISTS v2_agent_run_approvals (
				id BIGSERIAL PRIMARY KEY,
				run_id BIGINT NOT NULL REFERENCES v2_agent_runs(id) ON DELETE CASCADE,
				call_id TEXT NOT NULL,
				tool_name TEXT NOT NULL,
				arguments_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				status TEXT NOT NULL DEFAULT 'pending',
				action_index INTEGER NOT NULL,
				result_json JSONB NOT NULL DEFAULT '{}'::jsonb,
				error_text TEXT NOT NULL DEFAULT '',
				decided_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
				decided_at TIMESTAMPTZ NULL,
				applied_at TIMESTAMPTZ NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE (run_id, call_id),
				CHECK (status IN ('pending', 'approved', 'rejected', 'applied', 'failed'))
			);

			CREATE INDEX IF NOT EXISTS idx_v2_agent_run_approvals_run
				ON v2_agent_run_approvals (run_id, action_index);
		`,
	},
	{
		ID: "20260731_064_versioned_agent_sessions",
		SQL: `
			ALTER TABLE v2_agent_runs
				ADD COLUMN IF NOT EXISTS agent_release_id TEXT NOT NULL
					DEFAULT 'executive_advisor_2026_07_30_v1',
				ADD COLUMN IF NOT EXISTS session_generation INTEGER NOT NULL DEFAULT 1,
				ADD COLUMN IF NOT EXISTS migrated_from_release_id TEXT NOT NULL DEFAULT '',
				ADD COLUMN IF NOT EXISTS continuity_context TEXT NOT NULL DEFAULT '';

			CREATE INDEX IF NOT EXISTS idx_v2_agent_runs_release
				ON v2_agent_runs (
					workspace_id, user_id, thread_id, agent_release_id,
					model, prompt_version, created_at DESC
				);
		`,
	},
	{
		ID: "20260731_065_project_expected_result",
		SQL: `
			ALTER TABLE v2_tactical_projects
				ADD COLUMN IF NOT EXISTS expected_result TEXT NOT NULL DEFAULT '';

			UPDATE v2_tactical_projects
			SET expected_result=expected_value
			WHERE BTRIM(expected_result)='' AND BTRIM(expected_value)<>'';

			DROP TRIGGER IF EXISTS trg_queue_task_eval_from_project ON v2_tactical_projects;
			CREATE TRIGGER trg_queue_task_eval_from_project
				AFTER UPDATE OF title, description, expected_result, why_needed, success_criteria, failure_criteria, metric_name, expected_value, status ON v2_tactical_projects
				FOR EACH ROW EXECUTE FUNCTION reup_queue_task_evaluations_from_context();
		`,
	},
	{
		ID: "20260731_066_release_stalled_agent_approvals",
		SQL: `
			UPDATE v2_agent_runs run
			SET status='failed',
				error_text='agent_approval_no_longer_pending',
				completed_at=NOW(),
				updated_at=NOW()
			WHERE run.status='waiting_approval'
				AND NOT EXISTS (
					SELECT 1
					FROM v2_agent_run_approvals approval
					WHERE approval.run_id=run.id AND approval.status='pending'
				);
		`,
	},
	{
		ID: "20260731_067_prioritize_interactive_agent_jobs",
		SQL: `
			ALTER TABLE v2_background_jobs
				ADD COLUMN IF NOT EXISTS priority INTEGER NOT NULL DEFAULT 0;

			UPDATE v2_background_jobs
			SET priority=100, updated_at=NOW()
			WHERE job_type IN ('executive_agent.execute', 'executive_agent.resume')
				AND status IN ('queued', 'running');

			CREATE INDEX IF NOT EXISTS idx_v2_background_jobs_priority_due
				ON v2_background_jobs (status, priority DESC, not_before, id);
		`,
	},
	{
		ID: "20260731_068_recover_orphaned_agent_runs",
		SQL: `
			INSERT INTO v2_background_jobs (
				workspace_id, job_type, dedupe_key, payload_json, status,
				priority, attempts, max_attempts, not_before, updated_at
			)
			SELECT
				r.workspace_id,
				'executive_agent.execute',
				r.public_id,
				jsonb_build_object('run_id', r.public_id),
				'queued',
				100,
				0,
				3,
				NOW(),
				NOW()
			FROM v2_agent_runs r
			WHERE r.status IN ('queued', 'running')
				AND NOT EXISTS (
					SELECT 1
					FROM v2_background_jobs j
					WHERE j.workspace_id=r.workspace_id
						AND j.dedupe_key=r.public_id
						AND j.job_type IN ('executive_agent.execute', 'executive_agent.resume')
						AND j.status IN ('queued', 'running')
				)
			ON CONFLICT DO NOTHING;
		`,
	},
	{
		ID: "20260731_069_allow_strategy_advisor_threads",
		SQL: `
			ALTER TABLE v2_tactics_advisor_threads
				DROP CONSTRAINT IF EXISTS v2_tactics_advisor_threads_scope_type_check;
			ALTER TABLE v2_tactics_advisor_threads
				ADD CONSTRAINT v2_tactics_advisor_threads_scope_type_check
				CHECK (scope_type IN ('workspace', 'strategy', 'workstream', 'project', 'department'));
		`,
	},
	{
		ID: "20260731_070_isolate_background_job_queues",
		SQL: `
			ALTER TABLE v2_background_jobs
				ADD COLUMN IF NOT EXISTS queue_name TEXT NOT NULL DEFAULT 'default';

			DROP INDEX IF EXISTS idx_v2_background_jobs_active_dedupe;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_v2_background_jobs_active_dedupe
				ON v2_background_jobs (queue_name, job_type, workspace_id, dedupe_key)
				WHERE dedupe_key <> '' AND status='queued';

			DROP INDEX IF EXISTS idx_v2_background_jobs_priority_due;
				CREATE INDEX IF NOT EXISTS idx_v2_background_jobs_priority_due
					ON v2_background_jobs (queue_name, status, priority DESC, not_before, id);
			`,
	},
	{
		ID: "20260731_071_repair_agent_proposal_messages",
		SQL: `
				UPDATE v2_agent_runs run
				SET assistant_message_id=(
						SELECT message.id
						FROM v2_tactics_chat_messages message
						WHERE message.workspace_id=run.workspace_id
							AND message.scope_type='advisor_thread'
							AND message.scope_id=run.thread_id
							AND message.role='assistant'
							AND message.metadata_json->>'agent_run_id'=run.public_id
							AND jsonb_typeof(message.metadata_json->'draft_changes')='array'
							AND jsonb_array_length(
								COALESCE(message.metadata_json->'draft_changes', '[]'::jsonb)
							)>0
						ORDER BY message.created_at DESC, message.id DESC
						LIMIT 1
					),
					updated_at=NOW()
				WHERE run.status='waiting_approval'
					AND run.assistant_message_id IS NULL
					AND EXISTS (
						SELECT 1
						FROM v2_tactics_chat_messages message
						WHERE message.workspace_id=run.workspace_id
							AND message.scope_type='advisor_thread'
							AND message.scope_id=run.thread_id
							AND message.role='assistant'
							AND message.metadata_json->>'agent_run_id'=run.public_id
							AND jsonb_typeof(message.metadata_json->'draft_changes')='array'
							AND jsonb_array_length(
								COALESCE(message.metadata_json->'draft_changes', '[]'::jsonb)
							)>0
					);
			`,
	},
}

func Run(dbx *sql.DB) error {
	if _, err := dbx.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			id TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return err
	}

	for _, migration := range migrations {
		if err := runOne(dbx, migration); err != nil {
			return err
		}
	}

	return nil
}

func runOne(dbx *sql.DB, migration Migration) error {
	tx, err := dbx.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(728490217)`); err != nil {
		return fmt.Errorf("migration lock failed: %w", err)
	}

	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE id=$1)`, migration.ID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit()
	}

	if _, err := tx.Exec(migration.SQL); err != nil {
		return fmt.Errorf("migration %s failed: %w", migration.ID, err)
	}

	if _, err := tx.Exec(`INSERT INTO schema_migrations (id) VALUES ($1)`, migration.ID); err != nil {
		return err
	}

	return tx.Commit()
}
