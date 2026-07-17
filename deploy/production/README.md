# Production deployment skeleton

This directory is provider-neutral. Use it to build separate Russian and EU regional cells without forking application code.

## Required before first deploy

1. Copy `backend.env.example` to the host secret store or `/etc/reup-goals/backend.env`; never commit the filled file.
2. Choose `PRIVACY_MODE` and `DATA_RESIDENCY_REGION` from the approved data map.
3. Complete the regulator, processor-contract, and transfer checks in `docs/privacy-compliance.md`.
4. Provision PostgreSQL on a private network with TLS verification, encrypted disks, encrypted same-region backups, PITR, and a tested restore.
5. Install the binary under `/opt/reup-goals/backend`, create an unprivileged `reupgoals` user, and install `reup-goals.service`.
6. Install `nginx-api.conf.example` with the real API domain and certificate paths.
7. Restrict the firewall to 80/443 publicly; permit SSH only through the approved VPN/bastion; keep PostgreSQL private.
8. Configure monitoring and an on-call destination before accepting customers.

Do not reuse staging secrets, databases, OpenAI projects, email lists, payment terminals, or backups.
