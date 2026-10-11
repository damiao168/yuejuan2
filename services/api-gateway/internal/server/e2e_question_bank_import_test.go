package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/questionbank"
	"github.com/google/uuid"
)

func TestE2EPostgresQuestionBankFrozenImport(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for real PostgreSQL evidence")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "teacher"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, token, "069d")
	ctx := context.Background()
	scope := auth.AccessScope{TenantID: fixture.TenantID, ActorID: fixture.AdminID, TenantWide: true}
	store := questionbank.NewPostgresStore(db)
	bank, err := store.CreateBank(commandreceipt.WithID(ctx, "069d-bank"), scope, questionbank.CreateBankInput{SchoolID: fixture.SchoolID, Name: "Selected historical questions"})
	if err != nil {
		t.Fatal(err)
	}
	env := questionBankE2EEnvironment{ctx: ctx, db: db, store: store, scope: scope, router: router, token: token}
	binding := prepareFrozenQuestionBankImportSource(t, env, fixture, bank)
	result := assertFrozenQuestionBankImportReceiptAndProvenance(t, env, fixture, binding)
	linkedResult := assertFrozenQuestionBankImportDeduplication(t, env, binding, result)
	assertFrozenQuestionBankImportPartialRecovery(t, env, fixture, bank, binding)
	assertFrozenQuestionBankImportPublicationReuse(t, env, fixture, bank, binding, linkedResult)
	assertFrozenQuestionBankImportRevokedReplay(t, env, fixture, bank, binding)

}

// 输入保留最初冻结快照和命令身份；后续重试、拒绝与导出不可改读实时试卷定义。
type frozenQuestionBankImportBinding struct {
	snapshotID, questionID, path, sourceBefore string
	input                                      questionbank.ImportQuestionInput
	selection                                  questionbank.ImportPreviewSelection
	preview                                    *questionbank.ImportPreview
}

func prepareFrozenQuestionBankImportSource(t *testing.T, env questionBankE2EEnvironment, fixture story056AcceptanceFixture, bank questionbank.Bank) frozenQuestionBankImportBinding {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	scope := env.scope
	var err error
	var snapshotID string
	var sourceRaw []byte
	if err = db.QueryRow(`SELECT id::text,import_snapshot_json FROM exam_readiness_snapshot WHERE tenant_id=$1 AND exam_id=$2 AND status='passed'`, fixture.TenantID, fixture.ExamID).Scan(&snapshotID, &sourceRaw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"candidate_ids", "answer_area", "student_id", "identity_regions"} {
		if strings.Contains(string(sourceRaw), forbidden) {
			t.Fatal("source companion contains " + forbidden)
		}
	}
	questionID := fixture.QuestionIDs["true_false"]
	selection := questionbank.ImportPreviewSelection{QuestionID: questionID, TargetBankID: bank.ID, SourceSnapshotID: snapshotID, Mapping: questionbank.ImportMapping{ItemCode: "SELECTED-1"}}
	preview, err := store.PreviewImports(ctx, scope, questionbank.ImportPreviewInput{Selections: []questionbank.ImportPreviewSelection{selection}})
	if err != nil || len(preview.Items) != 1 || preview.Items[0].Preview == nil || !preview.Items[0].Preview.Available {
		t.Fatalf("new confirmation did not freeze usable content: %+v %v", preview, err)
	}
	p := preview.Items[0].Preview
	input := questionbank.ImportQuestionInput{TargetBankID: bank.ID, SourceSnapshotID: snapshotID, AssessmentSnapshotID: p.Source.AssessmentSnapshotID, ScoringSource: "original_exam", DedupDecision: "new_item", ExpectedTargetRevision: bank.Revision, ExpectedTargetSchemaVersion: bank.MetadataSchemaVersion, Mapping: selection.Mapping, CommandID: "069d-import"}
	path := "/api/v1/question-bank/items/import-from-question/" + questionID
	// 正常写入冻结考试定义会被拒绝。仅在此隔离夹具中模拟旧版维护写入，不触碰快照。
	if _, err = db.Exec(`UPDATE question SET stem='Later live stem' WHERE id=$1::uuid`, questionID); err == nil {
		t.Fatal("frozen exam accepted a live definition edit")
	}
	maintenance, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Rollback()
	if _, err = maintenance.Exec(`SET LOCAL session_replication_role='replica'`); err != nil {
		t.Fatal(err)
	}
	if _, err = maintenance.Exec(`UPDATE question SET stem='Later live stem' WHERE id=$1::uuid`, questionID); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Commit(); err != nil {
		t.Fatal(err)
	}
	sourceQuestions, err := paper.NewPostgresStore(db).ListQuestions(ctx, fixture.TenantID, fixture.ExamID)
	if err != nil {
		t.Fatal(err)
	}
	sourceBefore := qbJSON(t, sourceQuestions)
	updatedPreview, err := store.PreviewImports(ctx, scope, questionbank.ImportPreviewInput{Selections: []questionbank.ImportPreviewSelection{selection}})
	if err != nil || updatedPreview.Items[0].Preview == nil || updatedPreview.Items[0].Preview.ContentHash != p.ContentHash || updatedPreview.Items[0].Preview.Content.Stem != p.Content.Stem {
		t.Fatalf("live changed source: %+v %v", updatedPreview, err)
	}

	return frozenQuestionBankImportBinding{snapshotID: snapshotID, questionID: questionID, path: path, sourceBefore: sourceBefore, input: input, selection: selection, preview: p}

}

