package subjective

import (
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

var gradingSQLTime = time.Date(2026, 1, 8, 9, 10, 11, 0, time.UTC)

func gradingSQLValues(args []driver.NamedValue) []driver.Value {
	values := make([]driver.Value, len(args))
	for index, arg := range args {
		values[index] = arg.Value
	}
	return values
}

func gradingSQLArgs(want ...driver.Value) func(string, []driver.NamedValue) error {
	return func(_ string, args []driver.NamedValue) error {
		if !slices.Equal(gradingSQLValues(args), want) {
			return errors.New("grading SQL used unexpected identities, versions or state")
		}
		return nil
	}
}

func gradingSQLRun(run GradingRun) []driver.Value {
	var started, completed driver.Value
	if run.Status != RunQueued {
		started = gradingSQLTime
	}
	if !workerRunActive(run) {
		completed = gradingSQLTime
	}
	return []driver.Value{
		run.ID, run.TenantID, run.BatchID, run.AnswerSegmentID, run.AnswerVersion,
		run.QuestionID, run.RubricVersion, run.ModelVersion, run.PromptVersion, run.MinConfidence, run.RequestID,
		run.PanelID, run.AgentRole, run.MathArtifactID, int64(run.MathArtifactVersion), int64(run.MathCorrectionRevision), run.MathScoringVersion,
		run.Status, int64(run.AttemptCount), run.GradeID, run.ErrorCode, started, completed, gradingSQLTime, gradingSQLTime, nil,
		run.ExamQuestionSnapshotID,
	}
}

func gradingSQLRunFixture() GradingRun {
	return GradingRun{
		ID: "run-1", TenantID: workerServiceTenant, BatchID: "batch-1", AnswerSegmentID: "segment-1", AnswerVersion: "answer-v1",
		QuestionID: "question-1", RubricVersion: "rubric-v1", ModelVersion: "model-v1", PromptVersion: "prompt-v1",
		MinConfidence: .8, RequestID: "request-1", AgentRole: AgentRoleSingle, Status: RunQueued,
		ExamQuestionSnapshotID: "snapshot-1",
	}
}

func TestPostgresRunCreationRequiresFrozenSnapshotAndTrialMembership(t *testing.T) {
	run := gradingSQLRunFixture()
	input := CreateRunInput{
		BatchID: run.BatchID, AnswerSegmentID: run.AnswerSegmentID, AnswerVersion: run.AnswerVersion,
		QuestionID: run.QuestionID, RubricVersion: run.RubricVersion, ModelVersion: run.ModelVersion, PromptVersion: run.PromptVersion,
		MinConfidence: run.MinConfidence, RequestID: run.RequestID, ExamQuestionSnapshotID: run.ExamQuestionSnapshotID,
	}
	injected := errors.New("trial state unavailable")
	for _, tc := range []struct {
		name  string
		steps []imageSQLStep
		want  error
	}{
		{"snapshot changed", []imageSQLStep{imageQuery("FROM exam_question_snapshot", false)}, ErrIdempotencyConflict},
		{"batch missing", []imageSQLStep{imageQuery("FROM exam_question_snapshot", true), imageQuery("subjective_grading_batch")}, ErrNotFound},
		{"batch cancelled", []imageSQLStep{imageQuery("FROM exam_question_snapshot", true), imageQuery("FOR SHARE", "cancelled", true)}, ErrBatchCancelled},
		{"answer outside batch", []imageSQLStep{imageQuery("FROM exam_question_snapshot", true), imageQuery("FOR SHARE", "planned", false)}, ErrInvalidInput},
		{"trial not approved", []imageSQLStep{imageQuery("FROM exam_question_snapshot", true), imageQuery("FOR SHARE", "planned", true), imageQuery("FROM scoring_ai_trial", true)}, ErrTrialConflict},
		{"trial query fails", []imageSQLStep{imageQuery("FROM exam_question_snapshot", true), imageQuery("FOR SHARE", "planned", true), {kind: "query", contains: "FROM scoring_ai_trial", err: injected}}, injected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append([]imageSQLStep{{kind: "begin"}}, tc.steps...)
			steps = append(steps, imageSQLStep{kind: "rollback"})
			_, err := NewPostgresStore(imageTestDB(t, steps...)).GetOrCreateRun(t.Context(), run.TenantID, "teacher-1", input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("run admission error = %v, want %v", err, tc.want)
			}
		})
	}

	// 插入冲突由数据库返回原运行。旧快照的幂等键不能用于接受新的冻结题目。
	for _, replaySnapshot := range []string{run.ExamQuestionSnapshotID, "older-snapshot"} {
		t.Run(replaySnapshot, func(t *testing.T) {
			existing := run
			existing.ExamQuestionSnapshotID = replaySnapshot
			insert := imageQuery("ON CONFLICT (tenant_id, request_id)", gradingSQLRun(existing)...)
			insert.check = gradingSQLArgs(run.TenantID, run.BatchID, run.AnswerSegmentID, run.AnswerVersion, run.QuestionID,
				run.RubricVersion, run.ModelVersion, run.PromptVersion, run.MinConfidence, run.RequestID, "", AgentRoleSingle,
				"", int64(0), int64(0), "", RunQueued, int64(0), run.ExamQuestionSnapshotID)
			end := "commit"
			if replaySnapshot != run.ExamQuestionSnapshotID {
				end = "rollback"
			}
			db := imageTestDB(t, imageSQLStep{kind: "begin"}, imageQuery("FROM exam_question_snapshot", true), imageQuery("FOR SHARE", "planned", true),
				imageQuery("FROM scoring_ai_trial", false), insert, imageSQLStep{kind: end})
			got, err := NewPostgresStore(db).GetOrCreateRun(t.Context(), run.TenantID, "teacher-1", input)
			if end == "rollback" {
				if !errors.Is(err, ErrIdempotencyConflict) || got.ID != "" {
					t.Fatalf("old snapshot received a runnable identity: %+v, %v", got, err)
				}
			} else if err != nil || got.ID != run.ID || got.Status != RunQueued || got.StartedAt != nil {
				t.Fatalf("queued batch run had altered state: %+v, %v", got, err)
			}
		})
	}
}

