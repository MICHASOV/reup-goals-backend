# Regional deployment architecture

## Current environments

| Environment | Intended data | Privacy mode | Region |
| --- | --- | --- | --- |
| local | developer fixtures | `development` | `local-dev` |
| staging | synthetic/test accounts only | `test` | `eu-de` |
| Russian production | real Russian customer data | `ru_152fz` or `dual` | `ru-*` |
| EU production, when launched | EEA customer data | `gdpr` | `eu-*` |

Staging in Germany is not the primary database for Russian citizens. Do not invite real customers to staging or copy production data into it.

## Deployable unit

Each regional cell contains:

- stateless API instances running as an unprivileged system user;
- PostgreSQL with TLS, encrypted disks, point-in-time recovery, and encrypted backups in the same legal region;
- regional object storage for uploads before external processing;
- regional Nginx/load balancer and WAF/rate limiting;
- regional logs, metrics, alerting, and an append-only security/audit export;
- region-scoped secrets and provider credentials.

The same application binary runs in every cell. `PRIVACY_MODE` and `DATA_RESIDENCY_REGION` select policy; provider adapters select regional implementations. This avoids forking product code by country.

## Russian production reference

1. Choose a Russian cloud/data-center contract that explicitly locates primary DB, replicas, object storage, logs, and backups in Russia.
2. Keep account creation and initial collection in the Russian cell.
3. Send only a minimized, task-specific context to OpenAI after completing the Article 12 transfer process.
4. Record provider, destination, purpose, data categories, request ID, and deletion lifecycle without logging content.
5. Keep a switch to disable external AI while preserving access to stored documents.

## EU production reference

Use an EU regional cell and documented processor agreements. If a recipient is outside the EEA, configure the approved transfer mechanism and transfer impact assessment. Do not silently replicate EU content into the Russian cell.

## DevOps controls

- production and staging use separate cloud projects, databases, secrets, OpenAI projects, email lists, billing terminals, and DNS;
- SSH password and root login are disabled; admin access uses named keys through a VPN/bastion and is logged;
- only 80/443 are public; SSH is allowlisted; PostgreSQL is private;
- deployment uses immutable binaries/images, read-only filesystem where practical, non-root execution, and rollback to the previous signed artifact;
- database migrations are forward-only, transactionally locked, backed up, and tested on a production-like copy with synthetic data;
- encrypted backup restore is tested quarterly and before major schema migrations;
- alerts cover 5xx, authentication abuse, queue failures, latency, storage, backup age, secret expiry, and anomalous AI spend;
- dependency, static, race, integration, and browser tests block deployment.
