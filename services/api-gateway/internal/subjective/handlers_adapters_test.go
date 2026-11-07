package subjective_test

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSubjectivePermissionDeniedAndUnauthenticated(t *testing.T) {
	store := subjective.NewMemoryStore()
	store.AddContext(tenantID, "segment-1", subjectiveContext("short_answer", 5, "synthetic answer", nil))
	router := testRouter(authStoreWithPermissions(t, []string{"system:read"}), store)
	token := login(t, router)

	req := authedRequest(http.MethodPost, "/api/v1/answer-segments/segment-1/subjective-ai-grade", bytes.NewBufferString(`{}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/answer-segments/segment-1/subjective-ai-grade", bytes.NewBufferString(`{}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d %s", rec.Code, rec.Body.String())
	}
}

type fixedAdapter struct{}

func (fixedAdapter) Name() string { return "fixed-test-adapter" }

func (fixedAdapter) Grade(_ context.Context, input subjective.AdapterInput) (subjective.AdapterOutput, error) {
	return subjective.AdapterOutput{
		RequestID:         input.RequestID,
		SuggestedScore:    input.Question.Score / 2,
		Confidence:        0.95,
		MatchedPoints:     []grading.PointResult{{Code: "p1", Label: "synthetic matched point", Score: input.Question.Score / 2, EvidenceIDs: []string{"e1"}}},
		MissingPoints:     []grading.PointResult{},
		Evidence:          []grading.Evidence{{Type: "answer_text", EvidenceID: "e1", RubricPointID: "p1", AnswerSegment: input.SegmentID, AnswerText: input.AnswerText, Location: "answer_text", Confidence: 0.9}},
		RiskFlags:         []string{},
		NeedsHumanReview:  true,
		StudentFeedback:   "synthetic feedback",
		TeacherNote:       "synthetic teacher note",
		ModelVersion:      input.ModelPolicy.ModelVersion,
		PromptVersion:     input.ModelPolicy.PromptVersion,
		RubricVersion:     input.Rubric.Version,
		DeliveryMode:      "teacher_suggestion",
		CapabilityProfile: "local-pilot-v1",
		Telemetry: subjective.AdapterTelemetry{
			Adapter: "fixed-test-adapter", Provider: "local",
			Deployment: "fixed-test-deployment", Region: "on_premise",
			Attempts: 1, PriorErrorCodes: []string{},
		},
		RawOutput: map[string]any{"adapter": "fixed-test-adapter"},
		Mock:      false,
	}, nil
}

type invalidAdapter struct{}

func (invalidAdapter) Name() string { return "invalid-test-adapter" }

func (invalidAdapter) Grade(_ context.Context, input subjective.AdapterInput) (subjective.AdapterOutput, error) {
	return subjective.AdapterOutput{
		SuggestedScore: input.Question.Score + 1,
		Confidence:     0.95,
		RawOutput:      map[string]any{"adapter": "invalid-test-adapter"},
		Mock:           false,
	}, nil
}

type countingAdapter struct{ calls int32 }

func (a *countingAdapter) Name() string { return "counting-test-adapter" }

func (a *countingAdapter) Grade(ctx context.Context, input subjective.AdapterInput) (subjective.AdapterOutput, error) {
	atomic.AddInt32(&a.calls, 1)
	return fixedAdapter{}.Grade(ctx, input)
}
