package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/review"
	"github.com/google/uuid"
)

type currentAIFailureFixture struct {
	store                                       *PostgresStore
	tenant, actor, task, snapshot, answer, exam string
	run                                         GradingRun
}

func TestPostgresRunSnapshotInsertReplayAndGradeBinding(t *testing.T) {
	_, db, tenant := diagnosticsPostgresDB(t)
	f := seedCurrentAIFailure(t, db, tenant)
	input := CreateRunInput{AnswerSegmentID: f.run.AnswerSegmentID, QuestionID: f.run.QuestionID, ExamQuestionSnapshotID: f.snapshot, AnswerVersion: f.answer, RubricVersion: f.run.RubricVersion, ModelVersion: "model", PromptVersion: "prompt", RequestID: uuid.NewString()}
	run, err := f.store.GetOrCreateRun(context.Background(), tenant, f.actor, input)
	if err != nil || run.ExamQuestionSnapshotID != f.snapshot {
		t.Fatalf("run snapshot insert: %#v %v", run, err)
	}
	var nextSnapshot string
	if err := db.QueryRow(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,snapshot_version,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash)
SELECT tenant_id,exam_id,question_id,2,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,repeat('c',64) FROM exam_question_snapshot WHERE id=$1::uuid RETURNING id::text`, f.snapshot).Scan(&nextSnapshot); err != nil {
		t.Fatal(err)
	}
	workbench, err := review.NewPostgresStore(db).GetTaskContext(context.Background(), tenant, f.task)
	if err != nil || workbench.QuestionSnapshot.ID != nextSnapshot || workbench.FrozenRubric.Version != f.run.RubricVersion {
		t.Fatalf("teacher context did not use latest frozen snapshot: snapshot=%q rubric=%q err=%v", workbench.QuestionSnapshot.ID, workbench.FrozenRubric.Version, err)
	}
	input.ExamQuestionSnapshotID = nextSnapshot
	if _, err := f.store.GetOrCreateRun(context.Background(), tenant, f.actor, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same request rebound snapshot: %v", err)
	}
	other := seedCurrentAIFailure(t, db, tenant)
	input.RequestID, input.ExamQuestionSnapshotID = uuid.NewString(), other.snapshot
	if _, err := f.store.GetOrCreateRun(context.Background(), tenant, f.actor, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("run accepted another question's snapshot: %v", err)
	}
	grade := successfulGrade(Context{SegmentID: f.run.AnswerSegmentID, AnswerVersion: f.answer, Question: serviceTestContext().Question, Rubric: serviceTestContext().Rubric}, ModelPolicy{ModelVersion: "model", PromptVersion: "prompt"}, run.ID, validServiceOutput())
	grade.QuestionID, grade.AnswerSegmentID, grade.AnswerVersion, grade.AdapterRequestID, grade.RunID = f.run.QuestionID, f.run.AnswerSegmentID, f.answer, run.RequestID, run.ID
	created, err := f.store.CreateGrade(context.Background(), tenant, f.actor, grade)
	if err != nil {
		t.Fatal(err)
	}
	var actualSnapshot string
	if err := db.QueryRow(`SELECT exam_question_snapshot_id::text FROM ai_grade WHERE id=$1::uuid`, created.ID).Scan(&actualSnapshot); err != nil || actualSnapshot != f.snapshot {
		t.Fatalf("grade did not retain source run snapshot: got=%q err=%v", actualSnapshot, err)
	}
}

func TestPostgresTeacherSuggestionKeepsOnlyCurrentVersionHistory(t *testing.T) {
	_, db, tenant := diagnosticsPostgresDB(t)
	f := seedCurrentAIFailure(t, db, tenant)
	ctx := context.Background()
	workspace := review.NewPostgresStore(db)
	createSuggestion := func(snapshotID, answerID string, withRun bool) Grade {
		t.Helper()
		requestID, runID := uuid.NewString(), ""
		if withRun {
			run, err := f.store.GetOrCreateRun(ctx, tenant, f.actor, CreateRunInput{AnswerSegmentID: f.run.AnswerSegmentID, QuestionID: f.run.QuestionID, ExamQuestionSnapshotID: snapshotID, AnswerVersion: answerID, RubricVersion: f.run.RubricVersion, ModelVersion: "model", PromptVersion: "prompt", RequestID: requestID})
			if err != nil {
				t.Fatal(err)
			}
			runID = run.ID
		}
		grade := successfulGrade(Context{SegmentID: f.run.AnswerSegmentID, AnswerVersion: answerID, Question: serviceTestContext().Question, Rubric: serviceTestContext().Rubric}, ModelPolicy{ModelVersion: "model", PromptVersion: "prompt"}, runID, validServiceOutput())
		grade.QuestionID, grade.AnswerSegmentID, grade.AnswerVersion, grade.AdapterRequestID, grade.RunID = f.run.QuestionID, f.run.AnswerSegmentID, answerID, requestID, runID
		created, err := f.store.CreateGrade(ctx, tenant, f.actor, grade)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	assertSuggestion := func(wantID string, historyCount int) {
		t.Helper()
		value, err := workspace.GetTaskContext(ctx, tenant, f.task)
		if err != nil {
			t.Fatal(err)
		}
		if wantID == "" {
			if value.AISecondOpinion != nil {
				t.Fatalf("stale suggestion remained available: %#v", value.AISecondOpinion)
			}
			return
		}
		if value.AISecondOpinion == nil || value.AISecondOpinion.Metadata["id"] != wantID || len(value.AISecondOpinion.History) != historyCount {
			t.Fatalf("current suggestion/history lost version boundary: %#v", value.AISecondOpinion)
		}
	}
	first := createSuggestion(f.snapshot, f.answer, true)
	assertSuggestion(first.ID, 1)
	var nextSnapshot string
	if err := db.QueryRow(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,snapshot_version,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash)
SELECT tenant_id,exam_id,question_id,2,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,repeat('d',64) FROM exam_question_snapshot WHERE id=$1::uuid RETURNING id::text`, f.snapshot).Scan(&nextSnapshot); err != nil {
		t.Fatal(err)
	}
	assertSuggestion("", 0)
	second := createSuggestion(nextSnapshot, f.answer, true)
	assertSuggestion(second.ID, 1)
	third := createSuggestion(nextSnapshot, f.answer, true)
	assertSuggestion(third.ID, 2)
	createSuggestion(f.snapshot, f.answer, true)
	assertSuggestion(third.ID, 2)
	legacy := createSuggestion(nextSnapshot, f.answer, false)
	var unknown bool
	if err := db.QueryRow(`SELECT exam_question_snapshot_id IS NULL FROM ai_grade WHERE id=$1::uuid`, legacy.ID).Scan(&unknown); err != nil || !unknown {
		t.Fatalf("missing source run fabricated snapshot: %v %v", unknown, err)
	}
	assertSuggestion(third.ID, 2)
	if _, err := db.Exec(`INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,source,recorded_by,created_at) VALUES($1::uuid,$2::uuid,'new answer version','manual_entry',$3::uuid,now()+interval '1 minute')`, tenant, f.run.AnswerSegmentID, f.actor); err != nil {
		t.Fatal(err)
	}
	assertSuggestion("", 0)
}

