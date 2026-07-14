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
