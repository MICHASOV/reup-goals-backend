# Production deployment

Production is split into a Russian source-of-truth cell and a stateless German
AI cell.

## Data path

1. Browser and mobile clients call only `https://api.reupgoals.pro`.
2. The Russian Go API owns PostgreSQL, authentication, payments, tasks,
   directions, documents, approvals, quotas, and audit records.
3. For AI work the Go API sends a compact brief, the current user request, and
   a short-lived run token to `https://ai.reupgoals.pro`.
4. The German runtime calls OpenAI directly. When it needs canonical business
   data, its tools call protected endpoints on the Russian API with the run
   token.
5. Proposed mutations return to the Russian API as structured actions and are
   applied only after user confirmation.

PostgreSQL is never exposed to the German host. The real OpenAI key is never
stored on the Russian host. The German service does not log prompts, files, or
response bodies.

## Required before first deploy

1. On the Russian host, install `backend.env.example` as
   `/etc/reup-goals/backend.env`; never commit the filled file.
2. On the German host, install `ai-runtime.env.example` as
   `/etc/reup-goals/ai-production.env` with mode `600`.
3. Generate two different random secrets. Copy `AGENT_RUNTIME_SECRET` to both
   hosts. Copy German `AI_GATEWAY_SECRET` to Russian
   `OPENAI_GATEWAY_SECRET`.
4. Point `ai.reupgoals.pro` to the German host, install
   `nginx-ai.conf.example`, and replace `RUSSIAN_API_PUBLIC_IP` with the fixed
   egress IP of the Russian API. Production AI uses port `8092`; staging can
   continue using `8091` on the same host.
5. Choose `PRIVACY_MODE` and `DATA_RESIDENCY_REGION` from the approved data map.
6. Complete the regulator, processor-contract, and transfer checks in
   `docs/privacy-compliance.md`. Prompts and selected business context still
   cross the border even though the database remains in Russia.
7. Provision PostgreSQL on a private network with TLS verification, encrypted
   disks, encrypted same-region backups, PITR, and a tested restore.
8. Install `fonts-dejavu-core`, set `INVOICE_FONT_PATH`, install the Go binary,
   create an unprivileged `reupgoals` user, and install `reup-goals.service`.
9. Install `nginx-api.conf.example` on the Russian host. Keep PostgreSQL
   private and permit SSH only through the approved VPN or bastion.
10. Configure monitoring and an on-call destination for both hosts.
11. Generate a unique `BILLING_ADMIN_KEY` with at least 32 random bytes and
    verify one idempotent manual invoice activation during release smoke tests.

## Release order

`scripts/promote-production-all.sh` deploys the German AI runtime first and
checks that both authenticated surfaces are ready. It then deploys the Russian
API, removes any OpenAI key left in its environment, switches traffic to the
remote runtime, and disables the old colocated runtime only after the public API
passes health checks. The frontend is deployed last.

The local SSH aliases are `reup-ai` for Germany and `reup` for Russia. Override
them with `REUP_AI_SSH_TARGET` and `REUP_PRODUCTION_SSH_TARGET` when needed.

Do not reuse staging secrets, databases, OpenAI projects, email lists, payment terminals, or backups.
