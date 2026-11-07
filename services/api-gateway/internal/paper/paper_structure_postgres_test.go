package paper

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestBatch37PostgresStructureReadFailuresStopAndRollback(t *testing.T) {
	job, qs := structureBatch37Fixture()
	base := structureBatch37ReadSteps(t, job, qs)
	for index := range base {
		t.Run(fmt.Sprintf("query_%02d", index), func(t *testing.T) {
			failure := errors.New("read failure")
			steps := append([]structureBatch37Step{}, base[:index+1]...)
			steps[index].err = failure
			store, script := openStructureBatch37Store(t, steps)
			out, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
			if !errors.Is(err, failure) || !reflect.DeepEqual(out, PaperStructurePreview{}) {
				t.Fatalf("read error=%v preview=%#v", err, out)
			}
			assertStructureBatch37Script(t, script, "rollback", err.Error())
		})
	}
	for _, index := range []int{5, 6, 7} {
		t.Run(fmt.Sprintf("rows_err_%02d", index), func(t *testing.T) {
			failure := errors.New("rows iteration failure")
			steps := append([]structureBatch37Step{}, base[:index+1]...)
			steps[index].rowErr = failure
			store, script := openStructureBatch37Store(t, steps)
			_, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
			if !errors.Is(err, failure) {
				t.Fatalf("rows error=%v", err)
			}
			assertStructureBatch37Script(t, script, "rollback", err.Error())
		})
	}
	for _, index := range []int{5, 6} {
		t.Run(fmt.Sprintf("scan_err_%02d", index), func(t *testing.T) {
			steps := append([]structureBatch37Step{}, base[:index+1]...)
			steps[index].rows = [][]driver.Value{make([]driver.Value, steps[index].columns)}
			store, script := openStructureBatch37Store(t, steps)
			_, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
			if err == nil || !strings.Contains(err.Error(), "Scan error") {
				t.Fatalf("nil source/lock Scan error: %v", err)
			}
			assertStructureBatch37Script(t, script, "rollback", err.Error())
		})
	}
}

func TestBatch37PostgresStructureReceiptAndJSONPrecedence(t *testing.T) {
	for _, test := range []paperStructureReceiptCase{
		{name: "run_status", column: 4, value: "processing"},
		{name: "result_generation_missing", column: 1},
		{name: "result_generation_old", column: 1, value: int64(1)},
		{name: "job_task_missing", column: 2, value: ""},
		{name: "run_task_other", column: 6, value: "other"},
		{name: "job_hash_missing", column: 3, value: ""},
		{name: "run_hash_other", column: 7, value: "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertPaperStructureReceiptFailureOrder(t, test)
		})
	}
	t.Run("job_status", func(t *testing.T) {
		job, qs := structureBatch37Fixture()
		job.Status = "processing"
		steps := structureBatch37ReadSteps(t, job, qs)[:2]
		store, script := openStructureBatch37Store(t, steps)
		_, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("job status error=%v", err)
		}
		assertStructureBatch37Script(t, script, "rollback", err.Error())
	})
	t.Run("legacy_without_receipts", func(t *testing.T) {
		job, qs := structureBatch37Fixture()
		steps := structureBatch37ReadSteps(t, job, qs)
		steps[1].rows[0] = []driver.Value{job.Generation, nil, "", "", "review_required", "legacy-unverified", "", "", int64(0)}
		store, script := openStructureBatch37Store(t, steps)
		out, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
		if err != nil || out.RecognizedCount != 1 {
			t.Fatalf("legacy preview=%#v err=%v", out, err)
		}
		assertStructureBatch37Script(t, script, "rollback", out)
	})
	for _, candidatesBad := range []bool{true, false} {
		t.Run(fmt.Sprintf("candidate_json_first_%v", candidatesBad), func(t *testing.T) {
			job, qs := structureBatch37Fixture()
			steps := structureBatch37ReadSteps(t, job, qs)[:5]
			steps[4].rows[0][1] = []byte(`{`)
			if candidatesBad {
				steps[4].rows[0][0] = []byte(`[`)
			}
			store, script := openStructureBatch37Store(t, steps)
			_, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
			var syntax *json.SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("JSON error=%v", err)
			}
			assertStructureBatch37Script(t, script, "rollback", err.Error())
		})
	}
}

