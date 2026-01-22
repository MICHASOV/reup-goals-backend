package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type CtxKey string

const (
	ctxUserIDKey CtxKey = "analytics_user_id"
)

// Envelope is what we store with every event.
type Envelope struct {
	UserID       int
	SessionID    string
	Platform     string
	AppVersion   string
	DeviceLocale string
	IPCountry    string
}

// FromRequest extracts event envelope fields from request.
// Backend-trustable fields only.
func FromRequest(r *http.Request) Envelope {
	platform := strings.TrimSpace(r.Header.Get("X-Platform"))
	if platform == "" {
		platform = "unknown"
	} else {
		platform = strings.ToLower(platform)
		if platform != "ios" && platform != "android" && platform != "web" {
			platform = "unknown"
		}
	}

	appVer := strings.TrimSpace(r.Header.Get("X-App-Version"))
	locale := strings.TrimSpace(r.Header.Get("Accept-Language"))
	if locale == "" {
		locale = strings.TrimSpace(r.Header.Get("X-Device-Locale"))
	}

	sessionID := strings.TrimSpace(r.Header.Get("X-Session-Id"))

	// ip_country: you don't have geoip now -> keep empty
	return Envelope{
		SessionID:    sessionID,
		Platform:     platform,
		AppVersion:   appVer,
		DeviceLocale: locale,
		IPCountry:    "",
	}
}

func WithUserID(ctx context.Context, userID int) context.Context {
	return context.WithValue(ctx, ctxUserIDKey, userID)
}

func UserIDFromContext(ctx context.Context) (int, bool) {
	v := ctx.Value(ctxUserIDKey)
	if v == nil {
		return 0, false
	}
	uid, ok := v.(int)
	return uid, ok
}

// Client-provided idempotency key (optional)
// If present and duplicates, insert is ignored.
func SourceEventKeyFromRequest(r *http.Request) string {
	// preferred: Idempotency-Key header
	k := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if k != "" {
		return k
	}
	// fallback
	return strings.TrimSpace(r.Header.Get("X-Source-Event-Key"))
}

// Log inserts one analytics event.
// Never logs sensitive raw text; caller passes sanitized props.
func Log(ctx context.Context, db *sql.DB, env Envelope, eventName string, props any, sourceEventKey string) error {
	if eventName == "" {
		return nil
	}

	var userID int
	if env.UserID != 0 {
		userID = env.UserID
	} else if uid, ok := UserIDFromContext(ctx); ok {
		userID = uid
	} else {
		// no user => skip (or you can allow user_id=0 and store)
		return nil
	}

	b, err := json.Marshal(props)
	if err != nil {
		// if props can't marshal, don't break core flow
		return nil
	}

	// If source_event_key duplicates -> do nothing
	if sourceEventKey != "" {
		_, _ = db.ExecContext(ctx, `
			INSERT INTO analytics_events (
				event_name, event_time,
				user_id, session_id,
				platform, app_version, device_locale, ip_country,
				source_event_key,
				properties
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
			ON CONFLICT (source_event_key) DO NOTHING
		`, eventName, time.Now().UTC(),
			userID, nullIfEmpty(env.SessionID),
			env.Platform, env.AppVersion, nullIfEmpty(env.DeviceLocale), nullIfEmpty(env.IPCountry),
			sourceEventKey,
			string(b),
		)
		return nil
	}

	_, _ = db.ExecContext(ctx, `
		INSERT INTO analytics_events (
			event_name, event_time,
			user_id, session_id,
			platform, app_version, device_locale, ip_country,
			properties
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
	`, eventName, time.Now().UTC(),
		userID, nullIfEmpty(env.SessionID),
		env.Platform, env.AppVersion, nullIfEmpty(env.DeviceLocale), nullIfEmpty(env.IPCountry),
		string(b),
	)

	return nil
}

func nullIfEmpty(s string) sql.NullString {
	if strings.TrimSpace(s) == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// Priority tier helper (simple deterministic bucket)
func TierFromScore(score int) string {
	switch {
	case score >= 700:
		return "P1"
	case score >= 400:
		return "P2"
	default:
		return "P3"
	}
}
