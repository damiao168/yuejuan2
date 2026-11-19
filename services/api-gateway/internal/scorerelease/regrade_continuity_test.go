package scorerelease

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// 两个申诉任务在同一来源上并行复核；第二个发布必须保留第一个任务的更正，且不改原来源供申诉结案核验。
func TestIndependentRegradeDraftsPublishWithoutLosingEarlierCorrection(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	fact := fixtureFact(2)
	fact.MaxScore = 10
	second := fact.Questions[0]
	second.QuestionID, second.QuestionNo, second.FinalGradeID = "question-2", "Q2", "grade-2"
	fact.Questions = append(fact.Questions, second)
	fact.TotalScore = 4
	store.SeedFacts(exam, []SubmissionFact{fact})
	base, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "initial", IdempotencyKey: "parallel-initial"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(t.Context(), tenant, base.ID, actor); err != nil {
		t.Fatal(err)
	}
	makeDraft := func(questionID, key string, score float64) Release {
		t.Helper()
		release, err := service.CreateFromRegrade(t.Context(), tenant, exam, actor, CreateRegradeInput{SourceReleaseID: base.ID, QuestionID: questionID, Reason: "reviewed appeal", IdempotencyKey: key,
			Changes: []RegradeChange{{SubmissionID: fact.SubmissionID, QuestionID: questionID, Score: score, MaxScore: 5, ReviewedGradeID: key}}})
		if err != nil {
			t.Fatal(err)
		}
		return release
	}
	first, secondDraft := makeDraft("question-1", "parallel-first", 4), makeDraft("question-2", "parallel-second", 3)
	// 旧工作表草稿先于重评创建；发布时也必须重新核对，不能仅在创建时保护。
	ordinary, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "visibility change", IdempotencyKey: "ordinary-before-correction"})
	if err != nil {
		t.Fatal(err)
	}
	anonymous, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "publish anonymous examples", IdempotencyKey: "anonymous-before-correction", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true, ShowHighScorePaper: true}})
	if err != nil {
		t.Fatal(err)
	}
	handler := publicationTestMux(NewHandler(service, nil))
	manager := &auth.User{ID: actor, TenantID: tenant}
	draftPath := "/api/v1/score-releases/" + secondDraft.ID
	readPreview := func(path string, mode publicationPreviewMode) Detail {
		t.Helper()
		response := readPublicationResponse[Detail](t, requestPublication(handler, manager, http.MethodGet, path+"?publication_preview="+string(mode), ""), http.StatusOK)
		if len(response.PublicationSnapshot) != 64 {
			t.Fatalf("missing confirmation digest: %+v", response)
		}
		return response
	}
	publish := func(path, body string, status int, code string) {
		t.Helper()
		requirePublicationStatus(t, requestPublication(handler, manager, http.MethodPost, path+"/publish", body), status, code)
	}
	requirePublicationStatus(t, requestPublication(handler, manager, http.MethodGet, draftPath+"?publication_preview=unknown", ""), http.StatusBadRequest, "invalid_score_release_input")
	requirePublicationStatus(t, requestPublication(handler, &auth.User{ID: actor, TenantID: "other-tenant"}, http.MethodGet, draftPath+"?publication_preview=candidate", ""), http.StatusNotFound, "score_release_not_found")
	for _, body := range []string{`{"unknown":true}`, `{} {}`} {
		publish(draftPath, body, http.StatusBadRequest, "invalid_json")
	}
	const snapshotBody = `{"publication_snapshot":%q}`
	for _, invalid := range []string{"too-short", strings.Repeat("A", 64)} {
		publish(draftPath, fmt.Sprintf(snapshotBody, invalid), http.StatusBadRequest, "invalid_score_release_input")
	}
	before := readPreview(draftPath, previewDraft)
	if len(before.Items) != 1 || before.Items[0].TotalScore != 5 {
		t.Fatalf("original draft: %+v", before)
	}
	if _, err := service.Publish(t.Context(), tenant, first.ID, actor); err != nil {
		t.Fatal(err)
	}
	inherited, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "rebuild from old facts", IdempotencyKey: "ordinary-after-correction"})
	if err != nil || inherited.SourceReleaseID != first.ID {
		t.Fatalf("ordinary draft must inherit correction: %+v %v", inherited, err)
	}
	if detail, err := service.Get(t.Context(), tenant, inherited.ID); err != nil || detail.Items[0].TotalScore != 6 {
		t.Fatalf("inherited preview: %+v %v", detail, err)
	}
	publish(draftPath, fmt.Sprintf(snapshotBody, before.PublicationSnapshot), http.StatusConflict, "score_release_snapshot_changed")
	publish(draftPath, " ", http.StatusConflict, "score_release_snapshot_changed")
	merged := readPreview(draftPath, previewCandidate)
	if len(merged.Items) != 1 || merged.Items[0].TotalScore != 7 || merged.PublicationSnapshot == before.PublicationSnapshot {
		t.Fatalf("candidate skipped earlier correction: %+v", merged)
	}
	publish(draftPath, fmt.Sprintf(snapshotBody, merged.PublicationSnapshot), http.StatusOK, "")
	current, err := service.CurrentPublished(t.Context(), tenant, exam)
	if err != nil || current.Release.SourceReleaseID != base.ID || current.Release.SupersedesReleaseID != first.ID || current.Items[0].TotalScore != 7 {
		t.Fatalf("parallel continuation=%+v err=%v", current, err)
	}
	prior, _ := service.Get(t.Context(), tenant, first.ID)
	if prior.Items[0].TotalScore != 6 {
		t.Fatal("immutable earlier correction changed")
	}
	anonymousPath := "/api/v1/score-releases/" + anonymous.ID
	requirePublicationStatus(t, requestPublication(handler, manager, http.MethodGet, anonymousPath+"?publication_preview=candidate", ""), http.StatusConflict, "score_release_draft_refresh_required")
	if anonymousDraft := readPreview(anonymousPath, previewDraft); len(anonymousDraft.Items) != 1 || anonymousDraft.Items[0].TotalScore != 4 {
		t.Fatalf("refused candidate altered anonymous paper draft: %+v", anonymousDraft)
	}
	// 工作表始终保留原4分，不靠测试改库追平。旧草稿发布必须继承两项已公布的更正。
	ordinaryPath := "/api/v1/score-releases/" + ordinary.ID
	publish(ordinaryPath, "", http.StatusConflict, "score_release_snapshot_changed")
	ordinaryPreview := readPreview(ordinaryPath, previewCandidate)
	publish(ordinaryPath, fmt.Sprintf(snapshotBody, ordinaryPreview.PublicationSnapshot), http.StatusOK, "")
	if published, err := service.Get(t.Context(), tenant, ordinary.ID); err != nil || published.Release.SourceReleaseID != secondDraft.ID || published.Release.SupersedesReleaseID != secondDraft.ID {
		t.Fatalf("old ordinary draft cannot recover: %+v %v", published, err)
	}
	if detail, err := service.CurrentPublished(t.Context(), tenant, exam); err != nil || detail.Items[0].TotalScore != 7 {
		t.Fatalf("ordinary publish lost corrections: %+v %v", detail, err)
	}
	matching, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "matching facts", IdempotencyKey: "ordinary-matching-correction"})
	if err != nil {
		t.Fatalf("matching facts rejected: %v", err)
	}
	if _, err := service.Publish(t.Context(), tenant, matching.ID, actor); err != nil {
		t.Fatal(err)
	}
	if next, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "old facts after matching publish", IdempotencyKey: "ordinary-reverted-after-matching"}); err != nil || next.SourceReleaseID != matching.ID {
		t.Fatalf("ordinary intermediary lost correction inheritance: %+v %v", next, err)
	}
	if replay, err := service.Publish(t.Context(), tenant, ordinary.ID, actor); err != nil || replay.SourceReleaseID != secondDraft.ID {
		t.Fatalf("published retry changed historical source: %+v %v", replay, err)
	}
	rollback, err := service.CreateRollback(t.Context(), tenant, exam, actor, RollbackInput{SourceReleaseID: base.ID, Reason: "explicit rollback", IdempotencyKey: "explicit-before-corrections"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(t.Context(), tenant, rollback.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "facts after explicit rollback", IdempotencyKey: "ordinary-after-explicit-rollback"}); err != nil {
		t.Fatalf("rolled-back branch incorrectly retained protection: %v", err)
	}
}