func assertFrozenQuestionBankImportReceiptAndProvenance(t *testing.T, env questionBankE2EEnvironment, fixture story056AcceptanceFixture, binding frozenQuestionBankImportBinding) questionbank.ImportResult {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	scope := env.scope
	router := env.router
	token := env.token
	snapshotID := binding.snapshotID
	questionID := binding.questionID
	path := binding.path
	input := binding.input
	selection := binding.selection
	p := binding.preview
	var err error
	created := qbRequest(t, router, questionBankRequest{Method: http.MethodPost, Path: path, Token: token, Key: input.CommandID, Body: qbJSON(t, input), Want: 201})
	var result questionbank.ImportResult
	encoded, _ := json.Marshal(created)
	if err = json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if result.Version.WorkflowStatus != "draft" || result.Version.Stem != p.Content.Stem || result.Version.Metadata.Copyright != "unknown" {
		t.Fatal("import changed frozen facts or published")
	}
	qbRequest(t, e2ePostgresRouter(db), questionBankRequest{Method: http.MethodPost, Path: path, Token: token, Key: input.CommandID, Body: qbJSON(t, input), Want: 201})
	changed := input
	changed.Mapping.Copyright = "owned"
	qbRequest(t, router, questionBankRequest{Method: http.MethodPost, Path: path, Token: token, Key: input.CommandID, Body: qbJSON(t, changed), Want: 409})
	qbRequest(t, router, questionBankRequest{Method: http.MethodPost, Path: path, Token: token, Key: "different-key", Body: qbJSON(t, input), Want: 400})
	qbRequest(t, router, questionBankRequest{Method: http.MethodPost, Path: path, Token: token, Key: "self-hash", Body: strings.TrimSuffix(qbJSON(t, input), "}") + `,"source_snapshot_hash":"` + strings.Repeat("a", 64) + `"}`, Want: 400})
	if _, err = store.Transition(commandreceipt.WithID(ctx, "069d-block-submit"), scope, result.Version.ID, "submit-review", questionbank.ReviewInput{ExpectedRevision: result.Version.Revision, BundleHash: result.Version.BundleHash}); err == nil {
		t.Fatal("unmapped copyright draft could submit")
	}
	reloaded, err := store.GetVersion(ctx, scope, result.Version.ID)
	if err != nil || reloaded.ImportProvenance == nil || reloaded.ImportProvenance.Source.ReadinessSnapshotID != snapshotID {
		t.Fatal("provenance was not recoverable")
	}
	// 既有仅含哈希的准备度记录不凭空生成冻结内容。
	var oldID string
	if err = db.QueryRow(`INSERT INTO exam_readiness_snapshot(tenant_id,exam_id,configuration_hash,status,checks,confirmed_by) VALUES($1,$2,$3,'invalidated','[]',$4) RETURNING id::text`, fixture.TenantID, fixture.ExamID, strings.Repeat("b", 64), fixture.AdminID).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	old := selection
	old.SourceSnapshotID = oldID
	oldPreview, err := store.PreviewImports(ctx, scope, questionbank.ImportPreviewInput{Selections: []questionbank.ImportPreviewSelection{old}})
	if err != nil || oldPreview.Items[0].ErrorCode != "source_snapshot_unavailable" {
		t.Fatal("hash-only source was not unavailable")
	}
	wrongAssessment := input
	wrongAssessment.CommandID = "069d-wrong-assessment"
	wrongAssessment.AssessmentSnapshotID = uuid.NewString()
	if _, err = store.ImportQuestion(commandreceipt.WithID(ctx, wrongAssessment.CommandID), scope, questionID, wrongAssessment); !errors.Is(err, questionbank.ErrSourceUnavailable) {
		t.Fatalf("unbound assessment accepted: %v", err)
	}
	for _, statement := range []string{`UPDATE exam_readiness_snapshot SET import_snapshot_json='{}' WHERE id=$1::uuid`, `DELETE FROM exam_readiness_snapshot WHERE id=$1::uuid`} {
		if _, err = db.Exec(statement, snapshotID); err == nil {
			t.Fatal("readiness source was mutable")
		}
	}
	if _, err = db.Exec(`UPDATE question_bank_import SET mapping='{}' WHERE target_version_id=$1::uuid`, result.Version.ID); err == nil {
		t.Fatal("provenance was mutable")
	}

	return result

}

