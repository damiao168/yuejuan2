package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// 使用生产存储和完整迁移链，存储夹具均为合成数据。
// 此测试不覆盖数学准入资格或真实模型。
func TestPostgresMathGradingBindingsRoundTripAndRejectWrongVersion(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, token, suffix)
	e2eSeedStory056AcceptanceAnswersCount(t, db, fixture, suffix, 2)
	ctx := context.Background()
	var segmentID, questionID, snapshotID, otherSegmentID string
	if err := db.QueryRowContext(ctx, `SELECT seg.id::text,seg.question_id::text,snapshot.id::text
FROM answer_segment seg JOIN submission sub ON sub.id=seg.submission_id
JOIN exam_question_snapshot snapshot ON snapshot.tenant_id=seg.tenant_id AND snapshot.question_id=seg.question_id
WHERE sub.exam_id=$1::uuid ORDER BY seg.id LIMIT 1`, fixture.ExamID).Scan(&segmentID, &questionID, &snapshotID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT seg.id::text FROM answer_segment seg JOIN submission sub ON sub.id=seg.submission_id
WHERE sub.exam_id=$1::uuid AND seg.id<>$2::uuid LIMIT 1`, fixture.ExamID, segmentID).Scan(&otherSegmentID); err != nil {
		t.Fatal(err)
	}
	artifacts := mathunderstanding.NewPostgresStore(db)
	artifact, err := artifacts.CreateArtifact(ctx, fixture.TenantID, mathunderstanding.CreateArtifactInput{
		// 共享夹具是一场冻结的物理考试；即使这里只测试存储绑定，产物存储也要求相同学科。
		SubjectCode: "physics", AnswerSegmentID: segmentID, ExamQuestionSnapshotID: snapshotID, InputHash: "sha256:synthetic-binding", EngineVersion: "fixture-v1",
		Blocks: []mathunderstanding.MathAnswerBlock{{ID: "b1", Kind: "text", Status: "active", BoundingBox: mathunderstanding.BoundingBox{X: .1, Y: .1, Width: .5, Height: .2},
			RecognitionEngine: "fixture", RecognitionVersion: "v1", SourceImageHash: "sha256:synthetic-binding", RecognitionConfidence: .9, StructureConfidence: .9}},
		Formulas: []mathunderstanding.FormulaArtifact{}, Relations: []mathunderstanding.SpatialRelation{}, Verifications: []mathunderstanding.MathVerification{}, RubricEvidence: []mathunderstanding.RubricEvidence{},
		SolutionGraph: mathunderstanding.SolutionGraph{ID: "g1", AnswerSegmentID: segmentID, BuilderVersion: "fixture-v1", FormulaModelVersion: "fixture-v1", OverallConfidence: .9,
			Steps: []mathunderstanding.SolutionStep{{ID: "s1", BlockIDs: []string{"b1"}, Kind: "conclusion", Confidence: .9, RecognitionConfidence: .9, StructureConfidence: .9}}, Edges: []mathunderstanding.SolutionEdge{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := subjective.NewPostgresStore(db)
	input := subjective.CreateRunInput{
		AnswerSegmentID: segmentID, QuestionID: questionID, AnswerVersion: "answer-v1", RubricVersion: "rubric-v1", ModelVersion: "model-v1", PromptVersion: "prompt-v1", MinConfidence: .8, RequestID: "binding-" + suffix,
		MathArtifactID: artifact.ID, MathArtifactVersion: artifact.Version, MathCorrectionRevision: 3, MathScoringVersion: subjective.MathScoringVersionV1,
	}
	run, err := store.GetOrCreateRun(ctx, fixture.TenantID, fixture.AdminID, input)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetRun(ctx, fixture.TenantID, run.ID)
	if err != nil || loaded.MathArtifactID != artifact.ID || loaded.MathArtifactVersion != artifact.Version || loaded.MathCorrectionRevision != 3 || loaded.MathScoringVersion != subjective.MathScoringVersionV1 {
		t.Fatalf("run bindings not round-tripped: %#v %v", loaded, err)
	}
	gradeInput := subjective.Grade{
		AnswerSegmentID: segmentID, QuestionID: questionID, QuestionNo: "Q1", QuestionType: "calculation", AnswerVersion: "answer-v1", GraderType: "llm_subjective",
		ModelVersion: "model-v1", PromptVersion: "prompt-v1", RubricVersion: "rubric-v1", DeliveryMode: "teacher_suggestion", SuggestedScore: 1, MaxScore: 1, Confidence: .9,
		NeedsHumanReview: true, Status: "succeeded", AdapterRequestID: run.RequestID, RunID: run.ID,
		MathArtifactID: artifact.ID, MathArtifactVersion: artifact.Version, MathCorrectionRevision: 3, MathScoringVersion: subjective.MathScoringVersionV1,
	}
	grade, err := store.CreateGrade(ctx, fixture.TenantID, fixture.AdminID, gradeInput)
	if err != nil {
		t.Fatal(err)
	}
	readGrade, err := store.GetGradeByAdapterRequestID(ctx, fixture.TenantID, run.RequestID)
	if err != nil || readGrade.ID != grade.ID || readGrade.MathArtifactID != artifact.ID || readGrade.MathArtifactVersion != artifact.Version || readGrade.MathCorrectionRevision != 3 || readGrade.MathScoringVersion != subjective.MathScoringVersionV1 {
		t.Fatalf("grade bindings not round-tripped: %#v %v", readGrade, err)
	}
	binding := mathGradingBindingContext{DB: db, Store: store, Fixture: fixture}
	e2eAssertMathBindingReviewHistory(t, ctx, binding, gradeInput, grade, suffix)
	e2eAssertMathBindingBatchRecovery(t, ctx, binding, input, suffix)
	for _, scenario := range []string{"wrong_version", "wrong_segment", "missing_artifact", "incomplete_binding"} {
		t.Run(scenario, func(t *testing.T) {
			e2eAssertRejectedMathBinding(t, ctx, binding, input, gradeInput, otherSegmentID, scenario)
		})
	}
}

type mathGradingBindingContext struct {
	DB      *sql.DB
	Store   *subjective.PostgresStore
	Fixture story056AcceptanceFixture
}

// 指定复核任务的历史投影仍保留完整数学身份、修订号和最新到最旧的次序。
func e2eAssertMathBindingReviewHistory(t *testing.T, ctx context.Context, binding mathGradingBindingContext, gradeInput, grade subjective.Grade, suffix string) {
	t.Helper()
	db, store, fixture := binding.DB, binding.Store, binding.Fixture
	segmentID := gradeInput.AnswerSegmentID
	// 已分配任务的读取投影必须保留全部数学绑定，不能把持久化的 v2 建议静默变为无绑定的旧版 UI 材料。
	second := gradeInput
	second.AdapterRequestID, second.RunID = "history-"+suffix, ""
	second.MathCorrectionRevision = 4
	newer, err := store.CreateGrade(ctx, fixture.TenantID, fixture.AdminID, second)
	if err != nil {
		t.Fatal(err)
	}
	reviews := review.NewPostgresStore(db)
	task, err := reviews.CreateTask(ctx, fixture.TenantID, fixture.AdminID, review.CreateTaskInput{AnswerSegmentID: segmentID, Source: "manual_sample", AssignedTo: fixture.AdminID})
	if err != nil {
		t.Fatal(err)
	}
	taskContext, err := reviews.GetTaskContext(ctx, fixture.TenantID, task.ID)
	if err != nil || taskContext.AISecondOpinion == nil || len(taskContext.AISecondOpinion.History) != 2 {
		t.Fatalf("task history missing: %#v %v", taskContext.AISecondOpinion, err)
	}
	history := taskContext.AISecondOpinion.History
	if history[0]["id"] != newer.ID || history[1]["id"] != grade.ID || history[0]["math_artifact_id"] != gradeInput.MathArtifactID || history[0]["math_artifact_version"] != float64(gradeInput.MathArtifactVersion) || history[0]["math_correction_revision"] != float64(4) || history[0]["math_scoring_version"] != subjective.MathScoringVersionV1 {
		t.Fatalf("task history lost math bindings/order: %#v", history)
	}
}

// 重排批次时使用原运行冻结的绑定，不能替换为更高修订号或未来评分器。
func e2eAssertMathBindingBatchRecovery(t *testing.T, ctx context.Context, binding mathGradingBindingContext, input subjective.CreateRunInput, suffix string) {
	t.Helper()
	store, fixture := binding.Store, binding.Fixture
	segmentID := input.AnswerSegmentID
	// 恢复已有批次运行，不替换为当前证据修订号或评分器。
	// 此测试覆盖生产 SELECT/扫描回退路径。
	batch, err := store.CreateBatch(ctx, fixture.TenantID, fixture.AdminID, subjective.CreateBatchInput{IdempotencyKey: "math-batch-" + suffix, SegmentIDs: []string{segmentID}})
	if err != nil {
		t.Fatal(err)
	}
	frozenRun := input
	frozenRun.BatchID, frozenRun.RequestID = batch.ID, "math-batch-run-"+suffix
	if _, err := store.GetOrCreateRun(ctx, fixture.TenantID, fixture.AdminID, frozenRun); err != nil {
		t.Fatal(err)
	}
	changedRun := frozenRun
	changedRun.MathCorrectionRevision++
	changedRun.MathScoringVersion = "future-scorer-version"
	plan, err := store.SaveEnqueuePlan(ctx, fixture.TenantID, fixture.AdminID, subjective.BatchEnqueuePlan{
		CommandID: "enqueue:" + batch.ID, BatchID: batch.ID, Runs: []subjective.CreateRunInput{changedRun},
	})
	if err != nil || len(plan.Runs) != 1 || plan.Runs[0] != frozenRun {
		t.Fatalf("batch recovery changed the frozen math binding: %#v %v", plan, err)
	}
	loadedPlan, err := store.GetEnqueuePlan(ctx, fixture.TenantID, fixture.AdminID, batch.ID)
	if err != nil || len(loadedPlan.Runs) != 1 || loadedPlan.Runs[0] != frozenRun {
		t.Fatalf("batch plan binding not round-tripped: %#v %v", loadedPlan, err)
	}
}

// 错误版本、错误答案、缺失产物和不完整绑定分别核对对应数据库约束，不能只断言任意错误。
func e2eAssertRejectedMathBinding(t *testing.T, ctx context.Context, binding mathGradingBindingContext, input subjective.CreateRunInput, gradeInput subjective.Grade, otherSegmentID, scenario string) {
	t.Helper()
	store, fixture := binding.Store, binding.Fixture
	badRun, badGrade := input, gradeInput
	badRun.RequestID, badGrade.AdapterRequestID = uuid.NewString(), uuid.NewString()
	runConstraint, gradeConstraint := "fk_subjective_run_math_artifact", "fk_ai_grade_math_artifact"
	switch scenario {
	case "wrong_version":
		badRun.MathArtifactVersion++
		badGrade.MathArtifactVersion++
	case "wrong_segment":
		badRun.AnswerSegmentID, badGrade.AnswerSegmentID = otherSegmentID, otherSegmentID
	case "missing_artifact":
		badRun.MathArtifactID, badGrade.MathArtifactID = uuid.NewString(), uuid.NewString()
	case "incomplete_binding":
		badRun.MathScoringVersion, badGrade.MathScoringVersion = "", ""
		runConstraint, gradeConstraint = "chk_subjective_run_math_binding", "chk_ai_grade_math_binding"
	}
	if _, err := store.GetOrCreateRun(ctx, fixture.TenantID, fixture.AdminID, badRun); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != runConstraint {
			t.Fatalf("run failed for a different reason than binding: %v", err)
		}
	} else {
		t.Fatal("invalid run binding was accepted")
	}
	if _, err := store.CreateGrade(ctx, fixture.TenantID, fixture.AdminID, badGrade); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != gradeConstraint {
			t.Fatalf("grade failed for a different reason than binding: %v", err)
		}
	} else {
		t.Fatal("invalid grade binding was accepted")
	}
}