func TestPostgresPanelClaimNeverReexecutesAnExistingRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		panel  string
		role   string
		claim  bool
		want   error
	}{
		{"first claim", RunProcessing, "panel-1", AgentRolePrimaryA, true, nil},
		{"already processing", RunProcessing, "panel-1", AgentRolePrimaryA, false, nil},
		{"already succeeded", RunSucceeded, "panel-1", AgentRolePrimaryA, false, nil},
		{"standalone run", RunProcessing, "", AgentRoleSingle, false, ErrInvalidInput},
		{"run missing", "", "", AgentRoleSingle, false, ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := gradingSQLRunFixture()
			run.Status, run.PanelID, run.AgentRole, run.AttemptCount = tc.status, tc.panel, tc.role, 1
			claim := imageQuery("AND status='queued'")
			claim.check = gradingSQLArgs(run.TenantID, run.ID)
			steps := []imageSQLStep{claim}
			if tc.claim {
				steps[0].values = gradingSQLRun(run)
			} else if tc.status == "" {
				steps = append(steps, imageQuery("FROM subjective_grading_run"))
			} else {
				steps = append(steps, imageQuery("FROM subjective_grading_run", gradingSQLRun(run)...))
			}
			got, claimed, err := NewPostgresStore(imageTestDB(t, steps...)).ClaimPanelRun(t.Context(), run.TenantID, run.ID)
			if !errors.Is(err, tc.want) || claimed != tc.claim {
				t.Fatalf("claim = %t, %v; want %t, %v", claimed, err, tc.claim, tc.want)
			}
			if tc.want == nil && (got.ID != run.ID || got.Status != tc.status || got.StartedAt == nil || got.AttemptCount != 1) {
				t.Fatalf("claim lost actual run identity or state: %+v", got)
			}
		})
	}
}

