package calibration

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
)

func TestCalibrationRetakeCooldownAndNoReferenceDisclosure(t *testing.T) {
	service, _, store := testService(t, 1)
	session, err := service.CreateSession(t.Context(), "tenant-a", "exam-1", "question-1", "grader")
	if err != nil {
		t.Fatal(err)
	}
	attempt, completed, _, err := service.SubmitAttempt(t.Context(), "tenant-a", session.ID, SubmitAttemptInput{GoldPaperID: session.Samples[0].GoldPaperID, SubmittedScore: 5})
	if err != nil || completed.Status != SessionFailed {
		t.Fatal(completed, err)
	}
	raw, _ := json.Marshal(attempt)
	for _, field := range []string{"reference_score", "criterion_differences", "absolute_error", "exact_match"} {
		if strings.Contains(string(raw), field) {
			t.Fatal("reference information disclosed", string(raw))
		}
	}
	if _, err = service.CreateSession(t.Context(), "tenant-a", "exam-1", "question-1", "grader"); !errors.Is(err, ErrRetakeCooldown) {
		t.Fatal("immediate retake allowed", err)
	}
	var cooldown *RetakeCooldownError
	if !errors.As(err, &cooldown) || !cooldown.AvailableAt.Equal(completed.CompletedAt.Add(24*time.Hour)) {
		t.Fatalf("retake deadline does not use the failed completion time: %v", err)
	}
	service.now = func() time.Time { return completed.CompletedAt.Add(24 * time.Hour) }
	if _, err = service.CreateSession(t.Context(), "tenant-a", "exam-1", "question-1", "grader"); err != nil {
		t.Fatal("retake did not recover", err)
	}
	if len(store.sessions) != 2 {
		t.Fatal(len(store.sessions))
	}
}

func TestCalibrationExcludesBlindPoolAndDrawsConfiguredSubset(t *testing.T) {
	service, gold, _ := testService(t, 3)
	items, err := gold.ListActiveApproved(t.Context(), "tenant-a", "exam-1", "question-1")
	if err != nil {
		t.Fatal(err)
	}
	items[0].QualityPool = goldpaper.PoolBlind
	reader := &batch41GoldReader{items: items}
	service.gold = reader
	policy, _ := service.store.GetPolicy(t.Context(), "tenant-a", "exam-1", "question-1")
	policy.MinimumSamples = 1
	service.store.(*MemoryStore).mu.Lock()
	service.store.(*MemoryStore).policies[policyKey("tenant-a", "exam-1", "question-1")] = policy
	service.store.(*MemoryStore).mu.Unlock()
	session, err := service.CreateSession(t.Context(), "tenant-a", "exam-1", "question-1", "grader")
	if err != nil || len(session.Samples) != 1 || session.Samples[0].GoldPaperID == items[0].ID {
		t.Fatal(session, err)
	}
}
