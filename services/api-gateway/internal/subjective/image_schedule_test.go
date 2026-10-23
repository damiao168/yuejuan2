package subjective

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func imageTaskRow(requestID string) []driver.Value {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	return []driver.Value{"worker-task", "tenant-1", "ai_grade", "subjective-grading", "subjective_grading_run", "ai-run", "queued", int64(0), []byte(`{}`), "subjective-grade-v1", []byte(`{}`), []byte(`{}`), "", "", requestID, "image-subjective:ai-run", int64(3), int64(0), int64(30), nil, "", nil, "", "", "", nil, nil, nil, int64(0), "", []byte(`{}`), "actor", now, now, int64(1)}
}

func imageScheduleSteps(requestID string) []imageSQLStep {
	return []imageSQLStep{
		{kind: "begin"}, imageQuery("SELECT status FROM scoring_run", "processing"), imageQuery("SELECT item.id::text", "scoring-item"),
		{kind: "exec", contains: "INSERT INTO subjective_grading_run"}, imageQuery("SELECT id::text,input_version_id", "ai-run", true),
		imageQuery("INSERT INTO agent_worker_task", imageTaskRow(requestID)...),
		{kind: "exec", contains: "UPDATE scoring_run_item SET subjective_run_id"}, {kind: "commit"},
	}
}

func TestImageCoordinatorSchedulesOnlyTheFrozenMemberWithoutObservingAgain(t *testing.T) {
	input, value, frozen := coordinatorFrozen(t)
	observation := *value.Observation
	observation.Members = append(observation.Members, ObservedMember{QuestionID: "another-question", Presence: "present", Association: "matched", Confidence: .9,
		Answer: &ObservedAnswer{Type: "text", Value: "别题作答"}, Evidence: []ObservationEvidence{{EvidenceID: "another-answer", Text: "别题证据", BBox: []float64{.2, .2, .2, .2}}}})
	input.Observation = imageJSON(t, map[string]any{"student_observation": observation})
	steps := imageScheduleSteps(input.IdempotencyKey)
	steps[3].check = func(query string, args []driver.NamedValue) error {
		if !strings.Contains(query, "$3::uuid::text") || args[0].Value != "tenant-1" || args[1].Value != input.SegmentID || args[2].Value != input.InputVersionID || args[4].Value != input.AssessmentSnapshotID {
			return errors.New("schedule lost typed image identity")
		}
		var captured Context
		if json.Unmarshal(args[10].Value.([]byte), &captured) != nil || captured.AnswerText != "" || captured.OCRConfidence != nil || captured.MathEvidence != nil || captured.Observation == nil || len(captured.Observation.Members) != 1 || captured.Observation.Members[0].QuestionID != value.Question.ID || captured.ReferenceContext.StandardAnswer != "冻结标准答案" || captured.ReferenceContext.SnapshotHash != "snapshot-hash" {
			return errors.New("paid run did not freeze only this question's observation and reference")
		}
		return nil
	}
	steps[5].check = func(_ string, args []driver.NamedValue) error {
		var payload map[string]string
		if json.Unmarshal(args[6].Value.([]byte), &payload) != nil || payload["input_version_id"] != input.InputVersionID || payload["run_id"] != "ai-run" || args[8].Value != input.IdempotencyKey || args[9].Value != "image-subjective:ai-run" {
			return errors.New("runtime task escaped the frozen image command")
		}
		return nil
	}
	steps = append([]imageSQLStep{imageQuery("SELECT sr.rule_snapshot", imageJSON(t, frozen)), imageQuery("rule_snapshot->'ai_plan'", imageJSON(t, value.AIPlan))}, steps...)
	h := coordinatorHandler(t, imageTestDB(t, steps...), "http://no-observation-call.invalid", coordinatorModel())
	runID, taskID, err := h.ScheduleScoringItem(t.Context(), "tenant-1", "actor", input)
	if err != nil || runID != "ai-run" || taskID != "worker-task" {
		t.Fatalf("frozen observation did not schedule: run=%q task=%q err=%v", runID, taskID, err)
	}
}

