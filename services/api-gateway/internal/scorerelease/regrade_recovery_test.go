package scorerelease

import "testing"

func TestRegradeStableKeyRecoversOnlyExactHistoricalRandomKeySnapshot(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(2)})
	source, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "first", IdempotencyKey: "first-release-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(t.Context(), tenant, source.ID, actor); err != nil {
		t.Fatal(err)
	}
	input := CreateRegradeInput{SourceReleaseID: source.ID, QuestionID: "question-1", Reason: "核对原卷", IdempotencyKey: "historical-random-key", Changes: []RegradeChange{{SubmissionID: "submission-1", QuestionID: "question-1", Score: 3, MaxScore: 5, ReviewedGradeID: "reviewed-grade-1"}}}
	old, err := service.CreateFromRegrade(t.Context(), tenant, exam, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	input.IdempotencyKey = "regrade-job:job-1"
	recovered, err := service.CreateFromRegrade(t.Context(), tenant, exam, actor, input)
	if err != nil || recovered.ID != old.ID {
		t.Fatalf("recovery=%+v error=%v", recovered, err)
	}
	if len(store.releases) != 2 {
		t.Fatal("created duplicate historical draft")
	}
	// 相同分数但来自另一份正式复核事实，不能回认旧草稿。
	input.IdempotencyKey = "regrade-job:job-2"
	input.Changes[0].ReviewedGradeID = "reviewed-grade-2"
	different, err := service.CreateFromRegrade(t.Context(), tenant, exam, actor, input)
	if err != nil || different.ID == old.ID {
		t.Fatal("different review evidence reused old draft", err)
	}
	// 保留历史随机键，旧客户端响应重试仍命中原草稿。
	input.IdempotencyKey = "historical-random-key"
	retried, err := service.CreateFromRegrade(t.Context(), tenant, exam, actor, input)
	if err != nil || retried.ID != old.ID {
		t.Fatal("historical key broken", err)
	}
}

func TestRegradeHistoricalDuplicateCandidatesRequireManualResolution(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(2)})
	source, _ := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "first", IdempotencyKey: "first-key-duplicate"})
	_, _ = service.Publish(t.Context(), tenant, source.ID, actor)
	input := CreateRegradeInput{SourceReleaseID: source.ID, QuestionID: "question-1", Reason: "核对原卷", IdempotencyKey: "old-key-1", Changes: []RegradeChange{{SubmissionID: "submission-1", QuestionID: "question-1", Score: 3, MaxScore: 5, ReviewedGradeID: "reviewed-grade"}}}
	if _, err := service.CreateFromRegrade(t.Context(), tenant, exam, actor, input); err != nil {
		t.Fatal(err)
	}
	input.IdempotencyKey = "old-key-2"
	if _, err := service.CreateFromRegrade(t.Context(), tenant, exam, actor, input); err != nil {
		t.Fatal(err)
	}
	input.IdempotencyKey = "regrade-job:job"
	if _, err := service.CreateFromRegrade(t.Context(), tenant, exam, actor, input); err != ErrInvalidTransition {
		t.Fatalf("ambiguous drafts accepted: %v", err)
	}
	if len(store.releases) != 3 {
		t.Fatal("ambiguous recovery created another draft")
	}
}
