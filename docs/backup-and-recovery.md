# Backup and recovery

## Objectives

- Target RPO: 24 hours for MVP, moving to 15 minutes with PostgreSQL PITR before paid business use.
- Target RTO: 8 hours for MVP, moving to 2 hours before a contractual availability commitment.
- Keep database, object storage, logs, and backups in the approved legal region.

## Minimum production controls

1. Provider-managed encrypted PostgreSQL backups with PITR where available.
2. Daily encrypted backup, maximum 30-day product-data lifecycle, and a separate documented schedule for accounting records.
3. Backup credentials in the regional secret manager, separate from application credentials.
4. Immutability or deletion protection for the shortest period compatible with incident recovery.
5. Quarterly restore drill into an isolated environment with synthetic credentials and blocked outbound email/AI calls.
6. Evidence for backup age, restore duration, integrity checks, and deletion propagation.

## Restore drill

1. Open an incident/change ticket and name the restore point.
2. Restore into an isolated private network.
3. Rotate all restored credentials before starting the application.
4. Verify migrations, workspace isolation, representative document counts, and checksum/sample integrity.
5. Confirm that deleted subjects are not reintroduced into the live system; repeat post-restore deletion jobs where required.
6. Destroy the drill environment and record the result in the audit log.

No production data may be restored into the German staging environment.
