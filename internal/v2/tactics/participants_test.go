package tactics

import (
	"reflect"
	"testing"
)

func TestNormalizedParticipantIDs(t *testing.T) {
	got := normalizedParticipantIDs([]int{7, 3, 7, 0, -2, 4, 3})
	want := []int{3, 4, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizedParticipantIDs() = %v, want %v", got, want)
	}
}

func TestWorkstreamParticipantsPath(t *testing.T) {
	tests := []struct {
		path string
		id   int
		ok   bool
	}{
		{path: "/api/v2/tactics/workstreams/9/participants", id: 9, ok: true},
		{path: "/api/v2/tactics/workstreams/9/participants/", id: 9, ok: true},
		{path: "/api/v2/tactics/workstreams/9", ok: false},
		{path: "/api/v2/tactics/workstreams/nope/participants", ok: false},
	}
	for _, test := range tests {
		id, ok := workstreamParticipantsPath(test.path)
		if id != test.id || ok != test.ok {
			t.Fatalf("workstreamParticipantsPath(%q) = (%d, %v), want (%d, %v)", test.path, id, ok, test.id, test.ok)
		}
	}
}
