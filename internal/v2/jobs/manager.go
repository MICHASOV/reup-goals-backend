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
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

type Handler func(context.Context, Job) error

type Job struct {
	ID          int64
	WorkspaceID *int
	Type        string
	DedupeKey   string
	Payload     json.RawMessage
	Priority    int
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
	namespace    string
	pollInterval time.Duration
	staleAfter   time.Duration

	mu         sync.RWMutex
	handlers   map[string]Handler
	timeouts   map[string]time.Duration
	jobCancels map[int64]context.CancelFunc
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

func NewManager(dbx *sql.DB) *Manager {
	return NewManagerWithNamespace(dbx, "default")
}

func NewManagerWithNamespace(dbx *sql.DB, namespace string) *Manager {
	hostname, _ := os.Hostname()
	namespace = normalizeNamespace(namespace)
	return &Manager{
		dbx:          dbx,
		workerID:     fmt.Sprintf("%s:%s-%d", namespace, hostname, os.Getpid()),
		namespace:    namespace,
		pollInterval: time.Second,
		staleAfter:   2 * time.Minute,
		handlers:     make(map[string]Handler),
		timeouts:     make(map[string]time.Duration),
		jobCancels:   make(map[int64]context.CancelFunc),
	}
}

func normalizeNamespace(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "default"
	}
	var builder strings.Builder
	for _, current := range value {
		if (current >= 'a' && current <= 'z') ||
			(current >= '0' && current <= '9') ||
			current == '-' || current == '_' {
			builder.WriteRune(current)
		}
	}
	if builder.Len() == 0 {
		return "default"
	}
	return builder.String()
}

func (m *Manager) Register(jobType string, handler Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[jobType] = handler
	m.timeouts[jobType] = 10 * time.Minute
}

// RegisterWithoutTimeout is reserved for providers that own their request
// lifecycle and can legitimately take longer than a local HTTP deadline.
func (m *Manager) RegisterWithoutTimeout(jobType string, handler Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[jobType] = handler
	m.timeouts[jobType] = 0
}

func (m *Manager) Namespace() string {
	if m == nil {
		return "default"
	}
	return m.namespace
}

func (m *Manager) Enqueue(ctx context.Context, workspaceID int, jobType string, dedupeKey string, payload any, maxAttempts int, notBefore time.Time) (int64, error) {
	return m.EnqueuePriority(ctx, workspaceID, jobType, dedupeKey, payload, maxAttempts, notBefore, 0)
}

func (m *Manager) EnqueuePriority(
	ctx context.Context,
	workspaceID int,
	jobType string,
	dedupeKey string,
	payload any,
	maxAttempts int,
	notBefore time.Time,
	priority int,
) (int64, error) {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if notBefore.IsZero() {
		notBefore = time.Now().UTC()
	}
	if priority < 0 {
		priority = 0
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("job payload: %w", err)
	}

	var id int64
	err = m.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_background_jobs (
			queue_name, workspace_id, job_type, dedupe_key, payload_json, status,
			priority, attempts, max_attempts, not_before, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8, $9, NOW())
		ON CONFLICT (queue_name, job_type, workspace_id, dedupe_key)
			WHERE dedupe_key <> '' AND status='queued'
		DO UPDATE SET
			payload_json=EXCLUDED.payload_json,
			priority=GREATEST(v2_background_jobs.priority, EXCLUDED.priority),
			max_attempts=GREATEST(v2_background_jobs.max_attempts, EXCLUDED.max_attempts),
			not_before=LEAST(v2_background_jobs.not_before, EXCLUDED.not_before),
			updated_at=NOW()
		RETURNING id
		`, m.namespace, workspaceID, jobType, dedupeKey, raw, StatusQueued, priority, maxAttempts, notBefore).Scan(&id)
	return id, err
}

// EnqueueDebounced coalesces bursts and waits for a short quiet period before
// running. The 15-minute cap prevents a continuously active workspace from
// postponing a required refresh forever.
func (m *Manager) EnqueueDebounced(ctx context.Context, workspaceID int, jobType string, dedupeKey string, payload any, maxAttempts int, notBefore time.Time) (int64, error) {
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
			queue_name, workspace_id, job_type, dedupe_key, payload_json, status,
			priority, attempts, max_attempts, not_before, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 0, 0, $7, $8, NOW())
		ON CONFLICT (queue_name, job_type, workspace_id, dedupe_key)
			WHERE dedupe_key <> '' AND status='queued'
		DO UPDATE SET
			payload_json=EXCLUDED.payload_json,
			max_attempts=GREATEST(v2_background_jobs.max_attempts, EXCLUDED.max_attempts),
			not_before=LEAST(
				GREATEST(v2_background_jobs.not_before, EXCLUDED.not_before),
				v2_background_jobs.created_at + INTERVAL '15 minutes'
			),
			updated_at=NOW()
		RETURNING id
		`, m.namespace, workspaceID, jobType, dedupeKey, raw, StatusQueued, maxAttempts, notBefore).Scan(&id)
	return id, err
}

