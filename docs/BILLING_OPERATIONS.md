# Billing operations

## Current release mode

- `BILLING_PAYMENTS_ENABLED=false` keeps card checkout and payment webhooks disabled.
- `BILLING_ENFORCEMENT_ENABLED=false` keeps staging workspaces usable without creating a fake trial.
- Workspace owners can select a plan, save buyer details, generate a PDF invoice, download it, and email it.
- A paid invoice is confirmed manually until the product admin panel is available.
- There is no trial period.

## Manual payment confirmation

Set a long random `BILLING_ADMIN_KEY` only for the backend environment that should accept confirmations.
The endpoint is unavailable while the key is empty.

```sh
curl -X POST https://api.example.com/api/v2/admin/billing/invoices/confirm \
  -H "Content-Type: application/json" \
  -H "X-Billing-Admin-Key: $BILLING_ADMIN_KEY" \
  -d '{"invoice_id":123,"confirmed_by":"bank-statement-2026-07-27"}'
```

Confirmation is idempotent. For a subscription invoice it activates the selected plan and starts its
seven-day AI window. For a reset invoice it adds one full weekly allowance to the persistent additional
reserve. The action also records a manual payment.

## Enabling a real provider later

1. Configure provider credentials in the production secret store.
2. Verify provider callbacks and idempotency in staging with `BILLING_PAYMENTS_ENABLED=true`.
3. Run a real low-value payment and refund.
4. Set `BILLING_ENFORCEMENT_ENABLED=true` only after both card and invoice activation paths are verified.

Do not enable the flag merely to preview the frontend. The subscription screen already shows the complete
plan catalog and invoice flow while real transactions are disabled.