func TestImageScheduleStopsBeforeRuntimeOnCancelledChangedOrConflictingInput(t *testing.T) {
	for _, failure := range []string{"begin", "cancelled", "stale member", "insert", "different replay", "runtime", "link", "commit"} {
		t.Run(failure, func(t *testing.T) {
			input, value, _ := coordinatorFrozen(t)
			steps := imageScheduleSteps(input.IdempotencyKey)
			injected := fmt.Errorf("injected %s failure", failure)
			want := injected
			rollback := func(last int) { steps = append(steps[:last+1], imageSQLStep{kind: "rollback"}) }
			switch failure {
			case "begin":
				steps[0].err = injected
				steps = steps[:1]
			case "cancelled":
				steps[1].values = []driver.Value{"cancelled"}
				want = ErrBatchCancelled
				rollback(1)
			case "stale member":
				steps[2].values = nil
				want = ErrWorkerResultVersionConflict
				rollback(2)
			case "insert":
				steps[3].err = injected
				rollback(3)
			case "different replay":
				steps[4].values = []driver.Value{"ai-run", false}
				want = ErrIdempotencyConflict
				rollback(4)
			case "runtime":
				steps[5].err = injected
				rollback(5)
			case "link":
				steps[6].err = injected
				rollback(6)
			case "commit":
				steps[7].err = injected
			}
			// 后半段失败必须请求回滚，且不得返回可供协调器误记成功的运行或任务身份。
			store := NewPostgresStore(imageTestDB(t, steps...))
			runID, taskID, err := store.scheduleImageRun(t.Context(), "tenant-1", "actor", input, value, ModelPolicy{ModelVersion: "vision-v1", PromptVersion: ImageComparisonPromptVersion, MinConfidence: .8})
			if !errors.Is(err, want) || runID != "" || taskID != "" {
				t.Fatalf("failure published a task identity: run=%q task=%q err=%v", runID, taskID, err)
			}
		})
	}
}

func TestImageScheduleValidatesFrozenRubricModelAndObservationBeforeTransaction(t *testing.T) {
	for _, boundary := range []string{"wrong snapshot", "missing rubric", "wrong mode", "deductions", "invalid observation", "changed model binding", "missing member"} {
		t.Run(boundary, func(t *testing.T) {
			input, value, frozen := coordinatorFrozen(t)
			want := ErrWorkerResultVersionConflict
			switch boundary {
			case "wrong snapshot":
				input.AssessmentSnapshotID = "different-snapshot"
			case "missing rubric":
				frozen["rubric"] = nil
				want = ErrRubricMissing
			case "wrong mode":
				value.AssessmentSnapshot.ScoringPolicySnapshot.Mode = assessment.ScoringHumanPrimary
				frozen["assessment_snapshot"] = value.AssessmentSnapshot
			case "deductions":
				value.Rubric.Deductions = []any{map[string]any{"reason": "unsupported"}}
				frozen["rubric"] = value.Rubric
				want = nil
			case "invalid observation":
				input.Observation = []byte("{")
				want = ErrInvalidInput
			case "changed model binding":
				value.Observation.ModelBinding = "changed"
				input.Observation = imageJSON(t, map[string]any{"student_observation": value.Observation})
			case "missing member":
				value.Observation.Members[0].QuestionID = "foreign-question"
				input.Observation = imageJSON(t, map[string]any{"student_observation": value.Observation})
				want = ErrInvalidModelOutput
			}
			steps := []imageSQLStep{imageQuery("SELECT sr.rule_snapshot", imageJSON(t, frozen))}
			if boundary != "wrong snapshot" && boundary != "missing rubric" {
				steps = append(steps, imageQuery("rule_snapshot->'ai_plan'", imageJSON(t, value.AIPlan)))
			}
			h := coordinatorHandler(t, imageTestDB(t, steps...), "http://no-paid-call.invalid", coordinatorModel())
			_, _, err := h.ScheduleScoringItem(t.Context(), "tenant-1", "actor", input)
			if boundary == "deductions" {
				var failure *GradingAgentError
				if !errors.As(err, &failure) || failure.Code != "image_rubric_deductions_unsupported" {
					t.Fatalf("unsupported deductions were scheduled: %v", err)
				}
				return
			}
			if !errors.Is(err, want) {
				t.Fatalf("invalid frozen boundary %s accepted: %v", boundary, err)
			}
		})
	}
}

func TestImageFrozenContextUsesSnapshotRubricAndSingleModelForSmallQuestion(t *testing.T) {
	input, value, frozen := coordinatorFrozen(t)
	value.Question.QuestionType = "fill_blank"
	value.AssessmentSnapshot.RubricSnapshot = map[string]any{"version": "assessment-rubric", "max_score": 5, "points": []paper.RubricPoint{{ID: "frozen-point", Score: 5}}}
	frozen["question"], frozen["assessment_snapshot"] = value.Question, value.AssessmentSnapshot
	plan := value.AIPlan
	plan.EffectiveMode, plan.ResolvedRoles = "dual_conditional_arbiter", map[string]grading.GradingAIModel{"primary_a": *plan.SingleModel}
	store := NewPostgresStore(imageTestDB(t, imageQuery("SELECT sr.rule_snapshot", imageJSON(t, frozen)), imageQuery("rule_snapshot->'ai_plan'", imageJSON(t, plan))))
	got, err := store.loadFrozenImageContext(t.Context(), "tenant-1", input)
	if err != nil || got.Rubric.Version != "assessment-rubric" || got.Rubric.Points[0].ID != "frozen-point" || got.AIPlan.EffectiveMode != "single" || got.AIPlan.SingleModel.ModelConfigID != plan.SingleModel.ModelConfigID {
		t.Fatalf("small-question frozen context changed: %+v err=%v", got, err)
	}
}
