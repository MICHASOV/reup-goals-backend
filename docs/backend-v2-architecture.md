# REUP.goals v2 Product and Backend Architecture

Документ фиксирует архитектурную основу REUP.goals v2: продуктовую модель, backend, базу данных, AI-интеграцию и правила разработки.

Ключевая установка: **v2 является целевой основной версией продукта**. Текущий backend не является долгосрочной продуктовой моделью. Его нужно использовать как рабочую основу для auth/email/payments/deploy и как источник данных для будущей миграции, но новые продуктовые сущности должны строиться вокруг v2-модели: workspace, knowledge base, strategy, course, tactics, tasks, AI chats.

Цель: быстро довести web-кабинет до production/MVP, затем подогнать сайт и мобильное приложение под v2, не создавая временную кашу и не ломая уже работающие критичные сценарии раньше времени.

## 0. Product Architecture Overview

### Product target model

REUP.goals v2 строится вокруг цепочки:

```text
Workspace -> Knowledge Base -> Strategy -> Course -> Tactics -> Tasks -> AI feedback
```

Смысл модулей:

- Workspace / Company: компания, бизнес или проект пользователя.
- Knowledge Base: источник фактов о бизнесе.
- AI Notes / Extracted Facts: структурированные факты, извлечённые из пользовательских данных.
- Strategy: стратегическое решение на основе базы знаний.
- Course: активный фокус движения компании.
- Tactics: направления, проекты, риски, возможности и метрики.
- Tasks: execution layer, привязанный к курсу и тактике.
- AI chats: помощники в разных контекстах, которые предлагают draft/suggestion, но не утверждают важные решения без пользователя.
- Subscription / Access: доступ к workspace и AI-функциям.
- Events / Audit: история действий, аналитика спроса и отладка production.

### Existing system role

Текущий backend делится на три категории:

1. **Reuse as foundation**: users, auth, JWT middleware, bcrypt passwords, email verification, password reset, Unisender integration, CloudPayments client/webhooks.
2. **Bridge temporarily**: current subscription status and account API, while billing migrates from user-level to workspace-level.
3. **Deprecate and migrate later**: old `goals`, `goal_context`, `tasks`, `task_ai_state`, old mobile-oriented goal/task endpoints.

Старые goal/task модели не должны определять v2-архитектуру. Они могут быть миграционным источником или временным compatibility layer, пока сайт/мобильное приложение переходят на v2.

### MVP boundary

MVP не обязан включать всю будущую B2B-командность, но обязан не блокировать её. Практичная первая production-версия:

- один пользователь автоматически получает один workspace;
- роль только `owner`, но таблица membership уже есть;
- база знаний хранит текстовые блоки, без файлов;
- strategy/course/tactics/tasks уже workspace-bound;
- AI работает через backend и сохраняет структурированные результаты;
- subscription проверяется на уровне workspace facade, даже если физически ещё использует user subscription.

## 1. Backend Audit Report

### Текущее состояние

Backend написан на Go, HTTP API построен на стандартном `net/http` mux. Точка входа: `cmd/api/main.go`. База данных: PostgreSQL через `database/sql` и `github.com/lib/pq`.

Текущий backend уже обслуживает:

- auth: регистрация, вход, текущий пользователь, logout/delete;
- email verification и reset password через Unisender;
- subscription/payments через CloudPayments;
- старые цели и задачи мобильного MVP;
- базовую AI-оценку задач через OpenAI;
- частичную продуктовую аналитику.

Текущая структура модулей:

- `internal/auth`: пользователи, JWT, email-коды, пароль, account handlers;
- `internal/subscriptions`: CloudPayments, subscription status, webhook handlers;
- `internal/goals`: старые goal endpoints для mobile/MVP;
- `internal/tasks`: старые task endpoints и AI evaluation для mobile/MVP;
- `internal/ai`: OpenAI client и prompt builder для старой task evaluation;
- `internal/analytics`: event logging helpers;
- `internal/config`: env config;
- `internal/db`: PostgreSQL connection.

### Текущие endpoint'ы

Public:

- `POST /auth/register`
- `POST /auth/login`
- `POST /auth/verify-email`
- `POST /auth/resend-code`
- `POST /auth/forgot-password`
- `POST /auth/verify-reset-code`
- `POST /auth/reset-password`

Protected:

- `GET /auth/me`
- `POST /auth/logout`
- `DELETE /auth/delete`
- `GET /subscription/status`
- `GET /subscription/checkout-config`
- `POST /subscription/cancel`
- `GET /goal`
- `POST /goal/create`
- `POST /goal/update`
- `POST /goal/reset`
- `GET /tasks`
- `POST /task/create`
- `POST /task/update`
- `POST /task/status`
- `POST /task/clarification/create`
- `POST /task/evaluate`

CloudPayments webhooks:

- `POST /payments/cloudpayments/check`
- `POST /payments/cloudpayments/pay`
- `POST /payments/cloudpayments/fail`
- `POST /payments/cloudpayments/recurrent`
- `POST /payments/cloudpayments/cancel`

### Auth and users

Auth использует JWT в формате `Authorization: Bearer <token>`. JWT secret сейчас задан в `cmd/api/main.go` как константа `SUPER_SECRET_CHANGE_ME`, это production-риск: secret нужно вынести в env.

Пароли уже переведены на bcrypt, login поддерживает ленивую миграцию старых plaintext-паролей. Email verification и password reset реализованы через таблицу `auth_email_codes`.

`/auth/me` возвращает только `user_id` и `email`. Для v2 этого мало: нужны текущий workspace, роли, доступ по подписке и feature gates.

### Subscriptions

Сейчас `subscriptions` привязана к `user_id` и имеет `UNIQUE(user_id)`. Для v2 это нужно расширить до workspace-level billing. В MVP можно временно считать `one user = one workspace`, но данные подписки лучше мигрировать к `workspace_id`, чтобы потом не переделывать оплату.

### Goals/tasks deprecated legacy

Старые `goals` и `tasks` сейчас завязаны на `user_id`, не на workspace. JSON-контракт заточен под Flutter mobile app:

- task statuses: `active`, `done`, `canceled`;
- task AI поля: `normalized_task`, `avoidance_flag`, `trap_task`, `clarification_needed`, `explanation_short`.

Для v2 их не стоит напрямую превращать в стратегию/курс/тактику. Риск высокий: можно получить странную модель данных и потом переписывать core второй раз.

Решение: старые таблицы считаются deprecated. Их не развиваем как основную систему. Новые сайт и мобильное приложение должны постепенно перейти на `/api/v2`. После перехода данные из старых таблиц можно мигрировать, архивировать или удалить по отдельному migration plan.

### Frontend v2

Next.js frontend уже содержит web-кабинет:

- `/cabinet-v2/knowledge-base`
- `/cabinet-v2/strategy`
- `/cabinet-v2/course`
- `/cabinet-v2/tactics`
- `/cabinet-v2/tasks`
- `/cabinet-v2/tasks/[directionId]`
- `/cabinet-v2/tasks/[directionId]/board`
- `/cabinet-v2/tasks/[directionId]/documentation`

Основные mock-модули находятся в `components/cabinet-v2/*/*Mock.ts`.

Frontend v2 уже задаёт ключевые пользовательские сценарии, но не должен диктовать схему базы один-в-один. Backend должен дать стабильные API и доменную модель, а frontend постепенно заменить mock-данные на запросы.

### Основные риски

- JWT secret захардкожен.
- CORS сейчас `cors.AllowAll()`.
- Нет единого `/api/v2` слоя, legacy и v2 могут смешаться.
- Нет workspace/company модели.
- Подписка привязана к user, а продукт v2 должен быть workspace-based.
- AI-ответы старой системы частично живут как task state, нет общего AI orchestration слоя.
- Нет нормальной миграционной системы; schema создаётся через `EnsureSchema`.
- Нет permission layer, audit trail и workspace isolation.
- Нет healthcheck endpoint.

## 2. Target Backend Architecture

### Принцип развития

Не переписывать backend с нуля ради переписывания. Но v2 проектировать как **целевое ядро продукта**, а не как вечную параллельную надстройку.

Новые API логически отделить отдельным слоем:

```text
/api/v2/...
```

Это даёт четыре плюса:

- можно быстро подключать Next.js кабинет;
- старый Flutter/mobile/site flow не ломается во время разработки;
- сайт и мобилку можно потом спокойно перевести на v2;
- v2-модель можно проектировать нормально, без наследования старых компромиссов;
- старые endpoint'ы можно будет удалить после миграции, а не тащить их бесконечно.

### Предлагаемые backend-модули

```text
internal/
  auth/                 existing auth, плюс JWT env hardening
  subscriptions/        existing CloudPayments, постепенно workspace-aware
  v2/
    api/                route registration, response helpers
    access/             workspace access and permissions
    workspaces/         company/workspace foundation
    knowledge/          knowledge blocks and AI notes
    strategy/           strategy and artifacts
    course/             active course
    tactics/            directions, projects, risks, opportunities
    tasks/              v2 task model, board/status transitions
    chats/              AI chat threads/messages
    ai/                 orchestration, prompt versions, structured outputs
    events/             audit trail and product analytics
    subscriptions/      workspace subscription facade over existing payments
```

На первом этапе можно не делать идеальное разделение repository/service/router во всех модулях, но границы должны быть такими:

- API layer: decode/validate request, auth context, response shape;
- Service layer: business rules, status transitions, permission checks;
- Repository layer: SQL queries and transactions;
- AI layer: prompt version, model call, structured validation, result persistence.

### Auth/access model