func assertFrozenQuestionBankImportDeduplication(t *testing.T, env questionBankE2EEnvironment, binding frozenQuestionBankImportBinding, result questionbank.ImportResult) questionbank.ImportResult {
	t.Helper()
	ctx := env.ctx
	store := env.store
	scope := env.scope
	questionID := binding.questionID
	input := binding.input
	selection := binding.selection
	var err error
	// 相同资料包、答案改变和分值改变仍分别显示为不同提示。
	dedupPreview, _ := store.PreviewImports(ctx, scope, questionbank.ImportPreviewInput{Selections: []questionbank.ImportPreviewSelection{selection}})
	if len(dedupPreview.Items[0].Preview.DuplicateHints) != 1 || dedupPreview.Items[0].Preview.DuplicateHints[0].Kind != "exact_bundle" {
		t.Fatal("exact bundle hint missing")
	}
	modified := result.Version.Scoring
	modified.Answer.StandardAnswer = false
	if _, err = store.UpdateScoring(commandreceipt.WithID(ctx, "069d-answer-change"), scope, result.Version.ID, questionbank.UpdateScoringInput{ExpectedRevision: result.Version.Revision, Scoring: modified}); err != nil {
		t.Fatal(err)
	}
	dedupPreview, _ = store.PreviewImports(ctx, scope, questionbank.ImportPreviewInput{Selections: []questionbank.ImportPreviewSelection{selection}})
	if dedupPreview.Items[0].Preview.DuplicateHints[0].Kind != "same_content_different_scoring" {
		t.Fatal("different answer hint missing")
	}
	linked := input
	linked.DedupDecision = "new_version"
	linked.ExistingItemID = result.Item.ID
	linked.Mapping.ItemCode = ""
	linked.CommandID = "069d-linked"
	linkedResult, err := store.ImportQuestion(commandreceipt.WithID(ctx, linked.CommandID), scope, questionID, linked)
	if err != nil || linkedResult.Item.ID != result.Item.ID || linkedResult.Version.VersionNo != 2 || linkedResult.Provenance.LinkedItemID != result.Item.ID {
		t.Fatalf("explicit new version: %+v %v", linkedResult, err)
	}

	return linkedResult

}

