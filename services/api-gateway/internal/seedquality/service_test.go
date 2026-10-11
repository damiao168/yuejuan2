package seedquality

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
)

type fakeGoldReader struct{ items []goldpaper.GoldPaper }

func (f *fakeGoldReader) ListActiveApproved(context.Context, string, string, string) ([]goldpaper.GoldPaper, error) {
	return append([]goldpaper.GoldPaper(nil), f.items...), nil
}

type fakeQualification struct{ err error }

func (f fakeQualification) RequireQualification(context.Context, string, string, string, string) error {
	return f.err
}

type fakeContextSource struct {
	snapshot assessment.ExamQuestionSnapshot
}

func (f fakeContextSource) GetQuestionSnapshot(context.Context, string, string, string) (assessment.ExamQuestionSnapshot, error) {
	return f.snapshot, nil
}

type fixedRandom struct {
	integers []int
	floats   []float64
}

func (f *fixedRandom) Intn(bound int) int {
	value := 0
	if len(f.integers) > 0 {
		value, f.integers = f.integers[0], f.integers[1:]
	}
	if bound <= 1 {
		return 0
	}
	return value % bound
}

func (f *fixedRandom) Float64() float64 {
	if len(f.floats) == 0 {
		return 1
	}
	value := f.floats[0]
	f.floats = f.floats[1:]
	return value
}

func TestSeedPolicySamplingAndHiddenTraitObservation(t *testing.T) {
	store := NewMemoryStore()
	gold := &fakeGoldReader{items: []goldpaper.GoldPaper{activeGold("gold-1", 1, "extended_response", map[string]any{"ideas": 2, "language": "clear"})}}
	service := NewService(store, gold, fakeQualification{})
	service.random = &fixedRandom{floats: []float64{1, 1}}
	fixedNow := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }

	policy, err := service.PutPolicy(context.Background(), "tenant-a", "exam-a", "question-a", "manager-a", PutPolicyInput{
		Rate: .01, MinInterval: 2, MaxInterval: 2, Status: PolicyActive,
	})
	if err != nil || len(policy.ActiveGoldFingerprint) != 64 {
		t.Fatalf("policy=%+v err=%v", policy, err)
	}
	if _, issued, err := service.MaybeIssue(context.Background(), "tenant-a", "exam-a", "question-a", "Q12", "grader-a"); err != nil || issued {
		t.Fatalf("first slot must remain ordinary: issued=%v err=%v", issued, err)
	}
	task, issued, err := service.MaybeIssue(context.Background(), "tenant-a", "exam-a", "question-a", "Q12", "grader-a")
	if err != nil || !issued || task.Source != "manual" || task.Status != "in_progress" || task.AnonymousCode == "" {
		t.Fatalf("hidden task=%+v issued=%v err=%v", task, issued, err)
	}
	payload, _ := json.Marshal(task)
	serialized := string(payload)
	for _, forbidden := range []string{"gold-1", "reference_score", "submission", "seed", "extended_response"} {
		if strings.Contains(strings.ToLower(serialized), strings.ToLower(forbidden)) {
			t.Fatalf("grader task leaked %q: %s", forbidden, serialized)
		}
	}

	assertHiddenSeedTraitSubmission(t, service, task)
}