JWT остаётся прежним для совместимости. В v2 каждый protected endpoint должен:

1. Получить `user_id` из JWT.
2. Определить `workspace_id`.
3. Проверить membership.
4. Проверить permission.
5. Проверить subscription/access gate, если действие требует активного доступа.

На MVP:

- при регистрации или первом входе создавать default workspace;
- пользователь становится `owner`;
- frontend получает `current_workspace` через bootstrap endpoint.

## 3. Database Architecture

### Existing tables and migration posture

Существующие таблицы на первом этапе не удаляем:

- `users`
- `auth_email_codes`
- `subscriptions`
- `payment_events`
- `goals`
- `goal_context`
- `tasks`
- `task_ai_state`
- `task_clarifications`
- analytics tables, если уже есть в production DB.

При этом:

- `users`, `auth_email_codes` остаются частью целевой основы;
- `subscriptions`, `payment_events` нужно привести к workspace-aware модели;
- `goals`, `goal_context`, `tasks`, `task_ai_state`, `task_clarifications` считаются deprecated legacy.

Их не нужно переименовывать и ломать в первой итерации, но нужно явно планировать миграцию и последующее удаление/архивацию.

### New v2 tables

#### Workspaces

```sql
CREATE TABLE workspaces (
  id SERIAL PRIMARY KEY,
  owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  name TEXT NOT NULL,
  legal_name TEXT NULL,
  display_name TEXT NULL,
  description TEXT NULL,
  industry TEXT NULL,
  business_type TEXT NULL,
  company_size TEXT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  archived_at TIMESTAMPTZ NULL
);
```

Indexes:

- `(owner_user_id)`
- `(status)`

#### Workspace memberships

```sql
CREATE TABLE workspace_memberships (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'owner',
  status TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(workspace_id, user_id)
);
```

Roles planned:

- `owner`
- `admin`
- `leader`
- `member`
- `viewer`

MVP can use only `owner`.

#### Knowledge blocks

Use fixed 12 block types per workspace.

```sql
CREATE TABLE v2_knowledge_blocks (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  title TEXT NOT NULL,
  description TEXT NULL,
  status TEXT NOT NULL DEFAULT 'empty',
  raw_content TEXT NOT NULL DEFAULT '',
  ai_notes JSONB NOT NULL DEFAULT '[]'::jsonb,
  completeness_score INTEGER NOT NULL DEFAULT 0,
  specificity_score INTEGER NOT NULL DEFAULT 0,
  evidence_score INTEGER NOT NULL DEFAULT 0,
  consistency_score INTEGER NOT NULL DEFAULT 0,
  confidence_score INTEGER NOT NULL DEFAULT 0,
  readiness_status TEXT NOT NULL DEFAULT 'empty',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(workspace_id, type)
);
```

#### AI notes / extracted facts

