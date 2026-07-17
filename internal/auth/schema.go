package auth

import "database/sql"

func EnsureSchema(dbx *sql.DB) error {
	statements := []string{
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS auth_version INTEGER NOT NULL DEFAULT 1`,
		`CREATE TABLE IF NOT EXISTS auth_email_codes (
			id SERIAL PRIMARY KEY,
			email TEXT NOT NULL,
			user_id INTEGER NULL REFERENCES users(id) ON DELETE CASCADE,
			code_hash TEXT NOT NULL,
			code_type TEXT NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			used_at TIMESTAMPTZ NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			reset_token_hash TEXT NULL,
			reset_token_expires_at TIMESTAMPTZ NULL,
			reset_token_used_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_email_codes_lookup
			ON auth_email_codes (lower(email), code_type, used_at, created_at DESC)`,
	}

	for _, statement := range statements {
		if _, err := dbx.Exec(statement); err != nil {
			return err
		}
	}

	return nil
}
