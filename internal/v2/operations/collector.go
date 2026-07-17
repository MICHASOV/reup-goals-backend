package operations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"reup-goals-backend/internal/auth"
)

type contextKey string

const requestIDKey contextKey = "request_id"

type requestRecord struct {
	RequestID     string
	UserID        *int
	Method        string
	Path          string
	StatusCode    int
	LatencyMS     int64
	ResponseBytes int64
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(body)
	w.bytes += int64(written)
	return written, err
}

func (w *responseRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type Collector struct {
	dbx    *sql.DB
	secret []byte
	queue  chan requestRecord
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewCollector(dbx *sql.DB, secret []byte) *Collector {
	return &Collector{dbx: dbx, secret: secret, queue: make(chan requestRecord, 1024)}
}

func (c *Collector) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	c.wg.Add(1)
	go c.run(ctx)
}

func (c *Collector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
}

func (c *Collector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v2/") {
			next.ServeHTTP(w, r)
			return
		}

		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = newRequestID()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		recorder := &responseRecorder{ResponseWriter: w}
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Add("Trailer", "Server-Timing")
		started := time.Now()
		next.ServeHTTP(recorder, r.WithContext(ctx))
		latency := time.Since(started).Milliseconds()
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		w.Header().Set("Server-Timing", "app;dur="+strconv.FormatInt(latency, 10))

		record := requestRecord{
			RequestID: requestID, Method: r.Method, Path: normalizedPath(r.URL.Path),
			StatusCode: recorder.status, LatencyMS: latency, ResponseBytes: recorder.bytes,
		}
		if userID, ok := requestUserID(c.secret, r); ok && recorder.status != http.StatusUnauthorized {
			record.UserID = &userID
		}
		select {
		case c.queue <- record:
		default:
			log.Printf("[WARN] operations queue full, dropping request metric path=%s", record.Path)
		}
	})
}

func (c *Collector) run(ctx context.Context) {
	defer c.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case record := <-c.queue:
			if err := c.persist(ctx, record); err != nil && ctx.Err() == nil {
				log.Printf("[WARN] request metric write failed: %v", err)
			}
		}
	}
}

func (c *Collector) persist(ctx context.Context, record requestRecord) error {
	_, err := c.dbx.ExecContext(ctx, `
		INSERT INTO v2_http_request_logs (
			request_id, workspace_id, user_id, method, path, status_code, latency_ms, response_bytes
		)
		SELECT $1, membership.workspace_id, $2, $3, $4, $5, $6, $7
		FROM (SELECT 1) seed
		LEFT JOIN LATERAL (
			SELECT workspace_id FROM workspace_memberships
			WHERE user_id=$2 AND status='active'
			ORDER BY is_default DESC, created_at
			LIMIT 1
		) membership ON TRUE
	`, record.RequestID, record.UserID, record.Method, record.Path, record.StatusCode, record.LatencyMS, record.ResponseBytes)
	if err != nil {
		return err
	}
	if record.Method != http.MethodGet && record.Method != http.MethodHead && record.Method != http.MethodOptions && record.StatusCode < 400 {
		_, err = c.dbx.ExecContext(ctx, `
			INSERT INTO v2_product_events (workspace_id, user_id, event_name, source, properties_json)
			SELECT membership.workspace_id, $1, $2, 'api', jsonb_build_object('method', $3, 'path', $4, 'status', $5)
			FROM (SELECT 1) seed
			LEFT JOIN LATERAL (
				SELECT workspace_id FROM workspace_memberships
				WHERE user_id=$1 AND status='active'
				ORDER BY is_default DESC, created_at
				LIMIT 1
			) membership ON TRUE
		`, record.UserID, eventName(record.Method, record.Path), record.Method, record.Path, record.StatusCode)
	}
	return err
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func newRequestID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(raw)
}

func requestUserID(secret []byte, r *http.Request) (int, bool) {
	token, ok := auth.TokenFromRequest(r)
	if !ok {
		return 0, false
	}
	userID, err := auth.ParseToken(secret, token)
	return userID, err == nil
}

func normalizedPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for index, part := range parts {
		if _, err := strconv.Atoi(part); err == nil {
			parts[index] = ":id"
		}
	}
	return "/" + strings.Join(parts, "/")
}

func eventName(method string, path string) string {
	name := strings.Trim(strings.ReplaceAll(normalizedPath(path), "/", "."), ".")
	return strings.ToLower(method) + "." + name
}