func gradingSQLTask(run GradingRun, status, lease, source string, attempt int64) []driver.Value {
	return []driver.Value{
		"task-1", run.TenantID, "ai_grade", "subjective-grading", "subjective_grading_run", source,
		status, int64(100), []byte("{}"), "subjective-grade-v1", []byte("{}"), []byte("{}"),
		"", "", "task-key", "", int64(2), attempt, int64(30), nil,
		lease, gradingSQLTime.Add(time.Minute), "worker-1", "subjective-worker", "worker-1",
		gradingSQLTime, nil, nil, int64(0), "", []byte("{}"), "teacher-1", gradingSQLTime, gradingSQLTime, int64(1),
	}
}

func gradingSQLLockedRun(run GradingRun) imageSQLStep {
	step := imageQuery("FOR UPDATE", gradingSQLRun(run)...)
	step.check = func(query string, args []driver.NamedValue) error {
		if !strings.Contains(query, "FROM subjective_grading_run") {
			return errors.New("source run must be locked before its task")
		}
		return gradingSQLArgs(run.TenantID, run.ID)(query, args)
	}
	return step
}

func TestPostgresWorkerProcessingChecksBothLocksAndRollsBackRejection(t *testing.T) {
	run := gradingSQLRunFixture()
	const lease = "lease-1"
	injected := errors.New("processing write failed")
	for _, tc := range []struct {
		name         string
		runStatus    string
		taskStatus   string
		taskSource   string
		requestLease string
		updateErr    error
		want         error
	}{
		{"active attempt", RunQueued, workerruntime.StatusLeased, run.ID, lease, nil, nil},
		{"old lease", RunQueued, workerruntime.StatusLeased, run.ID, "expired-lease", nil, ErrWorkerLeaseMismatch},
		{"finished task", RunQueued, workerruntime.StatusSucceeded, run.ID, lease, nil, ErrWorkerLeaseMismatch},
		{"terminal run", RunFailed, workerruntime.StatusLeased, run.ID, lease, nil, ErrWorkerLeaseMismatch},
		{"foreign task", RunQueued, workerruntime.StatusLeased, "different-run", lease, nil, ErrWorkerTaskMismatch},
		{"write failure", RunQueued, workerruntime.StatusLeased, run.ID, lease, injected, injected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			locked := run
			locked.Status = tc.runStatus
			steps := []imageSQLStep{{kind: "begin"}, gradingSQLLockedRun(locked),
				imageQuery("FROM agent_worker_task", gradingSQLTask(locked, tc.taskStatus, lease, tc.taskSource, 1)...)}
			if tc.taskStatus != workerruntime.StatusSucceeded {
				steps = append(steps, imageQuery("SELECT clock_timestamp()", gradingSQLTime))
			}
			if tc.want == nil || tc.updateErr != nil {
				updated := locked
				updated.Status, updated.AttemptCount = RunProcessing, 1
				step := imageQuery("UPDATE subjective_grading_run", gradingSQLRun(updated)...)
				step.check = gradingSQLArgs(run.TenantID, run.ID, RunProcessing, "", "", int64(1), nil)
				step.err = tc.updateErr
				steps = append(steps, step)
			}
			end := "rollback"
			if tc.want == nil {
				end = "commit"
			}
			steps = append(steps, imageSQLStep{kind: end})
			got, task, err := NewPostgresStore(imageTestDB(t, steps...)).MarkWorkerProcessing(t.Context(), run.TenantID, run.ID, "task-1", tc.requestLease, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("processing rejected with %v, want %v", err, tc.want)
			}
			if tc.want == nil && (got.Status != RunProcessing || got.AttemptCount != task.AttemptCount || got.StartedAt == nil) {
				t.Fatalf("processing did not reflect locked attempt: %+v, %+v", got, task)
			}
			if tc.want != nil {
				// 拒绝请求会返回已读取的运行与任务供诊断，运行状态仍须保留拒绝前的值。
				if tc.updateErr == nil && (got.ID != locked.ID || got.Status != locked.Status || got.AttemptCount != locked.AttemptCount) {
					t.Fatalf("rejected processing changed the locked run: %+v", got)
				}
				if task.ID != "" && (task.Status != tc.taskStatus || task.SourceID != tc.taskSource) {
					t.Fatalf("rejected processing changed the locked task: %+v", task)
				}
			}
		})
	}
}
