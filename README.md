# REUP.goals backend

Go API for the REUP.goals product. The current product API is the workspace-scoped `/api/v2` modular monolith; legacy goal/task routes remain only for compatibility with the old Flutter client.

## Local verification

```sh
go test ./...
go vet ./...
go run ./cmd/api
```

## Required environment

```sh
APP_ENV=development
DB_HOST=127.0.0.1
DB_PORT=5432
DB_USER=
DB_PASSWORD=
DB_NAME=
DB_SSLMODE=disable
JWT_SECRET=
OPENAI_API_KEY=
OPENAI_MODEL=gpt-5.6-luna
OPENAI_AUDITOR_MODEL=gpt-5.6-luna
OPENAI_AUDITOR_COMPACT_THRESHOLD=24000
OPENAI_ADVISOR_MODEL=gpt-5.6-luna
OPENAI_TASK_MODEL=gpt-5.6-luna
OPENAI_ADVISOR_COMPACT_THRESHOLD=24000
OPENAI_PROXY_URL=direct
HTTP_WRITE_TIMEOUT=0s
CORS_ALLOWED_ORIGINS=http://localhost:3000
```

`JWT_SECRET` must contain at least 32 characters. Staging and production require explicit HTTPS CORS origins, a database password, and TLS for a remote PostgreSQL server (`DB_SSLMODE=verify-full` is preferred). Local PostgreSQL on loopback may use `disable`.

The web client authenticates through a secure HttpOnly cookie. Bearer JWT remains supported for the compatibility client. Sessions expire after seven days and are invalidated by logout or password reset.

See [docs/security-baseline.md](docs/security-baseline.md) for the security model, operational controls, and release checklist.
