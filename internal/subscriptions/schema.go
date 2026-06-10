package subscriptions

import "database/sql"

func EnsureSchema(dbx *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id SERIAL PRIMARY KEY,
			user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
			cloudpayments_subscription_id TEXT NULL,
			cloudpayments_token TEXT NULL,
			status TEXT NOT NULL DEFAULT 'inactive',
			plan_name TEXT NOT NULL DEFAULT 'REUP.goals Pro',
			amount NUMERIC(12,2) NOT NULL DEFAULT 199,
			currency TEXT NOT NULL DEFAULT 'RUB',
			trial_started_at TIMESTAMPTZ NULL,
			trial_ends_at TIMESTAMPTZ NULL,
			current_period_start TIMESTAMPTZ NULL,
			current_period_end TIMESTAMPTZ NULL,
			next_payment_at TIMESTAMPTZ NULL,
			grace_until TIMESTAMPTZ NULL,
			cancelled_at TIMESTAMPTZ NULL,
			last_payment_at TIMESTAMPTZ NULL,
			last_failed_at TIMESTAMPTZ NULL,
			failed_attempts INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_cloudpayments_subscription_id
			ON subscriptions (cloudpayments_subscription_id)`,
		`CREATE TABLE IF NOT EXISTS payment_events (
			id SERIAL PRIMARY KEY,
			event_type TEXT NOT NULL,
			user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
			subscription_id INTEGER NULL REFERENCES subscriptions(id) ON DELETE SET NULL,
			cloudpayments_transaction_id TEXT NULL,
			cloudpayments_subscription_id TEXT NULL,
			account_id TEXT NULL,
			amount NUMERIC(12,2) NULL,
			currency TEXT NULL,
			payload JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_payment_events_created_at
			ON payment_events (created_at DESC)`,
	}

	for _, statement := range statements {
		if _, err := dbx.Exec(statement); err != nil {
			return err
		}
	}

	return nil
}
