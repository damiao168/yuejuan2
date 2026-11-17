package subjective

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func gradingSQLGrade(t *testing.T, grade Grade) []driver.Value {
	t.Helper()
	return []driver.Value{
		grade.ID, grade.TenantID, grade.AnswerSegmentID, grade.QuestionID, grade.QuestionNo, grade.QuestionType,
		grade.AnswerVersion, grade.GraderType, grade.ModelVersion, grade.PromptVersion, grade.RubricVersion, grade.DeliveryMode, grade.CapabilityProfile,
		grade.AdapterRequestID, grade.AdapterName, grade.ProviderKey, grade.DeploymentKey, grade.DeploymentRegion,
		int64(grade.AdapterAttempts), int64(grade.AdapterLatencyMS), grade.AdapterRepairAttempted, grade.SuggestedScore, grade.MaxScore, grade.Confidence,
		imageJSON(t, grade.MatchedPoints), imageJSON(t, grade.MissingPoints), imageJSON(t, grade.Evidence), imageJSON(t, grade.RiskFlags),
		grade.NeedsHumanReview, grade.StudentFeedback, grade.TeacherNote, grade.Mock, grade.Status, grade.FailureReason,
		imageJSON(t, grade.RawOutput), grade.CreatedBy, gradingSQLTime, grade.RunID, grade.MathArtifactID,
		int64(grade.MathArtifactVersion), int64(grade.MathCorrectionRevision), grade.MathScoringVersion,
	}
}

func gradingWorkerSettlementFixture(t *testing.T, run GradingRun) (WorkerExecutionContext, WorkerResultInput, Grade) {
	t.Helper()
	value := serviceTestContext()
	value.SegmentID, value.AnswerVersion = run.AnswerSegmentID, run.AnswerVersion
	value.Question.ID, value.Rubric.Version, value.AssessmentSnapshot.ID = run.QuestionID, run.RubricVersion, run.ExamQuestionSnapshotID
	task := workerruntime.Task{ID: "task-1", WorkerService: "subjective-worker", WorkerInstanceID: "worker-1", LeaseToken: "lease-1"}
	policy := ModelPolicy{ModelVersion: run.ModelVersion, PromptVersion: run.PromptVersion, MinConfidence: run.MinConfidence}
	output := validServiceOutput()
	output.RequestID, output.ModelVersion, output.PromptVersion, output.RubricVersion = run.RequestID, run.ModelVersion, run.PromptVersion, run.RubricVersion
	input := WorkerResultInput{TaskID: task.ID, LeaseToken: task.LeaseToken, DurationMS: 15, ResultSchemaVersion: "subjective-grade-v1", Output: output}
	grade := successfulGrade(value, policy, run.ID, output)
	grade.ID, grade.TenantID, grade.CreatedBy = "grade-1", run.TenantID, "teacher-1"
	return WorkerExecutionContext{Run: run, Task: task, Context: value, Policy: policy}, input, grade
}

func TestPostgresWorkerCompletionKeepsGradeTaskAndRunInOneTransaction(t *testing.T) {
	run := gradingSQLRunFixture()
	run.Status, run.AttemptCount = RunProcessing, 1
	injected := errors.New("injected state write error")
	prepared, input, grade := gradingWorkerSettlementFixture(t, run)
	resultHash, err := workerResultHash(input)
	if err != nil {
		t.Fatal(err)
	}

	// 快照在模型调用后可能变更。复核必须早于成绩写入，随后任一写入失败都应回滚。
	for _, tc := range []struct {
		name    string
		failure string
		want    error
	}{
		{"commit all facts", "", nil},
		{"stale frozen answer", "snapshot", ErrWorkerResultVersionConflict},
		{"task write fails", "task", injected},
		{"run write fails", "run", injected},
		{"commit fails", "commit", injected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leased := gradingSQLTask(run, workerruntime.StatusLeased, input.LeaseToken, run.ID, 1)
			lockedTask := imageQuery("FROM agent_worker_task", leased...)
			lockedTask.check = gradingSQLArgs(run.TenantID, input.TaskID)
			clock := imageQuery("SELECT clock_timestamp()", gradingSQLTime)
			snapshot := imageQuery("JOIN LATERAL", tc.failure != "snapshot")
			snapshot.check = gradingSQLArgs(run.TenantID, run.AnswerSegmentID, run.ExamQuestionSnapshotID, run.RubricVersion, run.AnswerVersion, run.QuestionID)
			steps := []imageSQLStep{{kind: "begin"}, gradingSQLLockedRun(run), lockedTask, clock, snapshot}
			if tc.failure != "snapshot" {
				insert := imageQuery("INSERT INTO ai_grade", gradingSQLGrade(t, grade)...)
				insert.check = func(_ string, args []driver.NamedValue) error {
					if len(args) < 36 || args[0].Value != run.TenantID || args[17].Value != "teacher-1" || args[27].Value != run.RequestID || args[35].Value != run.ID {
						return errors.New("grade lost the original actor, tenant or request")
					}
					return nil
				}
				closedTask := imageQuery("SET status = 'succeeded'", gradingSQLTask(run, workerruntime.StatusSucceeded, input.LeaseToken, run.ID, 1)...)
				closedTask.check = func(_ string, args []driver.NamedValue) error {
					var output map[string]any
					if len(args) < 4 {
						return errors.New("worker completion omitted its frozen result payload")
					}
					raw, ok := args[2].Value.([]byte)
					if !ok || json.Unmarshal(raw, &output) != nil || output["run_id"] != run.ID || output["grade_id"] != grade.ID ||
						output["worker_result_hash"] != resultHash || args[3].Value != input.ResultSchemaVersion {
						return errors.New("worker completion was not bound to the persisted grade and full input")
					}
					return nil
				}
				if tc.failure == "task" {
					closedTask.err = injected
				}
				steps = append(steps, imageQuery("FROM scoring_ai_trial", false), insert, lockedTask, clock, closedTask)
				if tc.failure != "task" {
					updated := run
					updated.Status, updated.GradeID = RunSucceeded, grade.ID
					update := imageQuery("UPDATE subjective_grading_run", gradingSQLRun(updated)...)
					update.check = gradingSQLArgs(run.TenantID, run.ID, RunSucceeded, grade.ID, "", int64(1), nil)
					if tc.failure == "run" {
						update.err = injected
					}
					steps = append(steps, imageSQLStep{kind: "exec", contains: "UPDATE agent_worker_task_attempt"}, update)
				}
			}
			end := imageSQLStep{kind: "commit"}
			switch tc.failure {
			case "snapshot", "task", "run":
				end.kind = "rollback"
			case "commit":
				end.err = injected
			}
			steps = append(steps, end)
			store := NewPostgresStore(imageTestDB(t, steps...))
			got, err := store.CompleteWorkerAtomic(t.Context(), run.TenantID, "", prepared, input, grade, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("completion error = %v, want %v", err, tc.want)
			}
			if tc.want == nil {
				if got.Grade.ID != grade.ID || got.Run.GradeID != grade.ID || got.Run.Status != RunSucceeded || got.Task.Status != workerruntime.StatusSucceeded {
					t.Fatalf("success omitted an accepted fact: %+v", got)
				}
			} else if got.Grade.ID != "" || got.Run.ID != "" || got.Task.ID != "" {
				t.Fatalf("rolled back result escaped: %+v", got)
			}
		})
	}
}

