package subjective

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

const (
	gradingSQLBatchID      = "d27acdec-a76e-40b0-b45f-28a17db3205a"
	gradingSQLScoringRunID = "34188745-1ba2-420f-8ec9-d00a4d70ffbc"
	gradingSQLSegmentID    = "a6b029f9-57ea-4923-bfe7-ff4a7a6c9daa"
	gradingSQLActor        = "teacher-1"
	gradingSQLCommand      = "recover-grading"
)

func gradingSQLBatch(actor, status string) []driver.Value {
	return []driver.Value{gradingSQLBatchID, workerServiceTenant, gradingSQLCommand, gradingSQLScoringRunID, status,
		[]byte(`["` + gradingSQLSegmentID + `"]`), int64(1), int64(0), int64(0), int64(0), int64(0), actor, gradingSQLTime, gradingSQLTime}
}

func TestPostgresBatchCreationChecksScopeBeforeReplayingCommand(t *testing.T) {
	input := CreateBatchInput{IdempotencyKey: gradingSQLCommand, SegmentIDs: []string{gradingSQLSegmentID}, ScoringRunID: gradingSQLScoringRunID}
	for _, tc := range []struct {
		name          string
		trialRequired bool
		eligible      int64
		existingActor string
		want          error
	}{
		{"same request", false, 1, gradingSQLActor, nil},
		{"another actor", false, 1, "different-teacher", ErrIdempotencyConflict},
		{"outside scoring run", false, 0, "", ErrInvalidInput},
		{"requires trial", true, 0, "", ErrTrialConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []imageSQLStep{imageQuery("sr.ai_trial_required", tc.trialRequired)}
			if !tc.trialRequired {
				steps = append(steps, imageQuery("count(DISTINCT seg.id)", tc.eligible))
			}
			if tc.existingActor != "" {
				insert := imageQuery("ON CONFLICT (tenant_id, idempotency_key)", gradingSQLBatch(tc.existingActor, "planned")...)
				insert.check = func(_ string, args []driver.NamedValue) error {
					var scope []string
					raw, ok := args[2].Value.([]byte)
					if !ok || json.Unmarshal(raw, &scope) != nil || !slices.Equal(scope, []string{gradingSQLSegmentID}) ||
						args[0].Value != workerServiceTenant || args[4].Value != gradingSQLActor || args[6].Value != gradingSQLScoringRunID {
						return errors.New("idempotency insert used an out-of-scope batch")
					}
					return nil
				}
				steps = append(steps, insert)
			}
			got, err := NewPostgresStore(imageTestDB(t, steps...)).CreateBatch(t.Context(), workerServiceTenant, gradingSQLActor, input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("batch creation error = %v, want %v", err, tc.want)
			}
			if tc.want == nil && (got.ID != gradingSQLBatchID || !slices.Equal(got.SegmentIDs, []string{gradingSQLSegmentID})) {
				t.Fatalf("request did not recover the original batch: %+v", got)
			}
		})
	}
}