func TestBatch37PostgresStructurePresetAndPublishedBoundaries(t *testing.T) {
	for _, test := range []struct {
		name        string
		preset      float64
		missing     bool
		published   bool
		draftScore  float64
		wantScore   float64
		wantBlocked bool
	}{
		{name: "same_preset_is_not_human", preset: 7, draftScore: 5, wantScore: 5},
		{name: "changed_preset_is_human", preset: 5, draftScore: 5, wantScore: 7},
		{name: "missing_preset_is_not_zero", missing: true, draftScore: 5, wantScore: 5},
		{name: "published_change_blocks", preset: 7, published: true, draftScore: 5, wantScore: 5, wantBlocked: true},
		{name: "published_same_structure_keeps", preset: 7, published: true, draftScore: 7, wantScore: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			job, qs := structureBatch37Fixture()
			qs[0].ScoreSource, qs[0].Score = "preset", 7
			job.Questions[0].Score = test.draftScore
			steps := structureBatch37ReadSteps(t, job, qs)
			presetIndex := len(steps) - 2
			steps[presetIndex].rows = [][]driver.Value{{test.preset}}
			if test.missing {
				steps[presetIndex].rows = nil
			}
			steps[len(steps)-1].rows = [][]driver.Value{{test.published}}
			store, script := openStructureBatch37Store(t, steps)
			out, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
			if err != nil || out.RecognizedTotal != test.wantScore || (len(out.BlockedReasons) > 0) != test.wantBlocked {
				t.Fatalf("preset/published result=%#v err=%v", out, err)
			}
			assertStructureBatch37Script(t, script, "rollback", out)
		})
	}
}

func structureBatch37Input(job PaperImportJob, qs []Question) AdoptPaperStructureInput {
	preview, _ := planPaperStructure(job, append([]Question{}, qs...), 10)
	return AdoptPaperStructureInput{ExpectedGeneration: job.Generation, PreviewHash: preview.PreviewHash, CompletePaperConfirmed: true}
}

func TestBatch37PostgresStructureAdoptionGuardsDoNotWrite(t *testing.T) {
	for _, test := range []paperStructureAdoptionGuardCase{
		{name: "invalid_before_begin", invalid: true, want: ErrInvalidInput},
		{name: "generation_before_blocked", oldGeneration: true, blocked: true, want: ErrConflict},
		{name: "hash_before_blocked", staleHash: true, blocked: true, want: ErrConflict},
		{name: "blocked_before_confirmed", blocked: true, confirmed: true, want: ErrInvalidInput},
		{name: "confirmed_fresh_rolls_back", confirmed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertPaperStructureAdoptionWithoutWrite(t, test)
		})
	}
	for _, beginFailure := range []bool{true, false} {
		t.Run(fmt.Sprintf("begin_or_missing_%v", beginFailure), func(t *testing.T) {
			job, qs := structureBatch37Fixture()
			steps := structureBatch37ReadSteps(t, job, qs)[:1]
			steps[0].rows = nil
			end, want := "rollback", ErrNotFound
			if beginFailure {
				steps = nil
				end, want = "begin", errors.New("begin failed")
			}
			store, script := openStructureBatch37Store(t, steps)
			if beginFailure {
				script.beginErr = want
			}
			out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", structureBatch37Input(job, qs))
			if !errors.Is(err, want) || !reflect.DeepEqual(out, PaperStructurePreview{}) {
				t.Fatalf("begin/missing error=%v preview=%#v", err, out)
			}
			assertStructureBatch37Script(t, script, end, err.Error())
		})
	}
}

func structureBatch37FinalSteps(job PaperImportJob) []structureBatch37Step {
	return []structureBatch37Step{
		{kind: "exec", fragment: "UPDATE paper_import_job SET structure_confirmed_generation", args: []driver.Value{"tenant", "import", "user"}},
		{kind: "exec", fragment: "UPDATE exam_blueprint_section SET deleted_at", args: []driver.Value{"tenant", job.ExamID}},
	}
}

func structureBatch37NewQuestionSteps(t *testing.T, job PaperImportJob) []structureBatch37Step {
	t.Helper()
	draft := job.Questions[0]
	return []structureBatch37Step{
		{kind: "query", fragment: "INSERT INTO question(tenant_id,exam_id", args: []driver.Value{"tenant", job.ExamID, job.ExamPaperID, draft.QuestionNo, draft.QuestionType, draft.Score, draft.Stem, structureBatch37JSON(t, wireList(draft.Options)), structureBatch37JSON(t, wireList(draft.KnowledgePoints)), int64(1), "import", draft.CandidateID, structureBatch37JSON(t, wireList(draft.SourceRefs)), draft.ParentQuestionNo, draft.SubquestionNo}, columns: 1, rows: [][]driver.Value{{"new-question"}}},
		{kind: "exec", fragment: "UPDATE question SET score_source", args: []driver.Value{"tenant", "new-question", "material"}},
		{kind: "exec", fragment: "UPDATE question SET assessment_archetype", args: []driver.Value{"tenant", "new-question", "structured_steps"}},
		{kind: "exec", fragment: "SELECT assessment_apply_default_question_config", args: []driver.Value{"tenant", job.ExamID, "new-question"}},
		{kind: "exec", fragment: "UPDATE question_assessment_config c", args: []driver.Value{"tenant", job.ExamID, "new-question", "structured_steps"}},
	}
}
