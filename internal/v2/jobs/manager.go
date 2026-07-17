package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"sync"
	"time"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

type Handler func(context.Context, Job) error

type Job struct {
	ID          int64
	WorkspaceID *int
	Type        string
	DedupeKey   string
	Payload     json.RawMessage
	Attempts    int
	MaxAttempts int
}

type QueueStats struct {
	Queued  int `json:"queued"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
}

type Manager struct {
	dbx          *sql.DB
	workerID     string
	pollInterval time.Duration
	staleAfter   time.Duration

	mu       sync.RWMutex
	handlers map[string]Handler
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewManager(dbx *sql.DB) *Manager {
	hostname, _ := os.Hostname()
	return &Manager{
		dbx:          dbx,
		workerID:     fmt.Sprintf("%s-%d", hostname, os.Getpid()),
		pollInterval: time.Second,
		staleAfter:   10 * time.Minute,
		handlers:     make(map[string]Handler),
	}
}

func (m *Manager) Register(jobType string, handler Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[jobType] = handler
}

func (m *Manager) Enqueue(ctx context.Context, workspaceID int, jobType string, dedupeKey string, payload any, maxAttempts int, notBefore time.Time) (int64, error) {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if notBefore.IsZero() {
		notBefore = time.Now().UTC()
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("job payload: %w", err)
	}

	var id int64
	err = m.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_background_jobs (
			workspace_id, job_type, dedupe_key, payload_json, status,
			attempts, max_attempts, not_before, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, 0, $6, $7, NOW())
		ON CONFLICT (job_type, workspace_id, dedupe_key)
			WHERE dedupe_key <> '' AND status='queued'
		DO UPDATE SET
			payload_json=EXCLUDED.payload_json,
			max_attempts=GREATEST(v2_background_jobs.max_attempts, EXCLUDED.max_attempts),
			not_before=LEAST(v2_background_jobs.not_before, EXCLUDED.not_before),
			updated_at=NOW()
		RETURNING id
	`, workspaceID, jobType, dedupeKey, raw, StatusQueued, maxAttempts, notBefore).Scan(&id)
	return id, err
}

func (m *Manager) Start(parent context.Context, workers int) {
	if workers <= 0 {
		workers = 2
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	if err := m.recoverStale(ctx); err != nil {
		log.Printf("[WARN] background job recovery failed: %v", err)
	}
	for index := 0; index < workers; index++ {
		m.wg.Add(1)
		go m.worker(ctx)
	}
}

func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

func (m *Manager) worker(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	staleTicker := time.NewTicker(time.Minute)
	defer staleTicker.Stop()

	for {
		worked, err := m.runOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[WARN] background job worker failed: %v", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-staleTicker.C:
			if err := m.recoverStale(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("[WARN] background job recovery failed: %v", err)
			}
		case <-ticker.C:
		}
	}
}

func (m *Manager) runOne(ctx context.Context) (bool, error) {
	job, err := m.claim(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	m.mu.RLock()
	handler := m.handlers[job.Type]
	m.mu.RUnlock()
	if handler == nil {
		return true, m.fail(ctx, job, fmt.Errorf("no handler registered for %s", job.Type))
	}

	jobCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	err = invokeHandler(jobCtx, handler, job)
	cancel()
	if err != nil {
		return true, m.fail(ctx, job, err)
	}
	_, err = m.dbx.ExecContext(ctx, `
		UPDATE v2_background_jobs
		SET status=$2, completed_at=NOW(), locked_at=NULL, locked_by='', last_error='', updated_at=NOW()
		WHERE id=$1 AND status=$3
	`, job.ID, StatusCompleted, StatusRunning)
	return true, err
}

func invokeHandler(ctx context.Context, handler Handler, job Job) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job handler panic: %v", recovered)
		}
	}()
	return handler(ctx, job)
}

func (m *Manager) claim(ctx context.Context) (Job, error) {
	tx, err := m.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	var job Job
	var workspaceID sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, job_type, dedupe_key, payload_json, attempts, max_attempts
		FROM v2_background_jobs
		WHERE status=$1 AND not_before <= NOW()
		ORDER BY not_before, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, StatusQueued).Scan(&job.ID, &workspaceID, &job.Type, &job.DedupeKey, &job.Payload, &job.Attempts, &job.MaxAttempts)
	if err != nil {
		return Job{}, err
	}
	if workspaceID.Valid {
		value := int(workspaceID.Int64)
		job.WorkspaceID = &value
	}
	job.Attempts++
	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_background_jobs
		SET status=$2, attempts=$3, locked_at=NOW(), locked_by=$4, updated_at=NOW()
		WHERE id=$1
	`, job.ID, StatusRunning, job.Attempts, m.workerID); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (m *Manager) fail(ctx context.Context, job Job, jobErr error) error {
	status := StatusFailed
	notBefore := time.Now().UTC()
	if job.Attempts < job.MaxAttempts {
		status = StatusQueued
		notBefore = notBefore.Add(retryDelay(job.Attempts))
	}
	_, err := m.dbx.ExecContext(ctx, `
		UPDATE v2_background_jobs
		SET status=$2, not_before=$3, locked_at=NULL, locked_by='', last_error=$4, updated_at=NOW()
		WHERE id=$1
	`, job.ID, status, notBefore, truncateError(jobErr))
	if status == StatusFailed {
		log.Printf("[ERROR] background job failed permanently id=%d type=%s attempts=%d: %v", job.ID, job.Type, job.Attempts, jobErr)
	}
	return err
}

func retryDelay(attempt int) time.Duration {
	seconds := math.Min(300, math.Pow(2, float64(attempt))*5)
	return time.Duration(seconds) * time.Second
}

func (m *Manager) recoverStale(ctx context.Context) error {
	_, err := m.dbx.ExecContext(ctx, `
		UPDATE v2_background_jobs
		SET status=$1, not_before=NOW(), locked_at=NULL, locked_by='',
			last_error=CASE WHEN last_error='' THEN 'Recovered after an interrupted worker.' ELSE last_error END,
			updated_at=NOW()
		WHERE status=$2 AND locked_at < NOW() - ($3 * INTERVAL '1 second')
	`, StatusQueued, StatusRunning, int(m.staleAfter.Seconds()))
	return err
}

func (m *Manager) Stats(ctx context.Context, workspaceID int) (QueueStats, error) {
	var stats QueueStats
	err := m.dbx.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status='queued'),
			COUNT(*) FILTER (WHERE status='running'),
			COUNT(*) FILTER (WHERE status='failed' AND updated_at > NOW() - INTERVAL '24 hours')
		FROM v2_background_jobs
		WHERE workspace_id=$1
	`, workspaceID).Scan(&stats.Queued, &stats.Running, &stats.Failed)
	return stats, err
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 4000 {
		return value[:4000]
	}
	return value
}