func assertFrozenQuestionBankImportPartialRecovery(t *testing.T, env questionBankE2EEnvironment, fixture story056AcceptanceFixture, bank questionbank.Bank, binding frozenQuestionBankImportBinding) {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	scope := env.scope
	router := env.router
	token := env.token
	snapshotID := binding.snapshotID
	questionID := binding.questionID
	input := binding.input
	var err error
	// 单条失败后，另一条及其收据仍已提交。重试使用完全相同的原始载荷和命令身份。
	secondID := fixture.QuestionIDs["single_choice"]
	secondSel := questionbank.ImportPreviewSelection{QuestionID: secondID, TargetBankID: bank.ID, SourceSnapshotID: snapshotID, Mapping: questionbank.ImportMapping{ItemCode: "SELECTED-2", GradeScope: "grade_10", Copyright: "owned", Options: []string{"First", "Second", "Third", "Fourth"}}}
	secondPreview, _ := store.PreviewImports(ctx, scope, questionbank.ImportPreviewInput{Selections: []questionbank.ImportPreviewSelection{secondSel}})
	second := input
	second.CommandID = "069d-second"
	second.Mapping = secondSel.Mapping
	second.AssessmentSnapshotID = secondPreview.Items[0].Preview.Source.AssessmentSnapshotID
	first := input
	first.CommandID = "069d-batch-first"
	first.Mapping.ItemCode = "BATCH-1"
	batch := questionbank.BatchImportInput{Items: []questionbank.BatchImportItem{{QuestionID: questionID, ImportQuestionInput: first}, {QuestionID: secondID, ImportQuestionInput: second}}}
	if _, err = db.Exec(`CREATE FUNCTION qb_import_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='question_bank.import.from_question' AND NEW.payload->>'source_question_id'='` + secondID + `' THEN RAISE EXCEPTION 'synthetic import fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER qb_import_fail BEFORE INSERT ON event_outbox FOR EACH ROW EXECUTE FUNCTION qb_import_fail()`); err != nil {
		t.Fatal(err)
	}
	partial := qbRequest(t, router, questionBankRequest{Method: http.MethodPost, Path: "/api/v1/question-bank/imports/confirm", Token: token, Key: "", Body: qbJSON(t, batch), Want: 207})["items"].([]any)
	if partial[0].(map[string]any)["status"] != "succeeded" || partial[1].(map[string]any)["status"] != "failed" || partial[1].(map[string]any)["retryable"] != true {
		t.Fatal("partial receipt wrong")
	}
	if _, err = db.Exec(`DROP TRIGGER qb_import_fail ON event_outbox; DROP FUNCTION qb_import_fail()`); err != nil {
		t.Fatal(err)
	}
	retry := qbRequest(t, e2ePostgresRouter(db), questionBankRequest{Method: http.MethodPost, Path: "/api/v1/question-bank/imports/confirm", Token: token, Key: "", Body: qbJSON(t, batch), Want: 207})["items"].([]any)
	if retry[1].(map[string]any)["status"] != "succeeded" || retry[0].(map[string]any)["result"].(map[string]any)["version"].(map[string]any)["id"] != partial[0].(map[string]any)["result"].(map[string]any)["version"].(map[string]any)["id"] {
		t.Fatal("partial restart retry duplicated success")
	}

}

func assertFrozenQuestionBankImportPublicationReuse(t *testing.T, env questionBankE2EEnvironment, fixture story056AcceptanceFixture, bank questionbank.Bank, binding frozenQuestionBankImportBinding, linkedResult questionbank.ImportResult) {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	scope := env.scope
	router := env.router
	token := env.token
	snapshotID := binding.snapshotID
	sourceBefore := binding.sourceBefore
	v, classID := publishFrozenQuestionBankImport(t, env, fixture, bank, linkedResult)
	exam := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams", token, `{"school_id":"`+fixture.SchoolID+`","name":"Selected question reuse","subject":"物理","exam_type":"unit","total_score":`+qbJSON(t, v.DefaultScore)+`,"grading_mode":"human_review_required","publish_policy":"after_review","class_ids":["`+classID+`"]}`, 201)["exam"].(map[string]any)
	examID := e2eString(t, exam, "id")
	copied, err := store.Materialize(commandreceipt.WithID(ctx, "069d-reuse"), scope, examID, questionbank.MaterializeInput{ExpectedRevision: 1, Selections: []questionbank.MaterializeSelection{{VersionID: v.ID, QuestionNo: "1", SortOrder: 1}}})
	if err != nil || len(copied.Questions) != 1 || copied.Questions[0].SourceBankItemVersionID != v.ID || copied.Questions[0].AnswerArea != nil {
		t.Fatalf("published import reuse: %+v %v", copied, err)
	}
	provenance, err := store.GetVersion(ctx, scope, copied.Questions[0].SourceBankItemVersionID)
	if err != nil || provenance.ImportProvenance == nil || provenance.ImportProvenance.Source.ReadinessSnapshotID != snapshotID {
		t.Fatal("reused question lost original snapshot provenance")
	}
	sourceAfter, err := paper.NewPostgresStore(db).ListQuestions(ctx, fixture.TenantID, fixture.ExamID)
	if err != nil || sourceBefore != qbJSON(t, sourceAfter) {
		t.Fatal("import or reuse modified source exam")
	}

}

