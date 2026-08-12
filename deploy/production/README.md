# Production deployment

Production runs as one application cell in Germany. PostgreSQL temporarily
remains in Russia and is accessed by the German Go API over TLS.

## Data path

1. Browser and mobile clients call `https://api.reupgoals.pro` in Germany.
2. The German Go API owns authentication, payments, tasks, directions,
   documents, approvals, quotas, audit records, and all PostgreSQL access.
3. The agent runtime is available only on German loopback. It calls OpenAI
   directly and uses protected loopback callbacks to read canonical context or
   propose structured actions.
4. PostgreSQL is not publicly exposed beyond its managed TLS endpoint. The
   browser never connects to PostgreSQL or OpenAI directly.

There is no public AI gateway, reverse SSH tunnel, or request path through the
Russian application host.

## First cutover

1. Ensure Germany is reachable with
   `~/.ssh/reup_goals_staging_deploy` and Russia with the `reup` SSH alias.
2. Run `scripts/migrate-production-to-germany.sh` from the backend repository.
3. Wait until the script reports that the German candidate is ready, then
   point the `A` records for `reupgoals.pro`, `www.reupgoals.pro`, and
   `api.reupgoals.pro` to `167.233.230.212`. Keep the staging records unchanged.

The migration first runs local backend, agent, and frontend checks. It then
copies existing production settings without printing secrets, creates isolated
production services on ports `8082` and `8092`, deploys the frontend, waits for
DNS, obtains TLS certificates, verifies public endpoints, and only then stops
the legacy Russian application workers.

During DNS propagation, the old Russian API address becomes a temporary nginx
bridge to the verified German API. The Russian Go API and reverse AI tunnel are
stopped only after that bridge passes a readiness check. The bridge removes
itself after 24 hours, so clients with a cached DNS answer do not lose uploads
or active agent requests and no permanent second application path remains.

The script can wait up to seven hours for the current long-lived DNS records.
If DNS has not propagated by then, it exits without stopping the Russian
production service. It is safe to run the same command again. If the Russian
host is temporarily unavailable during final cleanup, German production stays
active and the old workers are left untouched instead of risking an outage.

## Normal releases

After the first cutover, use `scripts/promote-production-all.sh`. It requires
clean `main` revisions that match `origin/main`, runs backend, agent, and
frontend checks, deploys the colocated German services with rollback, then
verifies the public production endpoints.

Staging remains isolated on the same German host with its own domains, ports,
environment files, services, and job queue namespace.

## Required safeguards

- Keep `DB_SSLMODE` at `require`, `verify-ca`, or `verify-full`.
- Keep production and staging job queue namespaces different.
- Keep the production OpenAI key only in root-readable German environment
  files.
- Do not reuse staging payment credentials, signing secrets, email lists, or
  backups in production.
- Complete the cross-border processing checks in
  `docs/privacy-compliance.md`, because the German API processes business data
  while PostgreSQL remains in Russia.