```sql
CREATE TABLE v2_ai_notes (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  block_id INTEGER NULL REFERENCES v2_knowledge_blocks(id) ON DELETE SET NULL,
  note_text TEXT NOT NULL,
  source_type TEXT NOT NULL,
  source_id TEXT NULL,
  source_quote TEXT NULL,
  confidence NUMERIC(4,3) NULL,
  status TEXT NOT NULL DEFAULT 'suggested',
  created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Indexes:

- `(workspace_id, status)`
- `(workspace_id, block_id)`

#### Strategies and artifacts

```sql
CREATE TABLE v2_strategies (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  version INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL DEFAULT 'draft',
  source_context_snapshot_id INTEGER NULL,
  global_goal TEXT NULL,
  local_goal TEXT NULL,
  direction TEXT NULL,
  business_stage TEXT NULL,
  strategic_problem TEXT NULL,
  economic_engine TEXT NULL,
  key_metric TEXT NULL,
  time_horizon TEXT NULL,
  success_criteria TEXT NULL,
  exclusions TEXT NULL,
  constraints TEXT NULL,
  validation_plan JSONB NOT NULL DEFAULT '[]'::jsonb,
  ai_confidence NUMERIC(4,3) NULL,
  created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  approved_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  approved_at TIMESTAMPTZ NULL
);
```

```sql
CREATE TABLE v2_strategy_artifacts (
  id SERIAL PRIMARY KEY,
  strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  title TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'draft',
  confidence NUMERIC(4,3) NULL,
  evidence_links JSONB NOT NULL DEFAULT '[]'::jsonb,
  related_knowledge_blocks JSONB NOT NULL DEFAULT '[]'::jsonb,
  ai_reasoning_summary TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(strategy_id, type)
);
```

#### Course

```sql
CREATE TABLE v2_courses (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  strategy_id INTEGER NOT NULL REFERENCES v2_strategies(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'draft',
  title TEXT NOT NULL,
  description TEXT NULL,
  active_focus TEXT NULL,
  current_stage TEXT NULL,
  key_metric TEXT NULL,
  local_goal TEXT NULL,
  time_horizon TEXT NULL,
  alignment_score INTEGER NULL,
  knowledge_readiness TEXT NULL,
  likely_constraint TEXT NULL,
  attention_zone TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  activated_at TIMESTAMPTZ NULL,
  archived_at TIMESTAMPTZ NULL
);
```

#### Tactics

```sql
CREATE TABLE v2_directions (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  course_id INTEGER NULL REFERENCES v2_courses(id) ON DELETE SET NULL,
  strategy_id INTEGER NULL REFERENCES v2_strategies(id) ON DELETE SET NULL,
  title TEXT NOT NULL,
  description TEXT NULL,
  ckp TEXT NULL,
  goal TEXT NULL,
  owner_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'draft',
  priority_order INTEGER NOT NULL DEFAULT 0,
  contribution_type TEXT NULL,
  confidence NUMERIC(4,3) NULL,
  health_status TEXT NULL,
  created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  archived_at TIMESTAMPTZ NULL
);
```

```sql
CREATE TABLE v2_projects (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  direction_id INTEGER NOT NULL REFERENCES v2_directions(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  description TEXT NULL,
  why_needed TEXT NULL,
  success_criteria TEXT NULL,
  failure_criteria TEXT NULL,
  key_metric TEXT NULL,
  owner_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'draft',
  confidence NUMERIC(4,3) NULL,
  priority_order INTEGER NOT NULL DEFAULT 0,
  created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  archived_at TIMESTAMPTZ NULL
);
```

```sql
CREATE TABLE v2_risks (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  entity_type TEXT NOT NULL,
  entity_id INTEGER NOT NULL,
  title TEXT NOT NULL,
  description TEXT NULL,
  severity TEXT NULL,
  probability TEXT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  coverage_status TEXT NOT NULL DEFAULT 'uncovered',
  created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

```sql
CREATE TABLE v2_opportunities (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  entity_type TEXT NOT NULL,
  entity_id INTEGER NOT NULL,
  title TEXT NOT NULL,
  description TEXT NULL,
  potential_impact TEXT NULL,
  urgency TEXT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  coverage_status TEXT NOT NULL DEFAULT 'uncovered',
  created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

#### v2 tasks

```sql
CREATE TABLE v2_tasks (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  course_id INTEGER NULL REFERENCES v2_courses(id) ON DELETE SET NULL,
  strategy_id INTEGER NULL REFERENCES v2_strategies(id) ON DELETE SET NULL,
  direction_id INTEGER NULL REFERENCES v2_directions(id) ON DELETE SET NULL,
  project_id INTEGER NULL REFERENCES v2_projects(id) ON DELETE SET NULL,
  title TEXT NOT NULL,
  description TEXT NULL,
  status TEXT NOT NULL DEFAULT 'free',
  owner_user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  priority_score NUMERIC(5,4) NULL,
  priority_order INTEGER NOT NULL DEFAULT 0,
  priority_reason TEXT NULL,
  relevance_score NUMERIC(5,4) NULL,
  impact_score NUMERIC(5,4) NULL,
  urgency_score NUMERIC(5,4) NULL,
  effort_score NUMERIC(5,4) NULL,
  confidence_score NUMERIC(5,4) NULL,
  backlog_category TEXT NOT NULL DEFAULT 'active',
  linked_metric TEXT NULL,
  linked_risk_id INTEGER NULL REFERENCES v2_risks(id) ON DELETE SET NULL,
  linked_opportunity_id INTEGER NULL REFERENCES v2_opportunities(id) ON DELETE SET NULL,
  flags JSONB NOT NULL DEFAULT '{}'::jsonb,
  due_date DATE NULL,
  started_at TIMESTAMPTZ NULL,
  completed_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  archived_at TIMESTAMPTZ NULL
);
```

Statuses:

- `free`
- `in_progress`
- `done`
- `archived`

Indexes:

- `(workspace_id, status)`
- `(workspace_id, direction_id)`
- `(workspace_id, project_id)`
- `(workspace_id, priority_score DESC)`

#### Task sources

```sql
CREATE TABLE v2_task_sources (
  id SERIAL PRIMARY KEY,
  task_id INTEGER NOT NULL REFERENCES v2_tasks(id) ON DELETE CASCADE,
  source_type TEXT NOT NULL,
  source_id TEXT NULL,
  source_title TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

#### Chat and AI

```sql
CREATE TABLE v2_chat_threads (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  scope TEXT NOT NULL,
  entity_type TEXT NULL,
  entity_id INTEGER NULL,
  title TEXT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  archived_at TIMESTAMPTZ NULL
);
```

```sql
CREATE TABLE v2_chat_messages (
  id SERIAL PRIMARY KEY,
  thread_id INTEGER NOT NULL REFERENCES v2_chat_threads(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  structured_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  ai_provider TEXT NULL,
  model TEXT NULL,
  prompt_version TEXT NULL,
  context_snapshot_id INTEGER NULL,
  token_input INTEGER NULL,
  token_output INTEGER NULL,
  latency_ms INTEGER NULL,
  cost_estimate NUMERIC(12,6) NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

```sql
CREATE TABLE v2_ai_evaluations (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  entity_type TEXT NOT NULL,
  entity_id INTEGER NOT NULL,
  scenario TEXT NOT NULL,
  model TEXT NOT NULL,
  prompt_version TEXT NOT NULL,
  input_snapshot JSONB NOT NULL,
  output_json JSONB NULL,
  status TEXT NOT NULL DEFAULT 'started',
  error_message TEXT NULL,
  latency_ms INTEGER NULL,
  token_input INTEGER NULL,
  token_output INTEGER NULL,
  cost_estimate NUMERIC(12,6) NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

#### Events and audit

```sql
CREATE TABLE v2_audit_events (
  id SERIAL PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  event_type TEXT NOT NULL,
  entity_type TEXT NULL,
  entity_id INTEGER NULL,
  before_json JSONB NULL,
  after_json JSONB NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Migration strategy

1. Add migration runner before adding many v2 tables. Current `EnsureSchema` is acceptable for tiny features, but v2 needs real migrations.
2. Create v2 tables without touching legacy data.
3. Backfill default workspace for every existing user.
4. Create owner membership for each user/default workspace.
5. Add `workspace_id` to subscriptions or create `workspace_subscriptions` facade.
6. Keep old mobile endpoints working on old tables.
7. Connect Next.js cabinet to `/api/v2` only.

Recommended migration tooling: `golang-migrate/migrate` or `pressly/goose`. For current repo simplicity, `goose` is enough.

## 4. API Contract Draft

### Common conventions

Base path:

```text
/api/v2
```

Auth:

```http
Authorization: Bearer <token>
```

Common error:

```json
{
  "error": {
    "code": "forbidden",
    "message": "Недостаточно прав",
    "details": {}
  }
}
```

Pagination:

```text
?limit=50&cursor=...
```

For MVP, offset can be avoided. Cursor can be added only where lists can grow.

### Bootstrap

```http
GET /api/v2/bootstrap
```

Returns current user, current workspace, membership, subscription access, and enough summary for initial cabinet loading.

```json
{
  "user": { "id": 1, "email": "user@example.com" },
  "workspace": {
    "id": 10,
    "name": "REUP.goals",
    "status": "active"
  },
  "membership": { "role": "owner" },
  "subscription": {
    "status": "trial_active",
    "access": true,
    "next_payment_at": "2026-07-10T00:00:00Z",
    "grace_until": null
  }
}
```

### Workspaces

- `GET /api/v2/workspaces/current`
- `PATCH /api/v2/workspaces/current`
- `GET /api/v2/workspaces`

### Knowledge Base

- `GET /api/v2/knowledge/blocks`
- `GET /api/v2/knowledge/blocks/{type}`
- `PATCH /api/v2/knowledge/blocks/{type}`
- `POST /api/v2/knowledge/blocks/{type}/extract-facts`
- `GET /api/v2/knowledge/notes`
- `POST /api/v2/knowledge/notes/{id}/confirm`
- `POST /api/v2/knowledge/notes/{id}/reject`
- `POST /api/v2/knowledge/evaluate`

MVP request:

```json
{
  "raw_content": "Мы продаём сервис микробизнесу...",
  "status": "draft"
}
```

### Strategy

- `GET /api/v2/strategy/current`
- `POST /api/v2/strategy/generate`
- `PATCH /api/v2/strategy/{id}`
- `POST /api/v2/strategy/{id}/approve`
- `GET /api/v2/strategy/{id}/artifacts`
- `PATCH /api/v2/strategy/{id}/artifacts/{type}`

### Course

- `GET /api/v2/course/current`
- `POST /api/v2/course/from-strategy/{strategyId}`
- `PATCH /api/v2/course/{id}`
- `POST /api/v2/course/{id}/activate`

### Tactics

- `GET /api/v2/tactics/summary`
- `GET /api/v2/directions`
- `POST /api/v2/directions`
- `GET /api/v2/directions/{id}`
- `PATCH /api/v2/directions/{id}`
- `GET /api/v2/directions/{id}/projects`
- `POST /api/v2/directions/{id}/projects`
- `PATCH /api/v2/projects/{id}`
- `GET /api/v2/risks`
- `POST /api/v2/risks`
- `PATCH /api/v2/risks/{id}`
- `GET /api/v2/opportunities`
- `POST /api/v2/opportunities`
- `PATCH /api/v2/opportunities/{id}`

### Tasks v2

- `GET /api/v2/tasks`
- `POST /api/v2/tasks`
- `GET /api/v2/tasks/{id}`
- `PATCH /api/v2/tasks/{id}`
- `POST /api/v2/tasks/{id}/transition`
- `POST /api/v2/tasks/{id}/evaluate`
- `GET /api/v2/directions/{id}/tasks`
- `GET /api/v2/directions/{id}/board`

Create task:

```json
{
  "title": "Провести 8 интервью с отказавшимися",
  "description": "Собрать причины отказов...",
  "direction_id": 12,
  "project_id": 34,
  "source": {
    "source_type": "project",
    "source_id": "34",
    "source_title": "Серия проблемных интервью"
  }
}
```

Transition:

```json
{
  "status": "in_progress"
}
```

Allowed transitions:

- `free -> in_progress`
- `free -> archived`
- `in_progress -> free`
- `in_progress -> done`
- `in_progress -> archived`
- `done -> archived`

### Chats

- `GET /api/v2/chats?scope=strategy&entity_type=strategy&entity_id=1`
- `POST /api/v2/chats`
- `GET /api/v2/chats/{threadId}/messages`
- `POST /api/v2/chats/{threadId}/messages`
- `POST /api/v2/chats/{threadId}/actions/{actionId}/apply`

AI response envelope:

```json
{
  "message": {
    "text": "Я бы усилил направление платящего сегмента..."
  },
  "structured": {
    "type": "tactics_suggestion",
    "data": {}
  },
  "proposed_actions": [
    {
      "id": "act_123",
      "type": "create_task",
      "label": "Создать задачу",
      "payload": {}
    }
  ],
  "warnings": [],
  "confidence": 0.74,
  "needs_user_confirmation": true
}
```

## 5. AI Architecture Plan

### Core rule

Frontend never calls OpenAI directly. Backend receives user input, stores it, builds context, calls AI, validates structured JSON, stores output, and only then returns response.

### AI services

Planned services:

- `FactExtractor`: extracts facts from knowledge input/chat/task descriptions.
- `KnowledgeQualityEvaluator`: evaluates knowledge completeness and gaps.
- `StrategyArchitect`: generates strategy draft and artifacts.
- `CourseAssistant`: explains course and detects deviations.
- `TacticsAssistant`: suggests directions, projects, risks, opportunities, tasks.
- `TaskEvaluator`: scores tasks against course/strategy/tactics.

### Prompt versioning

Store prompt versions in code first:

```text
internal/v2/ai/prompts/
  fact_extractor_v1.go
  strategy_architect_v1.go
  task_evaluator_v1.go
```

Each AI result saves:

- `scenario`
- `model`
- `prompt_version`
- `input_snapshot`
- `output_json`
- token/cost/latency metadata
- status/error.

Later, prompts can move to DB/admin panel if iteration speed requires it.

### Structured outputs

Every AI service must return strict JSON and be validated before saving. If JSON is invalid:

1. Save failed AI evaluation.
2. Return user-safe error.
3. Do not mutate business entities.

### Background jobs

MVP can start with synchronous AI for short operations, but architecture should allow async jobs for:

- full knowledge evaluation;
- strategy generation;
- bulk task evaluation;
- large document processing.

For the current scale, a simple DB-backed job table is enough before adding Redis/queue.

## 6. Security and Access

Must do before serious production v2 usage:

- move JWT secret to env;
- replace `cors.AllowAll()` with configured origins;
- add request body size limits;
- validate all IDs through workspace access checks;
- add permission checks for workspace mutation;
- add rate limits for auth, AI, reset password, code resend;
- ensure no AI/API/payment/email secrets are logged;
- add healthcheck endpoint;
- add structured logging for production errors.

Workspace isolation rule:

> Every v2 query must filter by `workspace_id` obtained through membership, never trust only entity ID from URL.

## 7. Subscription and Access

Target model: subscription belongs to workspace, not only user.

MVP bridge:

- keep current `subscriptions.user_id` for existing payment flow;
- create default workspace per user;
- expose subscription in v2 as workspace subscription;
- when schema stabilizes, add `workspace_id` to `subscriptions` and backfill from owner/default workspace.

Access statuses:

- `trial_active`
- `active`
- `past_due`
- `cancelled`
- `expired`
- `payment_required`

Rules:

- `trial_active` and `active`: full access.
- `past_due`: 14-day grace, show payment update screen, keep limited access.
- `expired` / `payment_required`: block creation/edit/AI, allow payment/account screens.
- `cancelled`: access until period end, then payment required.

## 8. Implementation Roadmap

### Phase 0. Hardening before v2 work

1. Add real migration runner.
2. Move JWT secret to env.
3. Add `/healthz`.
4. Restrict CORS via env.
5. Add common JSON error helpers for new `/api/v2`.

### Phase 1. Workspace foundation

1. Create `workspaces` and `workspace_memberships`.
2. Backfill default workspace for existing users.
3. Add `GET /api/v2/bootstrap`.
4. Add current workspace middleware.
5. Add basic owner-only permission layer.

This is the first important vertical slice. Without it, all later v2 data will be attached to the wrong root.

### Phase 2. Knowledge Base MVP

1. Create 12 knowledge blocks per workspace.
2. Add list/get/update endpoints.
3. Connect `/cabinet-v2/knowledge-base` to API.
4. Add basic event logging.

Do not add file upload yet.

### Phase 3. AI notes and knowledge quality

1. Add `v2_ai_notes`.
2. Implement FactExtractor as save-suggested-facts flow.
3. Implement KnowledgeQualityEvaluator.
4. Store AI evaluations.
5. Show readiness/gaps in frontend.

### Phase 4. Strategy and Course

1. Add strategies/artifacts.
2. Implement generate draft strategy from knowledge.
3. Implement approve strategy.
4. Generate course from active strategy.
5. Connect Strategy and Course frontend screens.

### Phase 5. Tactics

1. Add directions/projects/risks/opportunities.
2. Connect tactics list/detail screens.
3. Add coverage summary.
4. Add TacticsAssistant suggestions as draft/proposed actions.

### Phase 6. Tasks v2

1. Add v2 tasks and task sources.
2. Implement list/create/update/transition.
3. Connect task boards and drag-drop transitions.
4. Add TaskEvaluator.
5. Keep legacy `/tasks` only as temporary compatibility until the mobile app switches to `/api/v2`.

### Phase 7. Chats

1. Add chat threads/messages.
2. Implement scope-aware chat endpoint.
3. Save structured AI responses and proposed actions.
4. Connect strategy/tactics/tasks chat UI.

### Phase 8. Subscription workspace migration

1. Add workspace_id to subscription model or create workspace subscription table.
2. Gate `/api/v2` mutation/AI endpoints by workspace access.
3. Keep account/paywall frontend connected.

### Phase 9. Pre-production hardening

1. Add integration tests for critical v2 APIs.
2. Add smoke test script for auth -> bootstrap -> knowledge -> strategy -> tasks.
3. Add structured logs around AI and payment failures.
4. Document deploy and rollback.

## 9. What to postpone

Postpone until product demand is validated:

- multi-workspace switching UI beyond backend readiness;
- team invites and role UI;
- file upload and document parsing;
- external integrations;
- complex prompt admin;
- realtime collaboration;
- enterprise audit UI;
- advanced analytics dashboard.

## 10. Code Style and Development Rules

### General backend rules

- New product functionality belongs to `internal/v2/...`.
- Existing `internal/goals` and `internal/tasks` must not be extended for v2 product logic.
- Handlers decode requests, call services, and encode responses. They should not contain business rules.
- Services own business logic, status transitions, permissions, subscription gates, and transactions.
- Repositories own SQL. Do not write non-trivial SQL directly inside handlers.
- AI calls must go through AI service/orchestration layer, never directly from handlers.
- No hardcoded `user_id`, `workspace_id`, API keys, tokens, provider secrets, prices, plan names, or model names.
- Every workspace-bound operation must validate membership/access using backend context, not a frontend-provided workspace ID alone.
- Every new mutation should write an audit/event entry when it changes important product data.
- Temporary shortcuts must have explicit `TODO(v2): ...` and should be visible in roadmap or issue tracker.

### Suggested module structure

For each major v2 feature:

```text
internal/v2/<feature>/
  handlers.go      HTTP handlers and route registration
  service.go       business logic
  repository.go    SQL/database access
  types.go         domain types and DTOs
  validation.go    input validation
  errors.go        feature-specific error mapping if needed
  *_test.go        unit/service tests
```

AI-heavy features may additionally have:

```text
internal/v2/<feature>/ai.go
internal/v2/ai/prompts/<scenario>_v1.go
```

Do not create a new abstraction layer just because it looks clean. Add structure when it prevents real mixing of API, business, SQL, and AI concerns.

### Database rules

- All v2 schema changes should go through migrations, not only `EnsureSchema`.
- Do not destructively alter current production tables without a migration/backfill/rollback plan.
- Use foreign keys for core ownership relations where it is safe.
- Prefer `archived_at` / status over physical deletes for product data.
- Store AI structured outputs as JSONB, but keep core queryable fields as normal columns.
- Add indexes for common workspace queries before production traffic:
  - `workspace_id`
  - `workspace_id, status`
  - `workspace_id, created_at DESC`
  - entity links such as `direction_id`, `project_id`.
- Never trust `workspace_id` from frontend as proof of access.
- For deprecated legacy tables, write explicit migration scripts before moving or deleting data.

### API rules

- v2 endpoints live under `/api/v2`.
- Use resource-oriented API, not one endpoint per visual component.
- Use one JSON error format for v2:

```json
{
  "error": {
    "code": "validation_error",
    "message": "Проверьте данные",
    "details": {}
  }
}
```

- Do not return password hashes, reset tokens, payment secrets, AI provider keys, raw provider secrets, or internal prompt text.
- Use `401` for unauthenticated, `403` for no access, `404` when entity is not visible in this workspace, `422` for validation errors.
- For lists that can grow, design pagination from the start.
- Keep frontend DTOs stable. If response shape changes, update frontend API client and types in the same task.
- Do not expose old mobile/MVP response shapes as v2 contracts unless they actually match the v2 domain.

### AI rules

- Frontend never calls AI providers directly.
- Every AI scenario has a name and prompt version.
- Every AI call should save:
  - scenario;
  - model/provider;
  - prompt version;
  - input snapshot or reference;
  - structured output;
  - status/error;
  - token/cost/latency metadata when available.
- AI can create suggestions/drafts. It must not silently approve strategy, change course, delete tasks, or mutate critical business data without an explicit backend action representing user confirmation.
- Validate AI JSON before applying or saving as structured result.
- Limit input size and summarize long context before sending to model.
- Failed AI calls should not corrupt user state.

### Frontend integration rules

- UI components should not import mock data once a screen is connected to API.
- API calls go through a frontend API client/data source layer.
- Components should render loading/error/empty/forbidden states.
- Frontend should not contain business-critical permission logic; it can hide buttons, but backend enforces rules.
- Keep visual design aligned with approved mockups; API integration should not trigger redesign unless explicitly requested.
- When replacing mock data, keep DTO mapping separate from presentational components.

### Validation and check rules

Before backend merge/deploy:

```sh
go test ./...
go build ./cmd/api
gofmt
```

Before frontend merge/deploy:

```sh
npm run check
npm run build
```

Before production deploy:

- verify required env variables;
- run migrations;
- run auth smoke test;
- run `/api/v2/bootstrap` smoke test once it exists;
- verify no secrets are present in frontend build;
- verify logs do not include passwords, email codes, reset tokens, payment secrets, or AI API keys.

### Explicitly forbidden

- Building v2 around old `goals` / `tasks` tables as the long-term model.
- Adding AI logic directly inside React components.
- Adding AI logic directly inside HTTP handlers.
- Letting frontend decide workspace access.
- Creating one-off backend endpoints that only mirror a current card/block if a normal resource API would work.
- Storing important AI output only as unstructured chat text.
- Applying AI suggestions to confirmed business data without user-confirmation flow.

## 11. Migration Strategy From Current Product To v2

The expected direction is not "old and v2 forever". It is:

```text
current backend -> v2 core -> website/mobile switch to v2 -> legacy migration/archive -> legacy removal
```

### Migration stages

1. Keep current auth/email/payment running.
2. Add v2 workspace foundation and bootstrap.
3. Route new Next.js cabinet screens to `/api/v2`.
4. Build all new product functionality only in v2 tables.
5. Add compatibility only where needed for current production stability.
6. When mobile app is redesigned, connect it to `/api/v2` instead of extending old endpoints.
7. Backfill/migrate useful old user goals/tasks into v2 entities if productively valuable.
8. Freeze old `/goal` and `/tasks` endpoints.
9. Remove or archive old tables after usage drops to zero and backup is confirmed.

### What can be migrated

- `users` -> stay.
- `auth_email_codes` -> stay.
- `subscriptions` -> migrate from user-level to workspace-level.
- old `goals` -> optional import into `v2_strategies` or `v2_courses` only if data quality is useful.
- old `tasks` -> optional import into `v2_tasks` as unlinked/manual tasks.
- old `task_ai_state` -> mostly not worth migrating, because v2 evaluation model is richer and contextual.

## 12. Open Questions

1. In MVP, should every user automatically get a workspace at registration, or only after first cabinet visit?
2. Can a user own multiple workspaces in the first paid version?
3. Should unpaid users keep read-only access to existing strategy/tasks, or should cabinet be fully blocked except account/payment?
4. Which role can approve strategy: only owner, or owner/admin?
5. Do we need user assignment in tasks for MVP, or `owner_user_id` can remain optional/self?
6. Should strategy approval automatically create course, directions, and starter tasks, or only suggestions?
7. What is the first AI workflow we want to ship to users: knowledge extraction, strategy generation, or task evaluation?
8. What AI cost limit per workspace/month is acceptable for the first tariff?
9. Do we need to keep mobile app on old goals/tasks long-term, or should it eventually consume `/api/v2`?
10. Should CloudPayments subscription be billed per workspace or per user seat when team functionality arrives?

## 13. First Codex Tasks To Execute

Recommended next implementation tasks:

1. Backend hardening: JWT env, CORS env, healthcheck, v2 response helpers.
2. Add migration runner.
3. Add workspace and membership tables with backfill.
4. Add `/api/v2/bootstrap`.
5. Add knowledge block tables and API.
6. Replace Knowledge Base frontend mock with API.

This sequence is boring in the best possible way: it creates the foundation that lets every next feature land faster and with less chaos.
