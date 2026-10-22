package workerruntime

import (
	"errors"
	"testing"
	"time"
)

func TestValidateTaskLeaseUsesProvidedDatabaseClock(t *testing.T) {
	expires := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	task := Task{LeaseToken: "lease-1", LeaseExpiresAt: &expires}

	if err := validateTaskLeaseAt(task, "lease-1", expires.Add(-time.Hour)); err != nil {
		t.Fatalf("lease should be valid according to the database clock: %v", err)
	}
	if err := validateTaskLeaseAt(task, "lease-1", expires); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("lease must be expired at its exact expiry instant: %v", err)
	}
	if err := validateTaskLeaseAt(task, "lease-1", expires.Add(time.Nanosecond)); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("lease should be expired according to the database clock, got %v", err)
	}
	if err := validateTaskLeaseAt(task, "other-token", expires.Add(-time.Hour)); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("token mismatch must win independently of clock skew, got %v", err)
	}
}
