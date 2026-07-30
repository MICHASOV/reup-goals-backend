# REUP.goals Agent Runtime

The runtime owns the OpenAI Agents SDK loop. PostgreSQL and the Go API remain
the source of truth for users, business entities, approvals, usage, and audit
events.

The service listens only on `127.0.0.1`. The Go API signs a short-lived token
for every run; tools use that token to call protected internal Go endpoints.
Mutation tools always pause for user approval before the Go API applies a
change.

## Local development

```sh
npm ci
npm run typecheck
npm test
npm run build
npm start
```

Required environment variables:

- `OPENAI_API_KEY`
- `AGENT_RUNTIME_SECRET` (at least 32 characters)

Optional variables:

- `PORT` (default `8091`)
- `GO_INTERNAL_URL` (default `http://127.0.0.1:8080`)
- `OPENAI_PROXY_URL`

The runtime does not publish SDK traces because they can contain sensitive
business data. Verified run stages, tool activity, token usage, and errors are
stored by the Go API.