func (m *Manager) Start(parent context.Context, workers int) {
	m.StartPartitioned(parent, workers, 0, 0)
}

// StartPartitioned reserves a worker pool for latency-sensitive jobs. Standard
// jobs cannot consume those slots, and interactive jobs cannot be trapped
// behind long-running maintenance work.
func (m *Manager) StartPartitioned(parent context.Context, workers int, priorityWorkers int, priorityThreshold int) {
	if workers <= 0 {
		workers = 2
	}
	if priorityWorkers < 0 {
		priorityWorkers = 0
	}
	if priorityWorkers > 0 && priorityThreshold <= 0 {
		priorityThreshold = 1
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	if err := m.recoverStale(ctx); err != nil {
		log.Printf("[WARN] background job recovery failed: %v", err)
	}
	for index := 0; index < workers; index++ {
		m.wg.Add(1)
		if priorityWorkers > 0 {
			go m.worker(ctx, 0, priorityThreshold)
		} else {
			go m.worker(ctx, 0, 0)
		}
	}
	for index := 0; index < priorityWorkers; index++ {
		m.wg.Add(1)
		go m.worker(ctx, priorityThreshold, 0)
	}
}

func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

func (m *Manager) worker(ctx context.Context, minimumPriority int, maximumPriority int) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	staleTicker := time.NewTicker(time.Minute)
	defer staleTicker.Stop()

	for {
		worked, err := m.runOne(ctx, minimumPriority, maximumPriority)
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

func (m *Manager) runOne(ctx context.Context, minimumPriority int, maximumPriority int) (bool, error) {
	jobTypes := m.registeredJobTypes()
	if len(jobTypes) == 0 {
		return false, nil
	}
	job, err := m.claim(ctx, minimumPriority, maximumPriority, jobTypes)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	m.mu.RLock()
	handler := m.handlers[job.Type]
	timeout := m.timeouts[job.Type]
	m.mu.RUnlock()
	if handler == nil {
		return true, m.fail(ctx, job, fmt.Errorf("no handler registered for %s", job.Type))
	}

	jobCtx, cancelJob := context.WithCancel(ctx)
	cancel := cancelJob
	if timeout > 0 {
		var timeoutCancel context.CancelFunc
		jobCtx, timeoutCancel = context.WithTimeout(jobCtx, timeout)
		cancel = func() {
			timeoutCancel()
			cancelJob()
		}
	}
	m.mu.Lock()
	m.jobCancels[job.ID] = cancelJob
	m.mu.Unlock()
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		m.heartbeat(heartbeatCtx, job.ID)
	}()
	err = invokeHandler(jobCtx, handler, job)
	cancel()
	m.mu.Lock()
	delete(m.jobCancels, job.ID)
	m.mu.Unlock()
	stopHeartbeat()
	<-heartbeatDone
	finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finalizeCancel()
	if err != nil {
		return true, m.fail(finalizeCtx, job, err)
	}
	_, err = m.dbx.ExecContext(finalizeCtx, `
			UPDATE v2_background_jobs
			SET status=$2, completed_at=NOW(), locked_at=NULL, locked_by='', last_error='', updated_at=NOW()
			WHERE id=$1 AND status=$3 AND locked_by=$4 AND queue_name=$5
		`, job.ID, StatusCompleted, StatusRunning, m.workerID, m.namespace)
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

func (m *Manager) registeredJobTypes() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	jobTypes := make([]string, 0, len(m.handlers))
	for jobType := range m.handlers {
		jobTypes = append(jobTypes, jobType)
	}
	return jobTypes
}

func (m *Manager) claim(
	ctx context.Context,
	minimumPriority int,
	maximumPriority int,
	jobTypes []string,
) (Job, error) {
	tx, err := m.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	var job Job
	var workspaceID sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, job_type, dedupe_key, payload_json, priority, attempts, max_attempts
		FROM v2_background_jobs
			WHERE queue_name=$1
				AND status=$2
				AND not_before <= NOW()
				AND priority >= $3
				AND ($4 <= 0 OR priority < $4)
				AND job_type = ANY($5)
		ORDER BY priority DESC, not_before, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
		`, m.namespace, StatusQueued, minimumPriority, maximumPriority, pq.Array(jobTypes)).Scan(
		&job.ID, &workspaceID, &job.Type, &job.DedupeKey, &job.Payload,
		&job.Priority, &job.Attempts, &job.MaxAttempts,
	)
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
			WHERE id=$1 AND queue_name=$5
		`, job.ID, StatusRunning, job.Attempts, m.workerID, m.namespace); err != nil {
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
		WHERE id=$1 AND queue_name=$5 AND status=$6 AND locked_by=$7
	`, job.ID, status, notBefore, truncateError(jobErr), m.namespace, StatusRunning, m.workerID)
	if status == StatusFailed {
		log.Printf("[ERROR] background job failed permanently id=%d type=%s attempts=%d: %v", job.ID, job.Type, job.Attempts, jobErr)
	}
	return err
}

// CancelByDedupeKey prevents queued retries and interrupts a matching job that
// is currently running in this process. A worker finishing after cancellation
// cannot overwrite the canceled database state.
func (m *Manager) CancelByDedupeKey(
	ctx context.Context,
	workspaceID int,
	dedupeKey string,
	jobTypes ...string,
) error {
	if strings.TrimSpace(dedupeKey) == "" || len(jobTypes) == 0 {
		return nil
	}
	rows, err := m.dbx.QueryContext(ctx, `
		UPDATE v2_background_jobs
		SET status=$1, locked_at=NULL, locked_by='', last_error='Canceled by user.', updated_at=NOW()
		WHERE queue_name=$2 AND workspace_id=$3 AND dedupe_key=$4
			AND job_type=ANY($5) AND status IN ($6, $7)
		RETURNING id
	`, StatusCanceled, m.namespace, workspaceID, dedupeKey, pq.Array(jobTypes), StatusQueued, StatusRunning)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	cancels := make([]context.CancelFunc, 0, len(ids))
	for _, id := range ids {
		if cancel := m.jobCancels[id]; cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	m.mu.RUnlock()
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

func retryDelay(attempt int) time.Duration {
	seconds := math.Min(300, math.Pow(2, float64(attempt))*5)
	return time.Duration(seconds) * time.Second
}

func (m *Manager) heartbeat(ctx context.Context, jobID int64) {
	interval := m.staleAfter / 4
	if interval <= 0 || interval > time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.dbx.ExecContext(ctx, `
				UPDATE v2_background_jobs
				SET locked_at=NOW(), updated_at=NOW()
				WHERE id=$1 AND status=$2 AND locked_by=$3 AND queue_name=$4
			`, jobID, StatusRunning, m.workerID, m.namespace); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("[WARN] background job heartbeat failed id=%d: %v", jobID, err)
			}
		}
	}
}

func (m *Manager) recoverStale(ctx context.Context) error {
	_, err := m.dbx.ExecContext(ctx, `
		UPDATE v2_background_jobs
		SET status=$1, not_before=NOW(), locked_at=NULL, locked_by='',
			last_error=CASE WHEN last_error='' THEN 'Recovered after an interrupted worker.' ELSE last_error END,
			updated_at=NOW()
		WHERE status=$2
			AND queue_name=$4
			AND (locked_at IS NULL OR locked_at < NOW() - ($3 * INTERVAL '1 second'))
	`, StatusQueued, StatusRunning, int(m.staleAfter.Seconds()), m.namespace)
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
		WHERE workspace_id=$1 AND queue_name=$2
	`, workspaceID, m.namespace).Scan(&stats.Queued, &stats.Running, &stats.Failed)
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