func TestStructuredSeedStoresCriterionObservationAndNeverFallsThrough(t *testing.T) {
	store := NewMemoryStore()
	gold := &fakeGoldReader{items: []goldpaper.GoldPaper{activeGold("gold-math", 2, "structured_steps", map[string]any{"step_1": true, "expression": "x=2"})}}
	service := NewService(store, gold, fakeQualification{})
	service.random = &fixedRandom{floats: []float64{0}}
	_, err := service.PutPolicy(context.Background(), "tenant-a", "exam-a", "question-a", "manager-a", PutPolicyInput{
		Rate: 1, MinInterval: 1, MaxInterval: 3, Status: PolicyActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	task, issued, err := service.MaybeIssue(context.Background(), "tenant-a", "exam-a", "question-a", "Q3", "grader-a")
	if err != nil || !issued {
		t.Fatalf("issued=%v err=%v", issued, err)
	}
	_, handled, err := service.TrySubmit(context.Background(), "tenant-a", task.ID, "grader-a", SubmitInput{
		Score: 4, ExpectedRevision: 1, RubricSelections: map[string]any{"step_1": true, "expression": "x=3"},
	})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	items, _ := service.ListObservations(context.Background(), "tenant-a", ObservationFilter{})
	if len(items) != 1 || items[0].ObservationKind != ObservationCriterion || items[0].CriterionObservation == nil || items[0].TraitObservation != nil ||
		items[0].RubricAgreement == nil || *items[0].RubricAgreement != .5 {
		t.Fatalf("unexpected criterion observation: %+v", items)
	}
	// Seed ID 完全由此边界处理，因此复核提交路径不能继续，也不能创建 human_grade/final_grade 事实。
	if _, handled, err = service.TrySubmit(context.Background(), "tenant-a", task.ID, "grader-a", SubmitInput{
		Score: 4, ExpectedRevision: 1,
	}); !handled || !errors.Is(err, ErrConflict) {
		t.Fatalf("completed Seed should stay inside quality boundary: handled=%v err=%v", handled, err)
	}
	if _, handled, err = service.TrySubmit(context.Background(), "tenant-a", "ordinary-task", "grader-a", SubmitInput{
		Score: 4, ExpectedRevision: 1,
	}); handled || err != nil {
		t.Fatalf("ordinary task must fall through: handled=%v err=%v", handled, err)
	}
}

func TestSeedRequiresCurrentQualificationAndConfiguredGoldFingerprint(t *testing.T) {
	store := NewMemoryStore()
	reader := &fakeGoldReader{items: []goldpaper.GoldPaper{activeGold("gold-1", 1, "short_constructed", map[string]any{})}}
	service := NewService(store, reader, fakeQualification{err: errors.New("not qualified")})
	_, err := service.PutPolicy(context.Background(), "tenant-a", "exam-a", "question-a", "manager-a", PutPolicyInput{
		Rate: .1, MinInterval: 2, MaxInterval: 5, Status: PolicyActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.MaybeIssue(context.Background(), "tenant-a", "exam-a", "question-a", "Q1", "grader-a"); !errors.Is(err, ErrQualificationNeeded) {
		t.Fatalf("qualification error=%v", err)
	}
	service.qualification = fakeQualification{}
	reader.items = []goldpaper.GoldPaper{activeGold("gold-2", 1, "short_constructed", map[string]any{})}
	if _, _, err := service.MaybeIssue(context.Background(), "tenant-a", "exam-a", "question-a", "Q1", "grader-a"); !errors.Is(err, ErrGoldSetChanged) {
		t.Fatalf("gold set error=%v", err)
	}
}

func TestSeedContextUsesNormalWorkbenchShapeWithoutGoldDisclosure(t *testing.T) {
	store := NewMemoryStore()
	reader := &fakeGoldReader{items: []goldpaper.GoldPaper{activeGold("gold-secret", 1, "extended_response", map[string]any{"ideas": 2})}}
	snapshot := assessment.ExamQuestionSnapshot{ID: "snapshot-a", ExamID: "exam-a", QuestionID: "question-a", SnapshotVersion: 3,
		SubjectCode: assessment.SubjectChinese, ArchetypeCode: "extended_response", RiskTier: assessment.RiskR3,
		AllowedEvidenceTypes: []assessment.EvidenceType{assessment.EvidenceTextSpan},
		ProfileSnapshot:      map[string]any{"parser_policy": map[string]any{"mode": "continuous_text"}},
		ArchetypeSnapshot:    map[string]any{"response_schema": map[string]any{"type": "constructed_response"}},
		RubricSnapshot:       map[string]any{"version": "v3", "max_score": 5, "points": []any{map[string]any{"id": "ideas", "description": "内容", "score": 5}}}}
	service := NewService(store, reader, fakeQualification{}, fakeContextSource{snapshot: snapshot})
	service.random = &fixedRandom{floats: []float64{0}}
	_, err := service.PutPolicy(context.Background(), "tenant-a", "exam-a", "question-a", "manager-a", PutPolicyInput{
		Rate: 1, MinInterval: 1, MaxInterval: 2, Status: PolicyActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	task, issued, err := service.MaybeIssue(context.Background(), "tenant-a", "exam-a", "question-a", "Q8", "grader-a")
	if err != nil || !issued {
		t.Fatalf("issue=%v err=%v", issued, err)
	}

	graderContext, handled, err := service.GetGraderTaskContext(context.Background(), "tenant-a", task.ID, "grader-a")
	if err != nil || !handled {
		t.Fatalf("context handled=%v err=%v", handled, err)
	}
	if graderContext.Task.AnswerSegmentID == "" || graderContext.Task.SubmissionID == "" ||
		graderContext.Task.AnswerSegmentID == "hidden" || graderContext.AnswerArtifact.SegmentImageURL != "/api/v1/review-tasks/"+task.ID+"/segment-image" ||
		graderContext.Question.ID != "question-a" || graderContext.Question.Score != 5 || graderContext.FrozenRubric.Version != "v3" ||
		graderContext.QuestionSnapshot.ID != "snapshot-a" || graderContext.SubjectToolHints.SubjectCode != assessment.SubjectChinese {
		t.Fatalf("incomplete normal workbench context: %+v", graderContext)
	}
	payload, _ := json.Marshal(graderContext)
	serialized := strings.ToLower(string(payload))
	for _, forbidden := range []string{"gold-secret", "reference_score", "hidden-submission", "answer-segments/hidden", "is_seed", "seed_task"} {
		if strings.Contains(serialized, strings.ToLower(forbidden)) {
			t.Fatalf("grader context leaked %q: %s", forbidden, payload)
		}
	}
	image, handled, err := service.GetGraderImageSource(context.Background(), "tenant-a", task.ID, "grader-a")
	if err != nil || !handled || image.SourceURL != "/api/v1/answer-segments/hidden/image" || image.AnswerSegmentID != "hidden" {
		t.Fatalf("image hook=%+v handled=%v err=%v", image, handled, err)
	}
	imageJSON, _ := json.Marshal(image)
	if strings.Contains(string(imageJSON), "hidden") {
		t.Fatalf("server-only image source serialized: %s", imageJSON)
	}
	if _, handled, err := service.TrySubmit(context.Background(), "tenant-a", task.ID, "grader-a", SubmitInput{
		Score: 4, ExpectedRevision: graderContext.ExpectedRevision, RubricSelections: map[string]any{"ideas": 2},
	}); err != nil || !handled {
		t.Fatalf("same-route submit hook handled=%v err=%v", handled, err)
	}
}

func activeGold(id string, version int, archetype string, criteria map[string]any) goldpaper.GoldPaper {
	now := time.Now().UTC()
	return goldpaper.GoldPaper{ID: id, ExamID: "exam-a", QuestionID: "question-a", SubmissionID: "hidden-submission",
		AnswerImageURL: "/api/v1/answer-segments/hidden/image", ActiveVersion: version, Status: goldpaper.StatusActive,
		ArchetypeCode: archetype, Versions: []goldpaper.Version{{Version: version, ExamQuestionSnapshotID: "snapshot-a",
			ReferenceScore: 4, MaxScore: 5, TraitScores: criteria, ApprovedAt: &now}}}
}

// 阅卷者提交只得到普通回执；管理侧随后读取独立的质量观察事实。
func assertHiddenSeedTraitSubmission(t *testing.T, service *Service, task Task) {
	t.Helper()
	receipt, handled, err := service.TrySubmit(context.Background(), "tenant-a", task.ID, "grader-a", SubmitInput{
		Score: 3, ExpectedRevision: task.Revision, RubricSelections: map[string]any{"ideas": 2, "language": "clear"},
	})
	if err != nil || !handled || receipt.Status != "completed" || receipt.Revision != 2 {
		t.Fatalf("receipt=%+v handled=%v err=%v", receipt, handled, err)
	}
	receiptJSON, _ := json.Marshal(receipt)
	if strings.Contains(string(receiptJSON), "reference") || strings.Contains(string(receiptJSON), "seed") || strings.Contains(string(receiptJSON), "gold") {
		t.Fatalf("completion revealed hidden quality task: %s", receiptJSON)
	}
	observations, err := service.ListObservations(context.Background(), "tenant-a", ObservationFilter{ExamID: "exam-a"})
	if err != nil || len(observations) != 1 {
		t.Fatalf("observations=%+v err=%v", observations, err)
	}
	observation := observations[0]
	if observation.ObservationKind != ObservationTrait || observation.TraitObservation["ideas"] != 2 || observation.CriterionObservation != nil || observation.AbsoluteError != 1 {
		t.Fatalf("unexpected trait observation: %+v", observation)
	}
}
