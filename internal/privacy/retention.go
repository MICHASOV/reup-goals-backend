package privacy

import (
	"context"
	"database/sql"
	"log"
	"time"
)

const retentionAdvisoryLock int64 = 528105221

type RetentionPolicy struct {
	Interval        time.Duration
	AuthCodes       time.Duration
	HTTPRequestLogs time.Duration
	ProductEvents   time.Duration
	AICallLogs      time.Duration
	BackgroundJobs  time.Duration
	LegalEvidence   time.Duration
	PrivacyRequests time.Duration
}

type RetentionRunner struct {
	dbx    *sql.DB
	policy RetentionPolicy
}

func NewRetentionRunner(dbx *sql.DB, policy RetentionPolicy) *RetentionRunner {
	if policy.Interval <= 0 {
		policy.Interval = 24 * time.Hour
	}
	return &RetentionRunner{dbx: dbx, policy: policy}
}

func (r *RetentionRunner) Start(ctx context.Context) {
	go func() {
		r.run(ctx)
		ticker := time.NewTicker(r.policy.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.run(ctx)
			}
		}
	}()
}

func (r *RetentionRunner) run(ctx context.Context) {
	tx, err := r.dbx.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("[WARN] retention transaction failed: %v", err)
		return
	}
	defer tx.Rollback()
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, retentionAdvisoryLock).Scan(&locked); err != nil || !locked {
		return
	}
	statements := []struct {
		name      string
		query     string
		retention time.Duration
	}{
		{"auth codes", `DELETE FROM auth_email_codes WHERE created_at < $1 AND (used_at IS NOT NULL OR expires_at < NOW())`, r.policy.AuthCodes},
		{"HTTP request logs", `DELETE FROM v2_http_request_logs WHERE created_at < $1`, r.policy.HTTPRequestLogs},
		{"product events", `DELETE FROM v2_product_events WHERE created_at < $1`, r.policy.ProductEvents},
		{"AI call logs", `DELETE FROM v2_ai_call_logs WHERE created_at < $1`, r.policy.AICallLogs},
		{"background jobs", `DELETE FROM v2_background_jobs WHERE updated_at < $1 AND status IN ('completed', 'failed')`, r.policy.BackgroundJobs},
		{"legal evidence", `
			DELETE FROM legal_acceptances evidence
			WHERE evidence.recorded_at < $1
				AND evidence.id NOT IN (
					SELECT MAX(latest.id) FROM legal_acceptances latest
					GROUP BY latest.subject_key, latest.document_type
				)`, r.policy.LegalEvidence},
		{"privacy requests", `DELETE FROM privacy_requests WHERE updated_at < $1 AND status IN ('completed', 'rejected', 'cancelled')`, r.policy.PrivacyRequests},
	}
	now := time.Now().UTC()
	for _, statement := range statements {
		if statement.retention <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, statement.query, now.Add(-statement.retention)); err != nil {
			log.Printf("[WARN] retention cleanup failed for %s: %v", statement.name, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("[WARN] retention commit failed: %v", err)
	}
}
