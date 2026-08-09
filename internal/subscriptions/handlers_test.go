package subscriptions

import (
	"net/url"
	"testing"
)

func TestCloudPaymentsOrderID(t *testing.T) {
	tests := []struct {
		invoiceID string
		want      int64
		ok        bool
	}{
		{invoiceID: "42", want: 42, ok: true},
		{invoiceID: " 42 ", want: 42, ok: true},
		{invoiceID: "0", ok: false},
		{invoiceID: "invalid", ok: false},
		{invoiceID: "", ok: false},
	}

	for _, test := range tests {
		got, ok := cloudPaymentsOrderID(url.Values{"InvoiceId": {test.invoiceID}})
		if got != test.want || ok != test.ok {
			t.Fatalf("cloudPaymentsOrderID(%q) = (%d, %v), want (%d, %v)", test.invoiceID, got, ok, test.want, test.ok)
		}
	}
}
