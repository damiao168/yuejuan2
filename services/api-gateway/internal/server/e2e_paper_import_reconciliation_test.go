package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
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
	assertPersistedPages := func(t *testing.T, where string, expected []paper.PaperImportDecodedPage) string {
		t.Helper()
		var inputID, pagesType string
		var pagesJSON []byte
		if err := db.QueryRowContext(ctx, `SELECT id::text,pages,jsonb_typeof(pages) FROM paper_import_parse_input WHERE tenant_id=$1::uuid AND paper_import_id=$2::uuid AND `+where+` ORDER BY created_at DESC LIMIT 1`, tenantID, job.ID).Scan(&inputID, &pagesJSON, &pagesType); err != nil {
			t.Fatalf("load persisted parse pages: %v", err)
		}
		if pagesType != "array" {
			t.Fatalf("persisted parse pages type = %q, want array", pagesType)
		}
		var persisted []paper.PaperImportDecodedPage
		if err := json.Unmarshal(pagesJSON, &persisted); err != nil {
			t.Fatalf("decode persisted parse pages: %v", err)
		}
		if len(persisted) != len(expected) {
			t.Fatalf("persisted parse pages = %#v, want %#v", persisted, expected)
		}
		for index := range expected {
			if persisted[index] != expected[index] {
				t.Fatalf("persisted parse page %d = %#v, want %#v", index, persisted[index], expected[index])
			}
		}
		loaded, err := store.LoadPaperImportParseRunInput(ctx, tenantID, inputID)
		if err != nil {
			t.Fatalf("reload persisted parse pages: %v", err)
		}
		if len(loaded.Input.Pages) != len(expected) {
			t.Fatalf("reloaded parse pages = %#v, want %#v", loaded.Input.Pages, expected)
		}
		for index := range expected {
			if loaded.Input.Pages[index] != expected[index] {
				t.Fatalf("reloaded parse page %d = %#v, want %#v", index, loaded.Input.Pages[index], expected[index])
			}
		}
		return inputID
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
	invalidScore := 10.0
	t.Run("MIG immutable parser input and accepted source snapshot", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `UPDATE paper_import_parse_input SET documents='[]'::jsonb WHERE id=$1::uuid`, binding.InputID); err == nil {
			t.Fatal("persisted stage input was mutable")
		}
		if _, err := db.ExecContext(ctx, `UPDATE paper_import_run SET source_snapshot='[]'::jsonb WHERE id=$1::uuid`, job.RunID); err == nil {
			t.Fatal("accepted material snapshot was mutable")
		}
		loaded, err := store.LoadPaperImportParseRunInput(ctx, tenantID, binding.InputID)
		if err != nil || loaded.InputHash != binding.InputHash || len(loaded.Input.Documents) != len(binding.Input.Documents) {
			t.Fatalf("immutable input changed: %#v %v", loaded, err)
		}
	})
	t.Run("IMP missing active attempt rolls back task completion", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `UPDATE agent_worker_task_attempt SET status='failed',completed_at=now() WHERE task_id=$1::uuid AND attempt_no=1`, claimedParse[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CompletePaperImportParseTask(ctx, tenantID, claimedParse[0].ID, claimedParse[0].LeaseToken, binding, paper.PaperImportParseResult{}, 1); !errors.Is(err, paper.ErrConflict) {
			t.Fatalf("missing active attempt accepted: %v", err)
		}
		var taskStatus, jobStatus string
		if err := db.QueryRowContext(ctx, `SELECT t.status,j.status FROM agent_worker_task t JOIN paper_import_job j ON j.id=$2::uuid WHERE t.id=$1::uuid`, claimedParse[0].ID, job.ID).Scan(&taskStatus, &jobStatus); err != nil {
			t.Fatal(err)
		}
		if taskStatus != "leased" || jobStatus != "processing" {
			t.Fatalf("partial commit: %s %s", taskStatus, jobStatus)
		}
		if _, err := db.ExecContext(ctx, `UPDATE agent_worker_task_attempt SET status='leased',completed_at=NULL WHERE task_id=$1::uuid AND attempt_no=1`, claimedParse[0].ID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("IMP binding rejects wrong stage and input without completing the task", func(t *testing.T) {
		for _, mutation := range []struct{ name, update string }{
			{"wrong stage", `UPDATE agent_worker_task SET task_type='ocr' WHERE id=$1::uuid`},
			{"wrong input", `UPDATE agent_worker_task SET source_id=gen_random_uuid() WHERE id=$1::uuid`},
		} {
			t.Run(mutation.name, func(t *testing.T) {
				if _, err := db.ExecContext(ctx, mutation.update, claimedParse[0].ID); err != nil {
					t.Fatal(err)
				}
				_, err := store.CompletePaperImportParseTask(ctx, tenantID, claimedParse[0].ID, claimedParse[0].LeaseToken, binding, paper.PaperImportParseResult{}, 1)
				if !errors.Is(err, paper.ErrConflict) {
					t.Fatalf("mismatched stage/input published: %v", err)
				}
				var taskStatus, jobStatus string
				var candidates int
				if err := db.QueryRowContext(ctx, `SELECT t.status,j.status,jsonb_array_length(j.question_candidates) FROM agent_worker_task t JOIN paper_import_job j ON j.id=$2::uuid AND j.tenant_id=t.tenant_id WHERE t.id=$1::uuid`, claimedParse[0].ID, job.ID).Scan(&taskStatus, &jobStatus, &candidates); err != nil {
					t.Fatal(err)
				}
				if taskStatus != "leased" || jobStatus != "processing" || candidates != 0 {
					t.Fatalf("rejected result changed facts: %s %s %d", taskStatus, jobStatus, candidates)
				}
				if _, err := db.ExecContext(ctx, `UPDATE agent_worker_task SET task_type='paper_parse',source_id=$2::uuid WHERE id=$1::uuid`, claimedParse[0].ID, binding.InputID); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
	if _, err = store.CompletePaperImportParseTask(ctx, tenantID, claimedParse[0].ID, claimedParse[0].LeaseToken, binding, paper.PaperImportParseResult{
		QuestionCandidates: []paper.QuestionCandidate{{CandidateID: "invalid-q1", QuestionNoRaw: "1", QuestionType: "essay", Stem: "Imported 1", Score: &invalidScore, Confidence: .99}},
	}, 1); err != nil {
		t.Fatalf("publish versioned invalid candidate: %v", err)
	}
	if _, err := store.ApplyPaperImport(ctx, tenantID, job.ID, userID); !errors.Is(err, paper.ErrInvalidInput) {
		t.Fatalf("PostgreSQL apply gate accepted invalid reconciliation: %v", err)
	}
	var stem string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(stem,'') FROM question WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, questionID).Scan(&stem); err != nil || stem != "" {
		t.Fatalf("blocked PostgreSQL import mutated canonical question: stem=%q err=%v", stem, err)
	}

	valid := postgresPaperImportDraft("1.", "short_answer", 10)
	valid.Solution = &paper.SolutionInput{RawText: "先列式，再计算", Steps: []paper.SolutionStep{{StepNo: 1, Content: "列式"}}}
	valid.HumanConfirmedFields = []string{"question_no", "question_type", "score", "stem", "answer", "solution", "rubric"}
	if _, err := store.SavePaperImportReview(ctx, tenantID, job.ID, userID, paper.ReviewPaperImportInput{ExpectedGeneration: job.Generation, Questions: []paper.PaperImportDraftQuestion{valid}}); err != nil {
		t.Fatalf("preview corrected candidate: %v", err)
	}
	if _, err := store.ApplyPaperImport(ctx, tenantID, job.ID, userID); err != nil {
		t.Fatalf("corrected PostgreSQL import did not apply: %v", err)
	}
	var appliedRunStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM paper_import_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, job.RunID).Scan(&appliedRunStatus); err != nil || appliedRunStatus != "applied" {
		t.Fatalf("applying import did not complete the owning run: status=%q err=%v", appliedRunStatus, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(stem,'') FROM question WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, questionID).Scan(&stem); err != nil || stem != "Imported 1." {
		t.Fatalf("corrected PostgreSQL import was not persisted: stem=%q err=%v", stem, err)
	}
	var appliedImportID, solutionText string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(q.paper_import_id::text,''),s.raw_text FROM question q JOIN question_solution s ON s.tenant_id=q.tenant_id AND s.question_id=q.id AND s.deleted_at IS NULL WHERE q.tenant_id=$1::uuid AND q.id=$2::uuid`, tenantID, questionID).Scan(&appliedImportID, &solutionText); err != nil || appliedImportID != job.ID || solutionText != "先列式，再计算" {
		t.Fatalf("formal provenance/solution missing: import=%q solution=%q err=%v", appliedImportID, solutionText, err)
	}

	reorderJob, err := store.CreatePaperImport(ctx, tenantID, examID, userID, paper.CreatePaperImportInput{Subject: "mathematics", Sources: []paper.CreatePaperImportSourceInput{
		{FileAssetID: paperFileID, DocumentIndex: 0, RoleHint: "auto"},
		{FileAssetID: answerFileID, DocumentIndex: 1, RoleHint: "auto"},
	}})
	if err != nil {
		t.Fatalf("create source reorder fixture: %v", err)
	}
	if _, err = store.FailPaperImport(ctx, tenantID, reorderJob.ID, "source_fixture_failed", []string{"fixture"}); err != nil {
		t.Fatalf("fail source reorder fixture through the versioned run: %v", err)
	}
	reordered, err := store.ReplacePaperImportSources(ctx, tenantID, reorderJob.ID, userID, paper.ReplacePaperImportSourcesInput{ExpectedGeneration: reorderJob.Generation, Sources: []paper.ReplacePaperImportSourceInput{
		{ID: reorderJob.Sources[1].ID, DocumentIndex: 0, RoleHint: "answer"},
		{ID: reorderJob.Sources[0].ID, DocumentIndex: 1, RoleHint: "question"},
	}})
	if err != nil {
		t.Fatalf("replace ordered sources against partial unique index: %v", err)
	}
	if len(reordered.Sources) != 2 || reordered.Sources[0].FileAssetID != answerFileID || reordered.Sources[1].FileAssetID != paperFileID {
		t.Fatalf("PostgreSQL source order was not preserved: %#v", reordered.Sources)
	}
	var errorCode string
	if err := db.QueryRowContext(ctx, `SELECT error_code FROM paper_import_job WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, reorderJob.ID).Scan(&errorCode); err != nil || errorCode != "" {
		t.Fatalf("replacing sources must clear error_code to empty string: %q (%v)", errorCode, err)
	}

	t.Run("OCR completion atomically hands off to restart-safe parse", func(t *testing.T) {
		pageContent := []byte("synthetic paper import page")
		pageHash := fmt.Sprintf("%x", sha256.Sum256(pageContent))
		fileStore := files.NewPostgresStore(db)
		pageAsset, createErr := fileStore.Create(ctx, files.CreateAssetInput{
			TenantID: tenantID, ExamID: examID, OwnerType: "exam", OwnerID: examID,
			OriginalName: "paper-page.png", ContentType: "image/png", SizeBytes: int64(len(pageContent)), HashSHA256: pageHash,
			StorageBucket: "paper-import-e2e", StorageKey: "paper-page.png", Visibility: "tenant", UploadedBy: userID,
		})
		if createErr != nil {
			t.Fatalf("create parsed page asset: %v", createErr)
		}
		objectStore := files.NewMemoryObjectStorage()
		if putErr := objectStore.Put(ctx, pageAsset.StorageBucket, pageAsset.StorageKey, bytes.NewReader(pageContent), int64(len(pageContent)), pageAsset.ContentType); putErr != nil {
			t.Fatalf("store parsed page object: %v", putErr)
		}
		parseJob, createErr := store.CreatePaperImport(ctx, tenantID, examID, userID, paper.CreatePaperImportInput{
			Subject: "mathematics", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: paperFileID, DocumentIndex: 0, RoleHint: "question"}},
		})
		if createErr != nil {
			t.Fatalf("create parse recovery fixture: %v", createErr)
		}
		ocrTask, createErr := runtimeStore.CreateTask(ctx, tenantID, userID, workerruntime.CreateTaskInput{
			TaskType: "ocr", QueueName: "ocr", SourceType: "paper_import_job", SourceID: parseJob.ID,
			IdempotencyKey: "paper-parse-recovery-ocr:" + parseJob.ID, PayloadSchemaVersion: "test-v1",
			Payload: map[string]any{"pages": []paper.PaperImportDecodedPage{{
				SourceID: parseJob.Sources[0].ID, DocumentIndex: 0, PageNo: 1, FileAssetID: pageAsset.ID, SHA256: pageHash,
			}}},
		})
		if createErr != nil {
			t.Fatalf("create OCR runtime fixture: %v", createErr)
		}
		if _, bindErr := db.ExecContext(ctx, `UPDATE agent_worker_task SET paper_import_run_id=$3::uuid,paper_import_generation=$4,task_protocol_version=2 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, ocrTask.ID, parseJob.RunID, parseJob.Generation); bindErr != nil {
			t.Fatalf("bind OCR runtime fixture to import run: %v", bindErr)
		}
		if _, bindErr := db.ExecContext(ctx, `UPDATE paper_import_run SET dispatch_status='queued',initial_task_id=$2::uuid WHERE tenant_id=$1::uuid AND id=$3::uuid`, tenantID, ocrTask.ID, parseJob.RunID); bindErr != nil {
			t.Fatalf("mark import dispatch queued: %v", bindErr)
		}
		claimed, claimErr := runtimeStore.Claim(ctx, tenantID, workerruntime.ClaimInput{
			QueueName: "ocr", WorkerService: "ocr-worker", WorkerInstanceID: "recovery-test", Limit: 1, LeaseSeconds: 300,
		})
		if claimErr != nil || len(claimed) != 1 || claimed[0].ID != ocrTask.ID {
			t.Fatalf("claim OCR runtime fixture: %#v %v", claimed, claimErr)
		}
		block := paper.PaperImportOCRBlock{SourceID: parseJob.Sources[0].ID, DocumentIndex: 0, PageNo: 1, BlockID: "b1", Text: "1. What is 6 x 7?", Confidence: .99}
		parseInput := paper.PaperImportParseRequest{Documents: []paper.PaperImportParseDocument{{
			SourceID: parseJob.Sources[0].ID, FileAssetID: paperFileID, DocumentIndex: 0, RoleHint: "question", Content: block.Text, Blocks: []paper.PaperImportOCRBlock{block},
		}}}
		if completeErr := store.CompletePaperImportOCR(ctx, tenantID, parseJob.ID, paper.PaperImportOCRResult{
			TaskID: claimed[0].ID, LeaseToken: claimed[0].LeaseToken, DurationMS: 10, Blocks: []paper.PaperImportOCRBlock{block},
		}, parseInput); completeErr != nil {
			t.Fatalf("complete OCR and queue parse: %v", completeErr)
		}
		var succeededOCR, queuedParse int
		if queryErr := db.QueryRowContext(ctx, `SELECT
		  count(*) FILTER (WHERE task_type='ocr' AND status='succeeded'),
		  count(*) FILTER (WHERE task_type='paper_parse' AND status='queued')
		FROM agent_worker_task WHERE tenant_id=$1::uuid AND
		  ((source_type='paper_import_job' AND source_id=$2::uuid) OR
		   (source_type='paper_import_parse' AND source_id IN
		     (SELECT id FROM paper_import_parse_input WHERE tenant_id=$1::uuid AND paper_import_id=$2::uuid)))`, tenantID, parseJob.ID).Scan(&succeededOCR, &queuedParse); queryErr != nil || succeededOCR != 1 || queuedParse != 1 {
			t.Fatalf("OCR/parse handoff was not atomic: ocr=%d parse=%d err=%v", succeededOCR, queuedParse, queryErr)
		}

		parser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode parser request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"documents":[],"question_candidates":[{"candidate_id":"q-recovered","question_no_raw":"1","question_type":"short_answer","stem":"What is 6 x 7?","score":1,"confidence":0.99}],"answer_candidates":[],"solution_candidates":[],"rubric_candidates":[],"issues":[]}`))
		}))
		defer parser.Close()
		if _, updateErr := db.ExecContext(ctx, `UPDATE paper_import_run SET dispatch_status='not_required' WHERE tenant_id=$1::uuid AND dispatch_status='pending'`, tenantID); updateErr != nil {
			t.Fatalf("close direct-store dispatch fixtures: %v", updateErr)
		}
		// Constructing a new service/executor models the gateway process restarting
		// after the OCR callback committed and before parsing began.
		restartedService := paper.NewDocumentImportService(store, fileStore, objectStore, parser.URL, strings.Repeat("t", 32), time.Minute)
		executor, executorErr := paper.NewParseTaskExecutor(restartedService, runtimeStore, time.Minute)
		if executorErr != nil {
			t.Fatalf("create restarted parse executor: %v", executorErr)
		}
		var parseTaskID string
		if queryErr := db.QueryRowContext(ctx, `SELECT task.id::text FROM agent_worker_task task JOIN paper_import_parse_input input ON input.tenant_id=task.tenant_id AND input.id=task.source_id WHERE task.tenant_id=$1::uuid AND input.paper_import_id=$2::uuid AND task.task_type='paper_parse'`, tenantID, parseJob.ID).Scan(&parseTaskID); queryErr != nil {
			t.Fatalf("load parse task: %v", queryErr)
		}
		if _, triggerErr := db.ExecContext(ctx, `CREATE FUNCTION fail_test_paper_parse_publish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.status='processing' AND NEW.status='review_required' THEN RAISE EXCEPTION 'injected parse publish failure'; END IF; RETURN NEW; END $$`); triggerErr != nil {
			t.Fatalf("install parse commit failure injection: %v", triggerErr)
		}
		if _, triggerErr := db.ExecContext(ctx, `CREATE TRIGGER fail_test_paper_parse_publish BEFORE UPDATE ON paper_import_job FOR EACH ROW EXECUTE FUNCTION fail_test_paper_parse_publish()`); triggerErr != nil {
			t.Fatalf("install parse commit failure trigger: %v", triggerErr)
		}
		worked, runErr := executor.RunOnce(ctx)
		if runErr == nil || !worked {
			t.Fatalf("injected parse commit must fail after doing work: worked=%v err=%v", worked, runErr)
		}
		var taskStatus, jobStatus string
		var candidateCount int
		if queryErr := db.QueryRowContext(ctx, `SELECT task.status,job.status,jsonb_array_length(job.question_candidates) FROM agent_worker_task task CROSS JOIN paper_import_job job WHERE task.tenant_id=$1::uuid AND task.id=$2::uuid AND job.tenant_id=$1::uuid AND job.id=$3::uuid`, tenantID, parseTaskID, parseJob.ID).Scan(&taskStatus, &jobStatus, &candidateCount); queryErr != nil {
			t.Fatalf("inspect rolled back parse commit: %v", queryErr)
		}
		if taskStatus != "running" || jobStatus != "processing" || candidateCount != 0 {
			t.Fatalf("parse commit was partially published: task=%s job=%s candidates=%d", taskStatus, jobStatus, candidateCount)
		}
		if _, dropErr := db.ExecContext(ctx, `DROP TRIGGER fail_test_paper_parse_publish ON paper_import_job`); dropErr != nil {
			t.Fatalf("remove parse commit failure injection: %v", dropErr)
		}
		if _, dropErr := db.ExecContext(ctx, `DROP FUNCTION fail_test_paper_parse_publish()`); dropErr != nil {
			t.Fatalf("remove parse commit failure function: %v", dropErr)
		}
		if _, expireErr := db.ExecContext(ctx, `UPDATE agent_worker_task SET lease_expires_at=now()-interval '1 second' WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, parseTaskID); expireErr != nil {
			t.Fatalf("expire failed parse attempt: %v", expireErr)
		}
		worked, runErr = executor.RunOnce(ctx)
		if runErr != nil || !worked {
			t.Fatalf("resume durable parse task after rollback: worked=%v err=%v", worked, runErr)
		}
		recovered, getErr := store.GetPaperImport(ctx, tenantID, parseJob.ID)
		if getErr != nil || recovered.Status != "review_required" || len(recovered.QuestionCandidates) != 1 || recovered.QuestionCandidates[0].CandidateID != "q-recovered" {
			t.Fatalf("recovered parse did not persist one candidate: %#v err=%v", recovered, getErr)
		}
		var replayLeaseToken, replayInputID, replayRunID, replayRevision, replayInputHash string
		var replayGeneration int64
		if queryErr := db.QueryRowContext(ctx, `SELECT task.lease_token,input.id::text,input.run_id::text,input.generation,input.source_revision,input.input_hash
FROM agent_worker_task task JOIN paper_import_parse_input input ON input.tenant_id=task.tenant_id AND input.id=task.source_id
WHERE task.tenant_id=$1::uuid AND task.id=$2::uuid`, tenantID, parseTaskID).Scan(&replayLeaseToken, &replayInputID, &replayRunID, &replayGeneration, &replayRevision, &replayInputHash); queryErr != nil {
			t.Fatalf("load parse result receipt: %v", queryErr)
		}
		score := 1.0
		parseResult := paper.PaperImportParseResult{
			Documents:          []paper.PaperImportDetectedDocument{},
			QuestionCandidates: []paper.QuestionCandidate{{CandidateID: "q-recovered", QuestionNoRaw: "1", QuestionType: "short_answer", Stem: "What is 6 x 7?", Score: &score, Confidence: .99}},
			AnswerCandidates:   []paper.AnswerCandidate{}, SolutionCandidates: []paper.SolutionCandidate{}, RubricCandidates: []paper.RubricCandidate{}, Issues: []paper.PaperImportIssue{},
		}
		binding := paper.PaperImportRunBinding{ImportID: parseJob.ID, RunID: replayRunID, Generation: replayGeneration, SourceRevision: replayRevision, InputID: replayInputID, InputHash: replayInputHash}
		if replayedResult, replayErr := store.CompletePaperImportParseTask(ctx, tenantID, parseTaskID, replayLeaseToken, binding, parseResult, 1); replayErr != nil || replayedResult.ResultGeneration != parseJob.Generation || len(replayedResult.QuestionCandidates) != 1 {
			t.Fatalf("identical parse callback did not replay original receipt: %#v err=%v", replayedResult, replayErr)
		}
		changedResult := parseResult
		changedResult.QuestionCandidates = append([]paper.QuestionCandidate{}, parseResult.QuestionCandidates...)
		changedResult.QuestionCandidates[0].Stem = "changed result must conflict"
		if _, replayErr := store.CompletePaperImportParseTask(ctx, tenantID, parseTaskID, replayLeaseToken, binding, changedResult, 1); !errors.Is(replayErr, paper.ErrConflict) {
			t.Fatalf("changed parse callback replay error=%v", replayErr)
		}
		worked, runErr = executor.RunOnce(ctx)
		if runErr != nil || worked {
			t.Fatalf("completed parse task must not be claimed twice: worked=%v err=%v", worked, runErr)
		}
		// A human save and an explicit new parse generation share the same job
		// lock and expected_generation boundary. If the save wins, its confirmed
		// field is retained into the new run; if the rerun wins, the stale save is
		// rejected instead of overwriting generation 2.
		humanDrafts := append([]paper.PaperImportDraftQuestion{}, recovered.Questions...)
		humanDrafts[0].Stem = "Human-confirmed concurrent stem"
		humanDrafts[0].HumanConfirmedFields = append(humanDrafts[0].HumanConfirmedFields, "stem")
		startRace := make(chan struct{})
		type importResult struct {
			job paper.PaperImportJob
			err error
		}
		reviewResult := make(chan importResult, 1)
		replaceResult := make(chan importResult, 1)
		go func() {
			<-startRace
			job, saveErr := store.SavePaperImportReview(ctx, tenantID, parseJob.ID, userID, paper.ReviewPaperImportInput{ExpectedGeneration: recovered.Generation, Questions: humanDrafts})
			reviewResult <- importResult{job: job, err: saveErr}
		}()
		go func() {
			<-startRace
			job, replaceErr := store.ReplacePaperImportSources(ctx, tenantID, parseJob.ID, userID, paper.ReplacePaperImportSourcesInput{
				CommandID: "historical-result-replay", ExpectedGeneration: recovered.Generation,
				Sources: []paper.ReplacePaperImportSourceInput{{ID: recovered.Sources[0].ID, DocumentIndex: 0, RoleHint: "question"}},
			})
			replaceResult <- importResult{job: job, err: replaceErr}
		}()
		close(startRace)
		reviewOutcome, replaceOutcome := <-reviewResult, <-replaceResult
		newRun, replaceErr := replaceOutcome.job, replaceOutcome.err
		if replaceErr != nil || newRun.Generation != recovered.Generation+1 {
			t.Fatalf("create successor run for historical replay: %#v err=%v", newRun, replaceErr)
		}
		if reviewOutcome.err != nil && !errors.Is(reviewOutcome.err, paper.ErrConflict) {
			t.Fatalf("concurrent human review returned invalid outcome: %v", reviewOutcome.err)
		}
		if reviewOutcome.err == nil {
			if len(newRun.Questions) != 1 || newRun.Questions[0].Stem != "Human-confirmed concurrent stem" {
				t.Fatalf("accepted human confirmation was lost by concurrent rerun: %#v", newRun.Questions)
			}
		} else if reviewOutcome.job.ID != "" {
			t.Fatalf("rejected stale review returned a misleading result: %#v", reviewOutcome.job)
		}
		if currentAfterReplay, replayErr := store.CompletePaperImportParseTask(ctx, tenantID, parseTaskID, replayLeaseToken, binding, parseResult, 1); replayErr != nil || currentAfterReplay.Generation != newRun.Generation || currentAfterReplay.Status != "processing" {
			t.Fatalf("historical exact result was not acknowledged without rewriting current run: %#v err=%v", currentAfterReplay, replayErr)
		}
		if _, replayErr := store.CompletePaperImportParseTask(ctx, tenantID, parseTaskID, replayLeaseToken, binding, changedResult, 1); !errors.Is(replayErr, paper.ErrConflict) {
			t.Fatalf("changed historical parse callback replay error=%v", replayErr)
		}
		wrongHistoricalInput := binding
		wrongHistoricalInput.InputHash = "wrong-historical-input-hash"
		if _, replayErr := store.CompletePaperImportParseTask(ctx, tenantID, parseTaskID, replayLeaseToken, wrongHistoricalInput, parseResult, 1); !errors.Is(replayErr, paper.ErrConflict) {
			t.Fatalf("historical replay accepted mismatched input hash: %v", replayErr)
		}
	})
}

func postgresPaperImportDraft(number, kind string, score float64) paper.PaperImportDraftQuestion {
	return paper.PaperImportDraftQuestion{
		QuestionNo: number, QuestionType: kind, Score: score, Stem: "Imported " + number,
		AnswerKey: &paper.AnswerKeyInput{StandardAnswer: "42", EquivalentAnswers: []any{"42"}, Tolerance: map[string]any{}},
		Rubric: &paper.RubricInput{Status: "draft", MaxScore: score, Points: []paper.RubricPoint{
			{ID: "p1", Description: "correct", Score: score, Required: true},
		}},
		Confidence: .99, Issues: []string{},
	}
}