func seedCurrentAIFailure(t *testing.T, db *sql.DB, tenant string) currentAIFailureFixture {
	t.Helper()
	store, _, actor, prepared, _ := seedRemediationCompletion(t, db, tenant)
	f := currentAIFailureFixture{store: store, tenant: tenant, actor: actor, run: prepared.Run}
	if err := db.QueryRow(`SELECT sub.exam_id::text FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid`, tenant, f.run.AnswerSegmentID).Scan(&f.exam); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,snapshot_version,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash,created_at)
SELECT profile.tenant_id,$4::uuid,$2::uuid,1,profile.id,profile.version,'short_constructed','["text_span"]','R2','{}','{}',jsonb_build_object('version',$3::text),'{"mode":"AI_ASSIST"}',repeat('a',64),now()-interval '1 minute'
FROM subject_profile profile WHERE profile.tenant_id=$1::uuid AND profile.subject_code='physics' AND profile.education_stage='junior' AND profile.version=1 RETURNING id::text`, tenant, f.run.QuestionID, f.run.RubricVersion, f.exam).Scan(&f.snapshot); err != nil {
		t.Fatalf("seed frozen task rubric: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,source,recorded_by,created_at) VALUES($1::uuid,$2::uuid,'synthetic answer','manual_entry',$3::uuid,now()-interval '30 seconds') RETURNING id::text`, tenant, f.run.AnswerSegmentID, actor).Scan(&f.answer); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,assigned_to,created_by)
