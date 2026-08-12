# REUP.goals Agent Runtime

The runtime owns the OpenAI Agents SDK loop. PostgreSQL and the Go API remain
the source of truth for users, business entities, approvals, usage, and audit
events.

In production the runtime is colocated with the Go API in Germany. Both
services communicate over loopback, use OpenAI directly from the German host,
and keep PostgreSQL as the source of truth in Russia over TLS. Runtime tools use
a short-lived per-run token to read or apply data through the protected local
Go API. Mutation tools always pause for user approval before the Go API changes
the source of truth. Request bodies and business context are not written to
runtime logs.

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
- `GO_INTERNAL_URL` (the local Go API origin in production)

Optional variables:

- `PORT` (default `8091`)
- `LISTEN_HOST` (default `127.0.0.1`)

Production deployment templates live in `deploy/production`. The runtime must
remain bound to loopback and must not be exposed through nginx.

The runtime does not publish SDK traces because they can contain sensitive
business data. Verified run stages, tool activity, token usage, and errors are
stored by the Go API.
