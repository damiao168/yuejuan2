package server

import (
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// 相同回调重放原收据，变更候选冲突，已完成任务不能再被执行器领取。
func e2eAssertPaperParseExactReceipt(t *testing.T, recovery paperParseRecovery) (string, paper.PaperImportRunBinding, paper.PaperImportParseResult, paper.PaperImportParseResult) {
	t.Helper()
	ctx, db, store, tenantID := recovery.Gate.Context, recovery.Gate.DB, recovery.Gate.Store, recovery.Gate.TenantID
	parseJob, executor, parseTaskID := recovery.Job, recovery.Executor, recovery.TaskID
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
	worked, runErr := executor.RunOnce(ctx)
	if runErr != nil || worked {
		t.Fatalf("completed parse task must not be claimed twice: worked=%v err=%v", worked, runErr)
	}
	return replayLeaseToken, binding, parseResult, changedResult
}

// 人工确认和新代次使用原开始屏障；先接受的人工字段保留，过时保存只允许明确冲突。
func e2eRacePaperHumanReviewAndSuccessor(t *testing.T, recovery paperParseRecovery) paper.PaperImportJob {
	t.Helper()
	ctx, store, tenantID, userID := recovery.Gate.Context, recovery.Gate.Store, recovery.Gate.TenantID, recovery.Gate.UserID
	parseJob, recovered := recovery.Job, recovery.Recovered
	// 人工保存与显式新建解析代次共用作业锁，同时核对资料代次和已读取的内容版本。
	// 若保存先完成，其已确认字段保留到新运行；若重新运行先完成，
	// 过时保存会被拒绝，不会覆盖第 2 代。
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
		job, saveErr := store.SavePaperImportReview(ctx, tenantID, parseJob.ID, userID, paper.ReviewPaperImportInput{ExpectedGeneration: recovered.Generation, ExpectedUpdatedAt: recovered.UpdatedAt.Format(time.RFC3339Nano), Questions: humanDrafts})
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
	return newRun
}

// 旧代次准确收据可被确认但不能改写新运行，结果或输入哈希改变仍拒绝。
func e2eAssertHistoricalPaperReceiptFenced(t *testing.T, recovery paperParseRecovery, newRun paper.PaperImportJob) {
	t.Helper()
	ctx, store, tenantID := recovery.Gate.Context, recovery.Gate.Store, recovery.Gate.TenantID
	parseTaskID, replayLeaseToken, binding, parseResult, changedResult := recovery.TaskID, recovery.ReplayLeaseToken, recovery.Binding, recovery.Result, recovery.ChangedResult
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

}

// 无效候选允许成为复核建议但应用门禁必须拒绝，正式题干保持原值。
func e2eAssertInvalidPaperImportGate(t *testing.T, scenario paperApplyGateScenario) {
	t.Helper()
	ctx, db, store, tenantID, userID, questionID, job, claimedParse, binding := scenario.Context, scenario.DB, scenario.Store, scenario.TenantID, scenario.UserID, scenario.QuestionID, scenario.Job, scenario.ClaimedParse, scenario.Binding
	invalidScore := 10.0
	if _, err := store.CompletePaperImportParseTask(ctx, tenantID, claimedParse[0].ID, claimedParse[0].LeaseToken, binding, paper.PaperImportParseResult{
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

}

// 人工确认只推进同代次数据库版本，先记录原已发布规则再应用，保证替换顺序。
func e2eReviewAndApplyPaperImport(t *testing.T, scenario paperApplyGateScenario) string {
	t.Helper()
	ctx, db, store, tenantID, userID, examID, questionID, job := scenario.Context, scenario.DB, scenario.Store, scenario.TenantID, scenario.UserID, scenario.ExamID, scenario.QuestionID, scenario.Job
	valid := postgresPaperImportDraft("1.", "short_answer", 10)
	valid.Solution = &paper.SolutionInput{RawText: "先列式，再计算", Steps: []paper.SolutionStep{{StepNo: 1, Content: "列式"}}}
	valid.HumanConfirmedFields = []string{"question_no", "question_type", "score", "stem", "answer", "solution", "rubric"}
	beforeReview, err := store.GetPaperImport(ctx, tenantID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := store.SavePaperImportReview(ctx, tenantID, job.ID, userID, paper.ReviewPaperImportInput{ExpectedGeneration: beforeReview.Generation, ExpectedUpdatedAt: beforeReview.UpdatedAt.Format(time.RFC3339Nano), Questions: []paper.PaperImportDraftQuestion{valid}})
	if err != nil {
		t.Fatalf("preview corrected candidate: %v", err)
	}
	if reviewed.Generation != beforeReview.Generation || reviewed.UpdatedAt.Equal(beforeReview.UpdatedAt) {
		t.Fatalf("review save must change the DB-backed review version within one generation: before=%d/%s after=%d/%s", beforeReview.Generation, beforeReview.UpdatedAt, reviewed.Generation, reviewed.UpdatedAt)
	}
	// 回执必须携带普通详情的运行、候选和来源信息；完整对比可发现只补 Generation 而遗漏其他装载字段的回归。
	persistedReview, err := store.GetPaperImport(ctx, tenantID, job.ID)
	if err != nil {
		t.Fatalf("read persisted review receipt: %v", err)
	}
	reviewJSON, err := json.Marshal(reviewed)
	if err != nil {
		t.Fatal(err)
	}
	persistedJSON, err := json.Marshal(persistedReview)
	if err != nil || string(reviewJSON) != string(persistedJSON) {
		t.Fatalf("review transaction receipt lost detail fields: receipt=%s persisted=%s err=%v", reviewJSON, persistedJSON, err)
	}
	if _, err := store.SavePaperImportReview(ctx, tenantID, job.ID, userID, paper.ReviewPaperImportInput{ExpectedGeneration: beforeReview.Generation, ExpectedUpdatedAt: beforeReview.UpdatedAt.Format(time.RFC3339Nano), Questions: []paper.PaperImportDraftQuestion{valid}}); !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("stale review version must remain rejected after returning complete receipt: %v", err)
	}
	var previousRuleID string
	if err := db.QueryRowContext(ctx, `INSERT INTO scoring_rule(tenant_id,exam_id,question_id,version,rule_type,config,status,revision,content_hash,created_by,published_by,published_at)
VALUES($1::uuid,$2::uuid,$3::uuid,1,'manual','{}'::jsonb,'published',1,$4,$5::uuid,$5::uuid,now()) RETURNING id::text`, tenantID, examID, questionID, strings.Repeat("a", 64), userID).Scan(&previousRuleID); err != nil {
		t.Fatalf("seed previously published scoring rule: %v", err)
	}
	if _, err := store.ApplyPaperImport(ctx, tenantID, job.ID, userID); err != nil {
		t.Fatalf("corrected PostgreSQL import did not apply: %v", err)
	}
	return previousRuleID
}

// 应用后旧规则退役、所属运行完成，正式题目及解答保留本次导入来源。
func e2eAssertAppliedPaperCanonicalFacts(t *testing.T, scenario paperApplyGateScenario, previousRuleID string) {
	t.Helper()
	ctx, db, tenantID, questionID, job := scenario.Context, scenario.DB, scenario.TenantID, scenario.QuestionID, scenario.Job
	var stem string
	var previousRuleStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM scoring_rule WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, previousRuleID).Scan(&previousRuleStatus); err != nil || previousRuleStatus != "retired" {
		t.Fatalf("old published scoring rule remained active after question import: status=%q err=%v", previousRuleStatus, err)
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

}

// 版本化失败后重排两份来源，部分唯一索引不改变用户指定顺序并清除旧错误码。
func e2eAssertPaperSourceReorder(t *testing.T, scenario paperApplyGateScenario) {
	t.Helper()
	ctx, db, store, tenantID, userID, examID, paperFileID, answerFileID := scenario.Context, scenario.DB, scenario.Store, scenario.TenantID, scenario.UserID, scenario.ExamID, scenario.PaperFileID, scenario.AnswerFileID
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
