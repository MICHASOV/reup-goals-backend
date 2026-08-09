package billing

import "testing"

func TestBillingPeriodMonths(t *testing.T) {
	tests := []struct {
		period string
		want   int
	}{
		{period: PeriodMonthly, want: 1},
		{period: PeriodQuarterly, want: 3},
		{period: PeriodAnnual, want: 12},
		{period: "", want: 1},
	}

	for _, test := range tests {
		if got := billingPeriodMonths(test.period); got != test.want {
			t.Fatalf("billingPeriodMonths(%q) = %d, want %d", test.period, got, test.want)
		}
	}
}

func TestReplacementConfirmationRequiresDistinctSubscriptions(t *testing.T) {
	tests := []struct {
		name       string
		previousID string
		currentID  string
		status     string
		want       string
	}{
		{name: "replacement", previousID: "old", currentID: "new", status: "pending", want: "old"},
		{name: "already cancelled", previousID: "old", currentID: "new", status: "cancelled"},
		{name: "same subscription", previousID: "same", currentID: "same", status: "pending"},
		{name: "missing new subscription", previousID: "old", status: "pending"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := replacementConfirmation(test.previousID, test.currentID, test.status)
			if got.SubscriptionIDToCancel != test.want {
				t.Fatalf("SubscriptionIDToCancel = %q, want %q", got.SubscriptionIDToCancel, test.want)
			}
		})
	}
}
