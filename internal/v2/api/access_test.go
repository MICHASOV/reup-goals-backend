package api

import (
	"database/sql"
	"testing"
	"time"
)

func TestSubscriptionGrantsProductAccess(t *testing.T) {
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	future := sql.NullTime{Time: now.Add(time.Hour), Valid: true}
	past := sql.NullTime{Time: now.Add(-time.Hour), Valid: true}
	empty := sql.NullTime{}

	cases := []struct {
		name       string
		status     string
		periodEnd  sql.NullTime
		graceUntil sql.NullTime
		want       bool
	}{
		{name: "active", status: "active", want: true},
		{name: "trial", status: "trial_active", want: true},
		{name: "cancelled within period", status: "cancelled", periodEnd: future, want: true},
		{name: "cancelled after period", status: "cancelled", periodEnd: past, want: false},
		{name: "past due within grace", status: "past_due", graceUntil: future, want: true},
		{name: "past due after grace", status: "past_due", graceUntil: past, want: false},
		{name: "inactive", status: "inactive", periodEnd: empty, graceUntil: empty, want: false},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := subscriptionGrantsProductAccess(test.status, test.periodEnd, test.graceUntil, now); got != test.want {
				t.Fatalf("access = %v, want %v", got, test.want)
			}
		})
	}
}
