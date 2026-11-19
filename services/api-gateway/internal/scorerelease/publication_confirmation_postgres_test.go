package scorerelease

import (
	"encoding/json"
	"net/http"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestPostgresPublicationConfirmationBindsPreviewToMergedScores(t *testing.T) {
	db := formalExportTestDB(t)
	f := seedConcurrentPublication(t, db)
	service := NewService(NewPostgresStore(db, nil))
	ctx := t.Context()
	base, err := service.Create(ctx, f.tenantID, f.examID, f.actorID, CreateInput{Reason: "synthetic original release", IdempotencyKey: "confirmation-base", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, f.tenantID, base.ID, f.actorID); err != nil {
		t.Fatal(err)
	}
	createCorrection := func(question int, key string, score float64) Release {
		t.Helper()
		qid := f.questionIDs[question]
		draft, err := service.CreateFromRegrade(ctx, f.tenantID, f.examID, f.actorID, CreateRegradeInput{SourceReleaseID: base.ID, QuestionID: qid, Reason: "verified correction", IdempotencyKey: key, Changes: []RegradeChange{{SubmissionID: f.submissionID, QuestionID: qid, Score: score, MaxScore: 5}}})
		if err != nil {
			t.Fatal(err)
		}
		return draft
	}
	first := createCorrection(0, "confirmation-first", 4)
	second := createCorrection(1, "confirmation-second", 3)
	ordinary, err := service.Create(ctx, f.tenantID, f.examID, f.actorID, CreateInput{Reason: "synthetic ordinary release", IdempotencyKey: "confirmation-ordinary", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true}})
	if err != nil {
		t.Fatal(err)
	}
	manager := &auth.User{ID: f.actorID, TenantID: f.tenantID}
	handler := publicationTestMux(NewHandler(service, &formalAuditSink{}))
	draftPath := "/api/v1/score-releases/" + second.ID
	studentPreview := func(mode publicationPreviewMode) (StudentResult, string) {
		t.Helper()
		studentPath := draftPath + "/student-preview?student_id=" + f.studentID + "&publication_preview=" + string(mode)
		response := readPublicationResponse[struct {
			Result              StudentResult `json:"result"`
			PublicationSnapshot string        `json:"publication_snapshot"`
		}](t, requestPublication(handler, manager, http.MethodGet, studentPath, ""), http.StatusOK)
		if len(response.PublicationSnapshot) != 64 {
			t.Fatalf("missing preview digest: %+v", response)
		}
		return response.Result, response.PublicationSnapshot
	}
	publish := func(snapshot string, status int, code string) {
		t.Helper()
		body := ""
		if snapshot != "" {
			input, err := json.Marshal(publicationConfirmation{Snapshot: snapshot})
			if err != nil {
				t.Fatal(err)
			}
			body = string(input)
		}
		requirePublicationStatus(t, requestPublication(handler, manager, http.MethodPost, draftPath+"/publish", body), status, code)
	}
	assertCurrent := func(id string, score float64) {
		t.Helper()
		current, err := service.CurrentPublished(ctx, f.tenantID, f.examID)
		if err != nil || current.Release.ID != id || len(current.Items) != 1 || current.Items[0].TotalScore != score {
			t.Fatalf("incorrect current release: %+v, %v", current, err)
		}
	}

	before, beforeDigest := studentPreview(previewDraft)
	if before.TotalScore != 5 {
		t.Fatalf("original draft score = %.2f, want 5", before.TotalScore)
	}
	// 另一项更正先公开。原摘要和无摘要客户端均不能把未确认的合并结果写进正式版本。
	if _, err = service.Publish(ctx, f.tenantID, first.ID, f.actorID); err != nil {
		t.Fatal(err)
	}
	publish(beforeDigest, http.StatusConflict, "score_release_snapshot_changed")
	publish("", http.StatusConflict, "score_release_snapshot_changed")
	assertCurrent(first.ID, 6)
	unchanged, err := service.Get(ctx, f.tenantID, second.ID)
	if err != nil || unchanged.Release.Status != StatusDraft || len(unchanged.Items) != 1 || unchanged.Items[0].TotalScore != 5 {
		t.Fatalf("conflict changed original draft: %+v, %v", unchanged, err)
	}

	candidate, confirmedDigest := studentPreview(previewCandidate)
	detail := readPublicationResponse[Detail](t, requestPublication(handler, manager, http.MethodGet, draftPath+"?publication_preview=candidate", ""), http.StatusOK)
	if candidate.TotalScore != 7 || confirmedDigest == beforeDigest || detail.PublicationSnapshot != confirmedDigest || len(detail.Items) != 1 || detail.Items[0].TotalScore != 7 {
		t.Fatalf("preview did not show same merged candidate: student=%+v detail=%+v", candidate, detail)
	}
	assertCurrent(first.ID, 6)
	// 即使正式版本换代后成绩不变，也不能用基于旧正式版本的确认凭证切换指针。
	sameScores, err := service.CreateRollback(ctx, f.tenantID, f.examID, f.actorID, RollbackInput{SourceReleaseID: first.ID, Reason: "verify latest publication base", IdempotencyKey: "confirmation-new-base"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, f.tenantID, sameScores.ID, f.actorID); err != nil {
		t.Fatal(err)
	}
	publish(confirmedDigest, http.StatusConflict, "score_release_snapshot_changed")
	assertCurrent(sameScores.ID, 6)
	latest, latestDigest := studentPreview(previewCandidate)
	if latest.TotalScore != candidate.TotalScore || latestDigest == confirmedDigest {
		t.Fatalf("new publication base did not invalidate the previous confirmation: %+v", latest)
	}
	// 预览后又有非冲突的小数更正时，必须重新展示实际合并分数，不能沿用前一次候选。
	decimalCorrection, err := service.CreateFromRegrade(ctx, f.tenantID, f.examID, f.actorID, CreateRegradeInput{
		SourceReleaseID: sameScores.ID, QuestionID: f.questionIDs[0], Reason: "verify decimal correction", IdempotencyKey: "confirmation-decimal",
		Changes: []RegradeChange{{SubmissionID: f.submissionID, QuestionID: f.questionIDs[0], Score: 4.11, MaxScore: 5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, f.tenantID, decimalCorrection.ID, f.actorID); err != nil {
		t.Fatal(err)
	}
	publish(latestDigest, http.StatusConflict, "score_release_snapshot_changed")
	assertCurrent(decimalCorrection.ID, 6.11)
	finalPreview, finalDigest := studentPreview(previewCandidate)
	if finalPreview.TotalScore != 7.11 || finalDigest == latestDigest {
		t.Fatalf("decimal correction did not update the candidate: %+v", finalPreview)
	}
	publish(finalDigest, http.StatusOK, "")
	assertCurrent(second.ID, 7.11)
	student, err := service.StudentResult(ctx, f.tenantID, f.examID, f.studentID)
	if err != nil || student.ReleaseID != second.ID || student.TotalScore != finalPreview.TotalScore || len(student.Questions) != 2 || student.Questions[0].Score != 4.11 || student.Questions[1].Score != 3 {
		t.Fatalf("formal student result differs from confirmed candidate: %+v, %v", student, err)
	}
	publish(finalDigest, http.StatusOK, "")
	assertCurrent(second.ID, 7.11)

	// 普通草稿仍含旧工作表的 4 分。保护复评成绩会改写最终候选，所以旧请求不能直接发布这份草稿。
	ordinaryPath := "/api/v1/score-releases/" + ordinary.ID
	original := readPublicationResponse[Detail](t, requestPublication(handler, manager, http.MethodGet, ordinaryPath+"?publication_preview=draft", ""), http.StatusOK)
	requirePublicationStatus(t, requestPublication(handler, manager, http.MethodPost, ordinaryPath+"/publish", ""), http.StatusConflict, "score_release_snapshot_changed")
	assertCurrent(second.ID, 7.11)
	merged := readPublicationResponse[Detail](t, requestPublication(handler, manager, http.MethodGet, ordinaryPath+"?publication_preview=candidate", ""), http.StatusOK)
	if len(original.Items) != 1 || original.Items[0].TotalScore != 4 || len(merged.Items) != 1 || merged.Items[0].TotalScore != 7.11 ||
		merged.PublicationSnapshot == original.PublicationSnapshot || merged.Release.SourceReleaseID != second.ID {
		t.Fatalf("ordinary preview lost regraded scores: draft=%+v candidate=%+v", original, merged)
	}
	input, err := json.Marshal(publicationConfirmation{Snapshot: merged.PublicationSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	requirePublicationStatus(t, requestPublication(handler, manager, http.MethodPost, ordinaryPath+"/publish", string(input)), http.StatusOK, "")
	assertCurrent(ordinary.ID, 7.11)
	publish(finalDigest, http.StatusOK, "")
	assertCurrent(ordinary.ID, 7.11)
}
