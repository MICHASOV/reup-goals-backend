package strategicmemory

import (
	"testing"
	"time"
)

func TestReserveAutoQualityAuditThrottlesRepeatedRuns(t *testing.T) {
	service := &Service{qualityAuditReservedAt: map[int]time.Time{}}

	if !service.reserveAutoQualityAudit(42) {
		t.Fatal("first automatic quality audit should be reserved")
	}
	if service.reserveAutoQualityAudit(42) {
		t.Fatal("repeated automatic quality audit should be throttled")
	}
	if !service.reserveAutoQualityAudit(43) {
		t.Fatal("a different workspace should have an independent reservation")
	}
}

func TestCompletedManualQualityAuditThrottlesAutomaticRun(t *testing.T) {
	service := &Service{qualityAuditReservedAt: map[int]time.Time{}}

	service.markQualityAuditCompleted(42)
	if service.reserveAutoQualityAudit(42) {
		t.Fatal("automatic audit should be throttled after a completed manual audit")
	}
}