SELECT tenant_id,$3::uuid,question_id,question_no,id,submission_id,'synthetic','manual_sample','assigned',$4::uuid,$4::uuid FROM answer_segment WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING id::text`, tenant, f.run.AnswerSegmentID, f.exam, actor).Scan(&f.task); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(truncatedDiagnostics())
	if _, err := db.Exec(`UPDATE subjective_grading_run SET answer_version=$2,status='failed',execution_diagnostics=$3::jsonb,exam_question_snapshot_id=$4::uuid WHERE id=$1::uuid`, f.run.ID, f.answer, raw, f.snapshot); err != nil {
		t.Fatal(err)
	}
	f.run.AnswerVersion, f.run.Status, f.run.ExamQuestionSnapshotID = f.answer, RunFailed, f.snapshot
	return f
}

func assertCurrentAIFailure(t *testing.T, f currentAIFailureFixture, want string) {
	t.Helper()
	got, err := f.store.CurrentAIFailure(context.Background(), f.tenant, f.task, f.run.AnswerSegmentID, f.run.QuestionID, f.snapshot, f.run.RubricVersion)
	if err != nil || got != want {
		t.Fatalf("current AI failure=%q, want=%q, err=%v", got, want, err)
	}
}

func TestPostgresCurrentAIFailureTaskVersionsAndLatestState(t *testing.T) {
	_, db, tenant := diagnosticsPostgresDB(t)
	t.Run("task_tenant_question_snapshot_and_frozen_rubric", func(t *testing.T) {
		f := seedCurrentAIFailure(t, db, tenant)
		assertCurrentAIFailure(t, f, "output_truncated")
		for _, field := range []string{"tenant", "task", "segment", "question", "snapshot", "rubric"} {
			t.Run(field, func(t *testing.T) {
				copy := f
				switch field {
				case "tenant":
					copy.tenant = uuid.NewString()
				case "task":
					copy.task = uuid.NewString()
				case "segment":
					copy.run.AnswerSegmentID = uuid.NewString()
				case "question":
					copy.run.QuestionID = uuid.NewString()
				case "snapshot":
					copy.snapshot = uuid.NewString()
				case "rubric":
					copy.run.RubricVersion = "later-live-rubric"
				}
				assertCurrentAIFailure(t, copy, "")
			})
		}
	})
	t.Run("new_answer_and_new_snapshot_do_not_inherit_old_failure", func(t *testing.T) {
		f := seedCurrentAIFailure(t, db, tenant)
		if _, err := db.Exec(`INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,source,recorded_by) VALUES($1::uuid,$2::uuid,'updated answer','manual_entry',$3::uuid)`, tenant, f.run.AnswerSegmentID, f.actor); err != nil {
			t.Fatal(err)
		}
		assertCurrentAIFailure(t, f, "")
		if _, err := db.Exec(`UPDATE answer_segment_answer SET deleted_at=now() WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND id<>$3::uuid`, tenant, f.run.AnswerSegmentID, f.answer); err != nil {
			t.Fatal(err)
		}
		assertCurrentAIFailure(t, f, "output_truncated")
		if err := db.QueryRow(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,snapshot_version,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash)
SELECT tenant_id,exam_id,question_id,2,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,repeat('b',64) FROM exam_question_snapshot WHERE id=$1::uuid RETURNING id::text`, f.snapshot).Scan(&f.snapshot); err != nil {
			t.Fatal(err)
		}
		assertCurrentAIFailure(t, f, "")
	})
	t.Run("new_success_or_inflight_run_supersedes_old_failure", func(t *testing.T) {
		f := seedCurrentAIFailure(t, db, tenant)
		newRun, err := f.store.GetOrCreateRun(context.Background(), tenant, f.actor, CreateRunInput{AnswerSegmentID: f.run.AnswerSegmentID, QuestionID: f.run.QuestionID, ExamQuestionSnapshotID: f.snapshot, AnswerVersion: f.answer, RubricVersion: f.run.RubricVersion, ModelVersion: "model", PromptVersion: "prompt", RequestID: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		assertCurrentAIFailure(t, f, "")
		if _, err := f.store.UpdateRun(context.Background(), tenant, newRun.ID, UpdateRunInput{Status: RunSucceeded}); err != nil {
			t.Fatal(err)
		}
		assertCurrentAIFailure(t, f, "")
	})
	t.Run("legacy_or_unknown_diagnostic_is_generic", func(t *testing.T) {
		f := seedCurrentAIFailure(t, db, tenant)
		for _, value := range []string{"null", `{"failure_kind":"private supplier text"}`} {
			if _, err := db.Exec(`UPDATE subjective_grading_run SET execution_diagnostics=NULLIF($2::jsonb,'null'::jsonb) WHERE id=$1::uuid`, f.run.ID, value); err != nil {
				t.Fatal(err)
			}
			assertCurrentAIFailure(t, f, "output_incomplete")
		}
	})
	t.Run("legacy_run_without_snapshot_is_not_projected", func(t *testing.T) {
		f := seedCurrentAIFailure(t, db, tenant)
		if _, err := db.Exec(`UPDATE subjective_grading_run SET exam_question_snapshot_id=NULL WHERE id=$1::uuid`, f.run.ID); err != nil {
			t.Fatal(err)
		}
		assertCurrentAIFailure(t, f, "")
	})
	t.Run("scoring_run_batch_is_exact", func(t *testing.T) {
		f := seedCurrentAIFailure(t, db, tenant)
		var scoringRun string
		if err := db.QueryRow(`INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,started_by,ai_trial_required) VALUES($1::uuid,$2::uuid,$3,$4::uuid,false) RETURNING id::text`, tenant, f.exam, uuid.NewString(), f.actor).Scan(&scoringRun); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE review_task SET scoring_run_id=$2::uuid WHERE id=$1::uuid`, f.task, scoringRun); err != nil {
			t.Fatal(err)
		}
		assertCurrentAIFailure(t, f, "")
		batch, err := f.store.CreateBatch(context.Background(), tenant, f.actor, CreateBatchInput{IdempotencyKey: uuid.NewString(), SegmentIDs: []string{f.run.AnswerSegmentID}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE subjective_grading_batch SET scoring_run_id=$2::uuid WHERE id=$1::uuid`, batch.ID, scoringRun); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE subjective_grading_run SET batch_id=$2::uuid WHERE id=$1::uuid`, f.run.ID, batch.ID); err != nil {
			t.Fatal(err)
		}
		assertCurrentAIFailure(t, f, "output_truncated")
		if _, err := db.Exec(`UPDATE subjective_grading_batch SET deleted_at=now() WHERE id=$1::uuid`, batch.ID); err != nil {
			t.Fatal(err)
		}
		assertCurrentAIFailure(t, f, "")
	})
}

func TestPostgresCurrentAIFailureMathEffectiveBinding(t *testing.T) {
	_, db, tenant := diagnosticsPostgresDB(t)
	f := seedCurrentAIFailure(t, db, tenant)
	var parent, artifact string
	for _, item := range []struct {
		stage             string
		version, revision int
		current           bool
		target            *string
	}{
		{"recognition", 1, 0, false, &parent}, {"verified", 2, 2, true, &artifact},
	} {
		if err := db.QueryRow(`INSERT INTO math_understanding_artifact(tenant_id,subject_code,answer_segment_id,exam_question_snapshot_id,version,input_hash,engine_version,blocks_json,solution_graph_json,is_current,stage,parent_artifact_id,correction_revision)
VALUES($1::uuid,'mathematics',$2::uuid,$3::uuid,$4,'sha256:'||repeat('a',64),'synthetic','[{"id":"block"}]','{}',$5,$6,NULLIF($7,'')::uuid,$8) RETURNING id::text`, tenant, f.run.AnswerSegmentID, f.snapshot, item.version, item.current, item.stage, parent, item.revision).Scan(item.target); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE answer_segment SET crop_sha256=repeat('a',64) WHERE id=$1::uuid`, f.run.AnswerSegmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE subjective_grading_run SET math_artifact_id=$2::uuid,math_artifact_version=2,math_correction_revision=2,math_scoring_version=$3 WHERE id=$1::uuid`, f.run.ID, artifact, MathScoringVersionV1); err != nil {
		t.Fatal(err)
	}
	assertCurrentAIFailure(t, f, "output_truncated")
	// 已核验产物的 revision=2 加上仍附着于它的新修订=1；不能只与产物列的 2 比较。
	if _, err := db.Exec(`INSERT INTO math_understanding_correction(tenant_id,artifact_id,answer_segment_id,revision,operations_json,corrected_contract_json,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,1,'[{"type":"move_step","target_id":"block"}]','{}',$4::uuid)`, tenant, artifact, f.run.AnswerSegmentID, f.actor); err != nil {
		t.Fatal(err)
	}
	assertCurrentAIFailure(t, f, "")
	if _, err := db.Exec(`UPDATE subjective_grading_run SET math_correction_revision=3 WHERE id=$1::uuid`, f.run.ID); err != nil {
		t.Fatal(err)
	}
	assertCurrentAIFailure(t, f, "output_truncated")
	if _, err := db.Exec(`UPDATE answer_segment SET crop_sha256=repeat('b',64) WHERE id=$1::uuid`, f.run.AnswerSegmentID); err != nil {
		t.Fatal(err)
	}
	assertCurrentAIFailure(t, f, "")
	if _, err := db.Exec(`UPDATE answer_segment SET crop_sha256=repeat('a',64) WHERE id=$1::uuid`, f.run.AnswerSegmentID); err != nil {
		t.Fatal(err)
	}
	assertCurrentAIFailure(t, f, "output_truncated")
	if _, err := db.Exec(`UPDATE math_understanding_artifact SET is_current=false WHERE id=$1::uuid`, artifact); err != nil {
		t.Fatal(err)
	}
	assertCurrentAIFailure(t, f, "")
}