func assertFrozenQuestionBankImportRevokedReplay(t *testing.T, env questionBankE2EEnvironment, fixture story056AcceptanceFixture, bank questionbank.Bank, binding frozenQuestionBankImportBinding) {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	scope := env.scope
	router := env.router
	token := env.token
	questionID := binding.questionID
	path := binding.path
	input := binding.input
	selection := binding.selection
	var err error
	// 来源作用域与目标 ACL 均需检查，包括持久化重放。
	denied := scope
	denied.TenantWide = false
	denied.SchoolIDs = []string{fixture.SchoolID}
	denied.ExamIDs = []string{uuid.NewString()}
	if _, err = store.ImportQuestion(commandreceipt.WithID(ctx, input.CommandID), denied, questionID, input); !errors.Is(err, questionbank.ErrNotFound) {
		t.Fatalf("source-scope replay: %v", err)
	}
	deniedPreview, _ := store.PreviewImports(ctx, denied, questionbank.ImportPreviewInput{Selections: []questionbank.ImportPreviewSelection{selection}})
	if deniedPreview.Items[0].Preview != nil {
		t.Fatal("out-of-scope source leaked preview or counts")
	}
	if _, err = db.Exec(`DELETE FROM question_bank_acl WHERE bank_id=$1::uuid AND user_id=$2::uuid AND action='read'`, bank.ID, fixture.AdminID); err != nil {
		t.Fatal(err)
	}
	qbRequest(t, router, questionBankRequest{Method: http.MethodPost, Path: path, Token: token, Key: input.CommandID, Body: qbJSON(t, input), Want: 404})
	deniedPreview, _ = store.PreviewImports(ctx, scope, questionbank.ImportPreviewInput{Selections: []questionbank.ImportPreviewSelection{selection}})
	if deniedPreview.Items[0].Preview != nil {
		t.Fatal("private bank duplicate hints leaked")
	}
}

func publishFrozenQuestionBankImport(t *testing.T, env questionBankE2EEnvironment, fixture story056AcceptanceFixture, bank questionbank.Bank, linkedResult questionbank.ImportResult) (questionbank.Version, string) {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	scope := env.scope
	var err error
	// 完成映射后，提交和发布由原操作者执行，批准使用独立复核者的范围。
	// 返回发布版本供后续复制检查来源信息，保持状态转换和版本修订顺序。
	reviewer := e2eLookupUserID(t, db, "demo", "teacher")
	var classID string
	if err = db.QueryRow(`SELECT class_id::text FROM exam_class WHERE exam_id=$1 LIMIT 1`, fixture.ExamID).Scan(&classID); err != nil {
		t.Fatal(err)
	}
	if err = org.NewPostgresStore(db).BindTeacherClass(ctx, fixture.TenantID, reviewer, classID); err != nil {
		t.Fatal(err)
	}
	for n, actor := range []string{fixture.AdminID, reviewer} {
		bank, err = store.BindReviewers(commandreceipt.WithID(ctx, "069d-reviewer-"+actor), scope, bank.ID, questionbank.ReviewerBinding{ExpectedRevision: bank.Revision, UserID: actor, Read: true, Review: true, Publish: n == 0})
		if err != nil {
			t.Fatal(err)
		}
	}
	content := linkedResult.Version.Content
	content.Metadata.GradeScope = "grade_10"
	content.Metadata.Copyright = "owned"
	v, err := store.UpdateVersion(commandreceipt.WithID(ctx, "069d-complete-mapping"), scope, linkedResult.Version.ID, questionbank.UpdateVersionInput{ExpectedRevision: linkedResult.Version.Revision, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	reviewerScope := auth.AccessScope{TenantID: fixture.TenantID, ActorID: reviewer, SchoolIDs: []string{fixture.SchoolID}, ClassIDs: []string{classID}}
	for _, action := range []string{"submit-review", "approve", "publish"} {
		actorScope := scope
		if action == "approve" {
			actorScope = reviewerScope
		}
		v, err = store.Transition(commandreceipt.WithID(ctx, "069d-"+action), actorScope, v.ID, action, questionbank.ReviewInput{ExpectedRevision: v.Revision, BundleHash: v.BundleHash})
		if err != nil {
			t.Fatal(err)
		}
	}

	return v, classID

}