func TestPostgresWorkerFailureClosesAttemptBeforeUpdatingRun(t *testing.T) {
	run := gradingSQLRunFixture()
	run.Status, run.AttemptCount = RunProcessing, 1
	input := WorkerFailureInput{TaskID: "task-1", LeaseToken: "lease-1", Retryable: true, ErrorCode: "provider_unavailable"}
	injected := errors.New("failure run update failed")
	for _, tc := range []struct {
		name       string
		endStatus  string
		retryable  bool
		taskSource string
		lease      string
		writeErr   error
		want       error
	}{
		{"retry active attempt", workerruntime.StatusQueued, true, run.ID, input.LeaseToken, nil, nil},
		{"terminal failure", workerruntime.StatusFailed, false, run.ID, input.LeaseToken, nil, nil},
		{"old lease", "", true, run.ID, "old-lease", nil, ErrWorkerFailureRejected},
		{"task belongs to another run", "", true, "other-run", input.LeaseToken, nil, ErrWorkerTaskMismatch},
		{"failure update rolled back", workerruntime.StatusFailed, false, run.ID, input.LeaseToken, injected, injected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := input
			current.LeaseToken, current.Retryable = tc.lease, tc.retryable
			task := imageQuery("FROM agent_worker_task", gradingSQLTask(run, workerruntime.StatusLeased, input.LeaseToken, tc.taskSource, 1)...)
			clock := imageQuery("SELECT clock_timestamp()", gradingSQLTime)
			steps := []imageSQLStep{{kind: "begin"}, gradingSQLLockedRun(run), task, clock}
			wantRunStatus := RunFailed
			if tc.endStatus != "" {
				failedRow := gradingSQLTask(run, tc.endStatus, input.LeaseToken, run.ID, 1)
				failedRow[29] = current.ErrorCode
				failed := imageQuery("UPDATE agent_worker_task", failedRow...)
				failed.check = func(_ string, args []driver.NamedValue) error {
					if args[2].Value != tc.endStatus || args[4].Value != current.ErrorCode {
						return errors.New("failed runtime attempt lost assigned status or error")
					}
					return nil
				}
				if tc.endStatus == workerruntime.StatusQueued {
					wantRunStatus = RunQueued
				}
				updated := run
				updated.Status, updated.ErrorCode = wantRunStatus, current.ErrorCode
				update := imageQuery("UPDATE subjective_grading_run", gradingSQLRun(updated)...)
				update.check = gradingSQLArgs(run.TenantID, run.ID, wantRunStatus, "", current.ErrorCode, int64(1), nil)
				update.err = tc.writeErr
				steps = append(steps, task, clock, failed, imageSQLStep{kind: "exec", contains: "UPDATE agent_worker_task_attempt"}, update)
			}
			end := "rollback"
			if tc.want == nil {
				end = "commit"
			}
			steps = append(steps, imageSQLStep{kind: end})
			got, err := NewPostgresStore(imageTestDB(t, steps...)).FailWorkerAtomic(t.Context(), run.TenantID, run.ID, current, "", nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("failure error = %v, want %v", err, tc.want)
			}
			if tc.want == nil && (got.Task.Status != tc.endStatus || got.Run.Status != wantRunStatus || got.Run.ErrorCode != current.ErrorCode) {
				t.Fatalf("failure returned inconsistent states: %+v", got)
			}
			if tc.want != nil && (got.Task.ID != "" || got.Run.ID != "") {
				t.Fatalf("rejected failure exposed accepted state: %+v", got)
			}
		})
	}
}
