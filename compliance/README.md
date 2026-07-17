# Compliance working registers

These files are operational templates, not public documents and not proof of compliance by themselves.

- `processing-activities.csv`: GDPR Article 30 / Russian processing-purpose working inventory.
- `processors.csv`: provider, country, purpose, contract, transfer, retention, and deletion status.
- `security-risk-register.csv`: security/privacy risks, controls, owners, and review dates.

Rules:

1. Never put passwords, API keys, customer content, or exported personal data in these files.
2. Assign a real owner and review date before production launch.
3. Update the processor register before changing hosting, AI, email, payment, monitoring, support, or backup providers.
4. Store signed contracts, DPIAs, transfer assessments, regulator receipts, and incident evidence in a restricted document system, not in git.
5. Legal and security owners approve completed registers before a production region begins accepting real data.
