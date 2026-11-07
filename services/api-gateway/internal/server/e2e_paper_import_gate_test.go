package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"os"
	"strings"
	"testing"
)

func TestPaperImportApplyGateE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping paper import PostgreSQL apply gate test")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	ctx := context.Background()
	var tenantID, userID, examID, paperID, paperFileID, answerFileID, questionID string
	err := db.QueryRowContext(ctx, `
WITH fixture AS (
  SELECT t.id AS tenant_id,(MAX(u.id::text) FILTER (WHERE u.username='teacher'))::uuid AS user_id
  FROM tenant t JOIN app_user u ON u.tenant_id=t.id
  WHERE t.code='demo' AND u.username='teacher'
  GROUP BY t.id
), school_row AS (
  INSERT INTO school(tenant_id,name,code,status)
  SELECT tenant_id,'Paper import gate school','paper-import-gate','active' FROM fixture
  RETURNING id,tenant_id
), exam_row AS (
  INSERT INTO exam(tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,appeal_enabled,publish_policy,created_by)
  SELECT f.tenant_id,s.id,'Paper import gate exam','mathematics','quiz',10,'draft','ai_assisted',true,'manual_after_confirmation',f.user_id
  FROM fixture f JOIN school_row s ON s.tenant_id=f.tenant_id
  RETURNING id,tenant_id,created_by
), paper_file AS (
  INSERT INTO file_asset(tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by)
  SELECT tenant_id,id,'exam',id,'paper.pdf','application/pdf',1,repeat('b',64),'test','paper-import-gate-paper','tenant',created_by FROM exam_row
  RETURNING id,tenant_id,exam_id,uploaded_by
), answer_file AS (
  INSERT INTO file_asset(tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by)
  SELECT tenant_id,id,'exam',id,'answer.pdf','application/pdf',1,repeat('c',64),'test','paper-import-gate-answer','tenant',created_by FROM exam_row
  RETURNING id,tenant_id,exam_id
), paper_row AS (
  INSERT INTO exam_paper(tenant_id,exam_id,file_asset_id,version_no,status,uploaded_by)
  SELECT tenant_id,exam_id,id,1,'uploaded',uploaded_by FROM paper_file
  RETURNING id,tenant_id,exam_id
), question_row AS (
  INSERT INTO question(tenant_id,exam_id,exam_paper_id,question_no,question_type,score,knowledge_points,answer_area,sort_order,status)
  SELECT tenant_id,exam_id,id,'1','short_answer',10,'[]','{}',1,'active' FROM paper_row
  RETURNING id,tenant_id,exam_id
)
SELECT f.tenant_id::text,f.user_id::text,e.id::text,p.id::text,pf.id::text,af.id::text,q.id::text
FROM fixture f
JOIN exam_row e ON e.tenant_id=f.tenant_id
JOIN paper_row p ON p.exam_id=e.id
JOIN paper_file pf ON pf.exam_id=e.id
JOIN answer_file af ON af.exam_id=e.id
JOIN question_row q ON q.exam_id=e.id
`).Scan(&tenantID, &userID, &examID, &paperID, &paperFileID, &answerFileID, &questionID)
	if err != nil {
		t.Fatalf("seed paper import fixture: %v", err)
	}

	store := paper.NewPostgresStore(db)
	job, err := store.CreatePaperImport(ctx, tenantID, examID, userID, paper.CreatePaperImportInput{
		ExamPaperID: paperID, PaperFileAssetID: paperFileID, AnswerFileAssetID: answerFileID, Subject: "mathematics",
	})
	if err != nil {
		t.Fatalf("create paper import: %v", err)
	}
	runtimeStore := workerruntime.NewPostgresStore(db)
	parseDocuments := make([]paper.PaperImportParseDocument, 0, len(job.Sources))
	for _, source := range job.Sources {
		parseDocuments = append(parseDocuments, paper.PaperImportParseDocument{SourceID: source.ID, FileAssetID: source.FileAssetID, DocumentIndex: source.DocumentIndex, RoleHint: source.RoleHint, Content: "1. imported"})
	}
	parseInput := paper.PaperImportParseRequest{Documents: parseDocuments}
	if err = store.QueuePaperImportParse(ctx, tenantID, job, userID, parseInput); err != nil {
		t.Fatalf("queue versioned apply-gate parse: %v", err)
	}
	scenario := paperApplyGateScenario{Context: ctx, DB: db, Store: store, Runtime: runtimeStore,
		TenantID: tenantID, UserID: userID, ExamID: examID, PaperFileID: paperFileID,
		AnswerFileID: answerFileID, QuestionID: questionID, Job: job, Documents: parseDocuments}
	assertPersistedPages := func(t *testing.T, where string, expected []paper.PaperImportDecodedPage) string {
		return e2eAssertPersistedPaperPages(t, scenario, where, expected)
	}

	t.Run("nil pages persist as an empty JSON array", func(t *testing.T) {
		assertPersistedPages(t, `pages = '[]'::jsonb`, []paper.PaperImportDecodedPage{})
	})
	t.Run("explicit empty pages persist as an empty JSON array", func(t *testing.T) {
		explicitlyEmpty := paper.PaperImportParseRequest{Documents: parseDocuments, Pages: []paper.PaperImportDecodedPage{}}
		if err := store.QueuePaperImportParse(ctx, tenantID, job, userID, explicitlyEmpty); err != nil {
			t.Fatalf("queue parse with explicit empty pages: %v", err)
		}
		assertPersistedPages(t, `pages = '[]'::jsonb`, explicitlyEmpty.Pages)
	})
	populatedPages := []paper.PaperImportDecodedPage{
		{Role: "question", SourceID: job.Sources[0].ID, DocumentIndex: job.Sources[0].DocumentIndex, PageNo: 2, FileAssetID: "decoded-page-2", SHA256: strings.Repeat("2", 64), Width: 1200, Height: 1600},
		{Role: "question", SourceID: job.Sources[0].ID, DocumentIndex: job.Sources[0].DocumentIndex, PageNo: 1, FileAssetID: "decoded-page-1", SHA256: strings.Repeat("1", 64), Width: 1200, Height: 1600},
	}
	t.Run("populated pages preserve content and order", func(t *testing.T) {
		withPages := paper.PaperImportParseRequest{Documents: parseDocuments, Pages: populatedPages}
		if err := store.QueuePaperImportParse(ctx, tenantID, job, userID, withPages); err != nil {
			t.Fatalf("queue parse with populated pages: %v", err)
		}
		assertPersistedPages(t, `jsonb_array_length(pages) > 0`, populatedPages)
	})
	claimedParse, err := runtimeStore.Claim(ctx, tenantID, workerruntime.ClaimInput{QueueName: "paper-parse", WorkerService: "apply-gate-test", WorkerInstanceID: "apply-gate-test", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimedParse) != 1 {
		t.Fatalf("claim versioned apply-gate parse: %#v err=%v", claimedParse, err)
	}
	binding, err := store.LoadPaperImportParseRunInput(ctx, tenantID, claimedParse[0].SourceID)
	if err != nil {
		t.Fatalf("load versioned apply-gate input: %v", err)
	}
	scenario.ClaimedParse, scenario.Binding = claimedParse, binding
	t.Run("MIG immutable parser input and accepted source snapshot", func(t *testing.T) {
		e2eAssertPaperStageSnapshotImmutable(t, scenario)
	})
	t.Run("IMP missing active attempt rolls back task completion", func(t *testing.T) {
		e2eAssertPaperMissingAttemptRollback(t, scenario)
	})
	t.Run("IMP binding rejects wrong stage and input without completing the task", func(t *testing.T) {
		e2eAssertPaperStageAndInputFence(t, scenario)
	})
	e2eAssertInvalidPaperImportGate(t, scenario)
	previousRuleID := e2eReviewAndApplyPaperImport(t, scenario)
	e2eAssertAppliedPaperCanonicalFacts(t, scenario, previousRuleID)
	e2eAssertPaperSourceReorder(t, scenario)
	t.Run("OCR completion atomically hands off to restart-safe parse", func(t *testing.T) {
		e2eAssertPaperOCRParseRecovery(t, scenario)
	})
}

type paperApplyGateScenario struct {
	Context                                                         context.Context
	DB                                                              *sql.DB
	Store                                                           *paper.PostgresStore
	Runtime                                                         *workerruntime.PostgresStore
	TenantID, UserID, ExamID, PaperFileID, AnswerFileID, QuestionID string
	Job                                                             paper.PaperImportJob
	Documents                                                       []paper.PaperImportParseDocument
	ClaimedParse                                                    []workerruntime.Task
	Binding                                                         paper.PaperImportRunBinding
}
