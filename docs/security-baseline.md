# REUP.goals Security Baseline

Status: mandatory baseline for staging and production.

## 1. Protected data

REUP.goals stores confidential business information: financial metrics, strategy, customer and team data, uploaded documents, chat history, decisions, and AI-derived claims. Treat all workspace content as confidential. Authentication secrets, billing identifiers, reset codes, and provider credentials are restricted secrets.

Core rules:

- every product query is scoped by `workspace_id` and active membership;
- business data is never logged as request bodies or prompt text;
- secrets are supplied at runtime and never committed;
- deletion covers PostgreSQL and persistent OpenAI files/vector stores;
- production access follows least privilege and is auditable.

## 2. Application controls

Implemented baseline:

- seven-day HS256 sessions with issuer, audience, expiry, `auth_version`, logout/password-reset revocation, and a 32-character minimum secret;
- HttpOnly, Secure, SameSite session cookie for web; Bearer compatibility for the legacy client;
- PBKDF2-SHA256 password hashes at 600,000 iterations with lazy rehashing and a 12-character minimum for new passwords;
- explicit production CORS and trusted-Origin checks for cookie-authenticated mutations;
- request size limits, upload extension allowlists, path-safe filenames, panic recovery, security headers, and per-IP rate limits;
- CloudPayments webhooks fail closed when the signing secret is absent;
- Prompt Registry and AI usage policies require a separate admin key;
- task assignees must be active members of the workspace;
- outbound OpenAI requests use bounded response reads, streaming uploads, connection pooling, and operation timeouts;
- account/Knowledge Base deletion removes persistent OpenAI files and vector stores before local identifiers are erased;
- migrations are transactional and serialized with a PostgreSQL advisory lock.

## 3. Infrastructure controls required before customer data

These controls require provider or server configuration and cannot be guaranteed by application code:

1. Enable encrypted Hetzner backups and PostgreSQL backups. Keep at least daily backups with a documented retention period and perform a restore test before production launch.
2. Restrict the Hetzner firewall to `80/443` publicly and `22` only from approved administrator IPs or a managed VPN. Disable SSH password login and root login after confirming the deployment user.
3. Store an exact SSH host key in the GitHub secret `STAGING_SSH_KNOWN_HOSTS`; remove the `ssh-keyscan` fallback after the secret is installed.
4. Rotate SSH, JWT, OpenAI, Unisender, CloudPayments, and database credentials. Keep production and staging credentials separate. Record owner and rotation date for each secret.
5. Run PostgreSQL with TLS when it is not on loopback. Use `DB_SSLMODE=verify-full` with a trusted CA for a managed remote database.
6. Confirm an OpenAI data-processing agreement and retention mode. API data is not used for training by default, but eligible endpoints can retain abuse-monitoring data for up to 30 days. Apply for Modified Abuse Monitoring or Zero Data Retention if the business risk requires it.
7. Define log retention and access. Journal, Nginx, application, and AI-call logs must not contain prompt bodies, uploaded file contents, passwords, tokens, or reset codes.
8. Enable uptime, latency, 5xx, queue-depth, database-capacity, backup-age, and AI-cost alerts. Assign an on-call owner and an incident contact.
9. Before adding a second API instance, move rate limiting to Redis or the edge and verify that background jobs use a shared, idempotent queue.
10. Run an external penetration test before onboarding companies with sensitive financial, employee, or customer data.

## 4. Data deletion and retention

- Account deletion must fail visibly if external OpenAI cleanup fails. The user record remains so cleanup can be retried.
- Knowledge Base reset follows the same external-first rule.
- OpenAI files and vector stores are explicitly deleted. Provider-side response retention still follows the configured OpenAI data controls.
- Database backup retention may delay physical erasure. This must be disclosed in the privacy policy with the maximum deletion window.
- Analytics and audit data need a written retention schedule before production.

## 5. Release security gate

Every release must pass:

```sh
go test ./...
go test -race ./...
go vet ./...
govulncheck ./...
gosec -quiet -exclude-generated ./...
```

The web app must pass type checking, linting, production build, dependency audit, and browser smoke tests. A failed security check blocks deployment unless a time-bounded exception is documented with an owner.

## 6. Incident minimum

If business data may have been exposed:

1. stop the affected service or credential;
2. preserve logs and identify affected workspaces and time range;
3. rotate relevant credentials and revoke sessions;
4. notify the incident owner and legal/privacy owner;
5. communicate with affected customers according to applicable law and contracts;
6. document root cause, corrective action, and a regression test.
