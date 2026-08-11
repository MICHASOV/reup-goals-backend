# REUP.goals Agent Runtime

The runtime owns the OpenAI Agents SDK loop. PostgreSQL and the Go API remain
the source of truth for users, business entities, approvals, usage, and audit
events.

In production this service runs in Germany and contains two authenticated
surfaces behind nginx:

- the Agents SDK runtime used by the Russian Go API;
- a narrow OpenAI-compatible gateway used for responses, files, vector stores,
  conversations, and transcription.

The real OpenAI key exists only on the German host. The Go API and PostgreSQL
remain in Russia. The Go API sends a compact business brief and a short-lived
per-run token; runtime tools use that token to read or apply data through the
protected Russian API. Mutation tools always pause for user approval before the
Go API changes the source of truth. Request bodies and business context are not
written to runtime logs.

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
- `AI_GATEWAY_SECRET` (a different secret, at least 32 characters)
- `GO_INTERNAL_URL` (the Russian API HTTPS origin in production)

Optional variables:

- `PORT` (default `8091`)
- `LISTEN_HOST` (default `127.0.0.1`)
- `OPENAI_UPSTREAM_URL` (default `https://api.openai.com`)
- `OPENAI_PROXY_URL`
- `AI_GATEWAY_MAX_REQUEST_BYTES` (default 128 MiB)

Production deployment templates live in `deploy/production`. Keep nginx or the
host firewall restricted to the fixed egress IP of the Russian API even though
both runtime surfaces also require independent bearer secrets.

The runtime does not publish SDK traces because they can contain sensitive
business data. Verified run stages, tool activity, token usage, and errors are
stored by the Go API.
