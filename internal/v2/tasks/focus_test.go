package tasks

import "testing"

func TestFocusScore(t *testing.T) {
	tests := []struct {
		name    string
		aligned int
		total   int
		want    *int
	}{
		{name: "no history", aligned: 0, total: 0, want: nil},
		{name: "all aligned", aligned: 4, total: 4, want: intPointer(100)},
		{name: "three of four", aligned: 3, total: 4, want: intPointer(75)},
		{name: "rounded", aligned: 2, total: 3, want: intPointer(67)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := focusScore(test.aligned, test.total)
			if test.want == nil {
				if got != nil {
					t.Fatalf("focusScore() = %d, want nil", *got)
				}
				return
			}
			if got == nil || *got != *test.want {
				t.Fatalf("focusScore() = %v, want %d", got, *test.want)
			}
		})
	}
}

func intPointer(value int) *int {
	return &value
}