func TestPostgresBatchRecoveryScopesOriginalActorAndEnqueuePlan(t *testing.T) {
	t.Run("find command", func(t *testing.T) {
		match := imageQuery("created_by=$2::uuid", gradingSQLBatch(gradingSQLActor, "planned")...)
		match.check = gradingSQLArgs(workerServiceTenant, gradingSQLActor, gradingSQLCommand)
		miss := imageQuery("created_by=$2::uuid")
		miss.check = gradingSQLArgs(workerServiceTenant, "different-teacher", gradingSQLCommand)
		store := NewPostgresStore(imageTestDB(t, match, miss))
		got, err := store.RecoverBatchCommand(t.Context(), workerServiceTenant, gradingSQLActor, gradingSQLCommand)
		if err != nil || got.CommandID != gradingSQLCommand || got.Status != "succeeded" || got.Batch == nil || got.Batch.ID != gradingSQLBatchID {
			t.Fatalf("creator could not recover batch: %+v, %v", got, err)
		}
		got, err = store.RecoverBatchCommand(t.Context(), workerServiceTenant, "different-teacher", gradingSQLCommand)
		if err != nil || got.Status != "not_accepted" || got.Batch != nil {
			t.Fatalf("another actor received batch facts: %+v, %v", got, err)
		}
		if _, err := store.RecoverBatchCommand(t.Context(), workerServiceTenant, gradingSQLActor, ""); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("empty command error = %v", err)
		}
	})

	t.Run("read exact completed plan", func(t *testing.T) {
		runs := []CreateRunInput{{AnswerSegmentID: gradingSQLSegmentID, RequestID: "original-run"}}
		statement := imageQuery("AND actor_id=$3::uuid", gradingSQLCommand, gradingSQLBatchID, imageJSON(t, runs), true)
		statement.check = gradingSQLArgs(workerServiceTenant, gradingSQLBatchID, gradingSQLActor)
		miss := imageQuery("AND actor_id=$3::uuid")
		complete := imageSQLStep{kind: "exec", contains: "completed_at=COALESCE", check: gradingSQLArgs(workerServiceTenant, gradingSQLBatchID, gradingSQLActor)}
		store := NewPostgresStore(imageTestDB(t, statement, miss, complete))
		got, err := store.GetEnqueuePlan(t.Context(), workerServiceTenant, gradingSQLActor, gradingSQLBatchID)
		if err != nil || got.CommandID != gradingSQLCommand || !got.Completed || !slices.Equal(got.Runs, runs) {
			t.Fatalf("plan failed to retain original identities: %+v, %v", got, err)
		}
		if _, err := store.GetEnqueuePlan(t.Context(), workerServiceTenant, "different-teacher", gradingSQLBatchID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("another actor could read protected plan: %v", err)
		}
		if err := store.CompleteEnqueuePlan(t.Context(), workerServiceTenant, gradingSQLActor, gradingSQLBatchID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPostgresBatchSaveKeepsPriorRunFactsOrRollsBack(t *testing.T) {
	injected := errors.New("plan insert error")
	prior := CreateRunInput{BatchID: gradingSQLBatchID, AnswerSegmentID: gradingSQLSegmentID, RequestID: "original-run",
		AnswerVersion: "answer-v1", QuestionID: "question-1", RubricVersion: "rubric-v1", ModelVersion: "model-v1", PromptVersion: "prompt-v1", MinConfidence: .8,
		MathArtifactID: "math-1", MathArtifactVersion: 3, MathCorrectionRevision: 2, MathScoringVersion: "math-v2", ExamQuestionSnapshotID: "snapshot-1"}
	const currentRequest = "recomputed-run"
	for _, tc := range []struct {
		name       string
		actor      string
		command    string
		priorCount int64
		writeErr   error
		want       error
	}{
		{"replay same command", gradingSQLActor, gradingSQLCommand, 0, nil, nil},
		{"reject different actor", "different-teacher", gradingSQLCommand, 0, nil, ErrIdempotencyConflict},
		{"reject different command", gradingSQLActor, "previous-command", 0, nil, ErrIdempotencyConflict},
		{"preserve migrated run", "", "", 1, nil, nil},
		{"reject ambiguous old runs", "", "", 2, nil, ErrIdempotencyConflict},
		{"rollback failed insert", "", "", 1, injected, injected},
		{"new plan", "", "", 0, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := prior
			input.RequestID = currentRequest
			plan := BatchEnqueuePlan{BatchID: gradingSQLBatchID, CommandID: gradingSQLCommand, Runs: []CreateRunInput{input}}
			steps := []imageSQLStep{{kind: "begin"}, {kind: "exec", contains: "pg_advisory_xact_lock", check: gradingSQLArgs(workerServiceTenant + ":" + gradingSQLBatchID + ":subjective-plan")}}
			if tc.actor != "" {
				steps = append(steps, imageQuery("SELECT actor_id::text", tc.actor, tc.command, imageJSON(t, []CreateRunInput{prior})))
			} else {
				steps = append(steps, imageQuery("SELECT actor_id::text"), imageQuery("SELECT count(*) FROM subjective_grading_run", tc.priorCount))
				if tc.priorCount == 1 {
					steps = append(steps, imageQuery("SELECT answer_version", prior.AnswerVersion, prior.QuestionID, prior.RubricVersion, prior.ModelVersion, prior.PromptVersion,
						prior.MinConfidence, prior.RequestID, prior.MathArtifactID, int64(prior.MathArtifactVersion), int64(prior.MathCorrectionRevision), prior.MathScoringVersion, prior.ExamQuestionSnapshotID))
				}
				if tc.priorCount < 2 {
					insert := imageSQLStep{kind: "exec", contains: "INSERT INTO subjective_batch_enqueue_plan", err: tc.writeErr}
					insert.check = func(_ string, args []driver.NamedValue) error {
						var got []CreateRunInput
						want := input
						if tc.priorCount == 1 {
							want = prior
						}
						if len(args) != 6 {
							return errors.New("plan insert must persist its scoped command, hash and frozen run inputs")
						}
						raw, ok := args[5].Value.([]byte)
						if args[0].Value != workerServiceTenant || args[1].Value != gradingSQLBatchID ||
							args[2].Value != gradingSQLActor || args[3].Value != gradingSQLCommand ||
							!ok || json.Unmarshal(raw, &got) != nil || !slices.Equal(got, []CreateRunInput{want}) {
							return errors.New("planned inputs did not retain the expected request, versions and snapshot")
						}
						return nil
					}
					steps = append(steps, insert)
				}
			}
			end := "commit"
			if tc.want != nil {
				end = "rollback"
			}
			steps = append(steps, imageSQLStep{kind: end})
			got, err := NewPostgresStore(imageTestDB(t, steps...)).SaveEnqueuePlan(t.Context(), workerServiceTenant, gradingSQLActor, plan)
			if !errors.Is(err, tc.want) {
				t.Fatalf("save plan error = %v, want %v", err, tc.want)
			}
			if tc.want != nil {
				return
			}
			want := prior
			if tc.actor == "" && tc.priorCount == 0 {
				want = input
			}
			if !slices.Equal(got.Runs, []CreateRunInput{want}) {
				t.Fatalf("saved plan did not return frozen inputs: %+v", got)
			}
		})
	}
}

func TestPostgresCancelledBatchCannotBeReopenedForFailures(t *testing.T) {
	batch := gradingSQLBatch(gradingSQLActor, "cancelled")
	db := imageTestDB(t, imageQuery("status <> 'cancelled'"), imageQuery("FROM subjective_grading_batch", batch...), imageQuery("FROM subjective_grading_batch", batch...))
	store := NewPostgresStore(db)
	if _, err := store.UpdateBatch(t.Context(), workerServiceTenant, gradingSQLBatchID, UpdateBatchInput{Status: "processing"}); !errors.Is(err, ErrBatchCancelled) {
		t.Fatalf("cancelled batch resumed: %v", err)
	}
	if _, err := store.FailedBatchSegments(t.Context(), workerServiceTenant, gradingSQLBatchID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cancelled batch could be used as retry source: %v", err)
	}
}

func TestPostgresFailedBatchSegmentsKeepsOpenReviewScope(t *testing.T) {
	batch := gradingSQLBatch(gradingSQLActor, "failed")
	db := imageTestDB(t, imageQuery("FROM subjective_grading_batch", batch...),
		imageQuery("latest.status IN ('failed','conflict')", gradingSQLSegmentID))
	got, err := NewPostgresStore(db).FailedBatchSegments(t.Context(), workerServiceTenant, gradingSQLBatchID)
	if err != nil || !slices.Equal(got, []string{gradingSQLSegmentID}) {
		t.Fatalf("failed segment lookup lost current retry scope: %v, %v", got, err)
	}
}
