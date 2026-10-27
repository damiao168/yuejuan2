package questionbank

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func currentImportSnapshot(t *testing.T, version int, mode string) (frozenImportSnapshot, frozenImportQuestion) {
	t.Helper()
	q := frozenImportQuestion{ID: batch22ItemID, AssessmentSnapshotID: batch22VersionID, AssessmentSnapshotHash: strings.Repeat("a", 64), QuestionNo: "18(1)", ParentQuestionNo: "18", SubquestionNo: "1", QuestionType: "single_choice", AssessmentArchetype: "selected_response", Score: 2, Stem: "Frozen source", Options: []string{"甲", "乙", "丙"}, KnowledgePoints: []string{"frozen"}, Answer: &paper.ReadinessAnswerSnapshot{StandardAnswer: "B", EquivalentAnswers: []any{}, Tolerance: map[string]any{}}}
	if version == 2 {
		q.ReferenceMaterials = &paper.ReferenceMaterialBinding{ID: batch22ExamID, Mode: mode, Revision: 1, ContentHash: strings.Repeat("b", 64)}
		if mode == "reference_images" {
			q.ReferenceMaterials.ImageCount = 1
		}
	}
	return frozenImportSnapshot{Version: version, ConfigurationHash: strings.Repeat("c", 64), Questions: []frozenImportQuestion{q}}, q
}

func sourceSnapshotSteps(t *testing.T, snapshot frozenImportSnapshot) []batch22Step {
	t.Helper()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return []batch22Step{
		batch22BankStep(batch22BankID, "active"),
		{kind: "query", fragment: "FROM exam_readiness_snapshot", columns: 5, rows: [][]driver.Value{{batch22ExamID, snapshot.ConfigurationHash, int64(snapshot.Version), hashJSON(snapshot), raw}}},
	}
}

func TestPrepareImportAcceptsCurrentFrozenContractAndPreservesOptions(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			snapshot, q := currentImportSnapshot(t, version, "text_only")
			steps := sourceSnapshotSteps(t, snapshot)
			steps = append(steps,
				batch22Step{kind: "query", fragment: "FROM exam_question_snapshot", columns: 10, rows: [][]driver.Value{{q.AssessmentSnapshotID, int64(1), []byte(`{"subject_code":"mathematics","education_stage":"junior"}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), q.AssessmentSnapshotHash, "selected_response", "mathematics", "junior"}}},
				batch22BankStep(batch22BankID, "active"),
				batch22Step{kind: "query", fragment: "FROM question_bank_metadata_schema", columns: 6, rows: [][]driver.Value{{batch22BankID, int64(1), []byte(`[]`), []byte(`[]`), "actor", time.Unix(0, 0)}}},
			)
			store, script := openBatch22Store(t, steps)
			selection := ImportPreviewSelection{QuestionID: q.ID, TargetBankID: batch22BankID, SourceSnapshotID: batch22VersionID, Mapping: ImportMapping{ItemCode: "IMPORTED", GradeScope: "grade_8", Copyright: "owned"}}
			prepared, err := store.prepareImport(context.Background(), store.db, batch22Scope(), selection, "")
			if err != nil || !reflect.DeepEqual(prepared.Content.Options, q.Options) || prepared.Scoring.Answer.StandardAnswer != "B" || !prepared.ScoringConsistent {
				t.Fatalf("current v%d snapshot failed: content=%#v scoring=%#v err=%v", version, prepared.Content, prepared.Scoring, err)
			}
			if script.err != nil || script.next != len(steps) || script.opened != script.closed {
				t.Fatalf("SQL script incomplete: %#v", script)
			}
		})
	}
}

func TestFrozenContractHashKeepsAllProducerFields(t *testing.T) {
	snapshot, q := currentImportSnapshot(t, 2, "text_only")
	q.Rubric = &paper.ReadinessRubricSnapshot{Status: "approved", MaxScore: 2, Points: []paper.RubricPoint{{ID: "p1", Score: 2, Description: "Frozen point"}}, Deductions: []any{}, Examples: []any{}}
	q.Solution = &paper.ReadinessSolutionSnapshot{RawText: "Frozen solution", Steps: []paper.SolutionStep{}, VerificationStatus: "human_confirmed"}
	snapshot.Questions[0] = q
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded frozenImportSnapshot
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if hashJSON(snapshot) != hashJSON(decoded) || decoded.Questions[0].Rubric.Status != "approved" || decoded.Questions[0].ParentQuestionNo != "18" || decoded.Questions[0].SubquestionNo != "1" || decoded.Questions[0].ReferenceMaterials.ContentHash != q.ReferenceMaterials.ContentHash {
		t.Fatalf("producer fields dropped in frozen contract: %#v", decoded)
	}
	decoded.Questions[0].Options[0] = "changed"
	if hashJSON(snapshot) == hashJSON(decoded) {
		t.Fatal("tampered options did not change frozen digest")
	}
}

func TestFrozenReferenceImagesRequireAvailableExactManifest(t *testing.T) {
	snapshot, q := currentImportSnapshot(t, 2, "reference_images")
	steps := sourceSnapshotSteps(t, snapshot)
	steps = append(steps,
		batch22Step{kind: "query", fragment: "FROM exam_question_snapshot", columns: 10, rows: [][]driver.Value{{q.AssessmentSnapshotID, int64(1), []byte(`{"subject_code":"mathematics","education_stage":"junior"}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), q.AssessmentSnapshotHash, "selected_response", "mathematics", "junior"}}},
		batch22Step{kind: "query", fragment: "FROM question_reference_manifest", columns: 8},
	)
	store, script := openBatch22Store(t, steps)
	_, err := store.prepareImport(context.Background(), store.db, batch22Scope(), ImportPreviewSelection{QuestionID: q.ID, TargetBankID: batch22BankID, SourceSnapshotID: batch22VersionID}, "")
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("image-bound source lost its transfer boundary: %v", err)
	}
	code, retry := importErrorCode(err)
	if code != "source_snapshot_unavailable" || retry || script.next != len(steps) {
		t.Fatalf("unexpected transfer result: %s/%v steps=%d", code, retry, script.next)
	}
}

func TestPrepareImportRejectsTamperedOrMismatchedSnapshotVersion(t *testing.T) {
	for _, change := range []string{"options", "embedded version", "storage version"} {
		t.Run(change, func(t *testing.T) {
			snapshot, q := currentImportSnapshot(t, 1, "")
			steps := sourceSnapshotSteps(t, snapshot)
			switch change {
			case "options":
				snapshot.Questions[0].Options[0] = "tampered"
				raw, _ := json.Marshal(snapshot)
				steps[1].rows[0][4] = raw
			case "embedded version":
				snapshot.Version = 2
				raw, _ := json.Marshal(snapshot)
				steps[1].rows[0][3], steps[1].rows[0][4] = hashJSON(snapshot), raw
			case "storage version":
				steps[1].rows[0][2] = int64(3)
			}
			store, script := openBatch22Store(t, steps)
			_, err := store.prepareImport(context.Background(), store.db, batch22Scope(), ImportPreviewSelection{QuestionID: q.ID, TargetBankID: batch22BankID, SourceSnapshotID: batch22VersionID}, "")
			if !errors.Is(err, ErrSourceUnavailable) || script.next != len(steps) {
				t.Fatalf("invalid frozen source accepted: %v steps=%d", err, script.next)
			}
		})
	}
}

func TestMaterializationPassesFormalOptionsToQuestionAndReply(t *testing.T) {
	options := []string{"two", "three"}
	optionJSON, _ := json.Marshal(options)
	store, script := openBatch22Store(t, []batch22Step{
		{kind: "query", fragment: "SELECT status FROM exam", columns: 1, rows: [][]driver.Value{{"draft"}}},
		{kind: "query", fragment: "INSERT INTO question(", columns: 12, rows: [][]driver.Value{{"question", "tenant", batch22ExamID, "", "9", "single_choice", float64(2), "stem", []byte("[]"), nil, int64(2), "active"}}, args: map[int]any{13: optionJSON}},
		{kind: "exec", fragment: "SELECT assessment_apply_default_question_config"},
		{kind: "exec", fragment: "UPDATE question_assessment_config"},
	})
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	content := fixtureContent()
	content.Options = options
	out := MaterializeResult{ExamID: batch22ExamID}
	err = materializeQuestions(context.Background(), tx, batch22Scope(), []MaterializeSelection{{VersionID: batch22VersionID, QuestionNo: "9", SortOrder: 2}}, map[string]Version{batch22VersionID: {ID: batch22VersionID, ItemID: batch22ItemID, Content: content}}, &out)
	if err != nil || len(out.Questions) != 1 || !reflect.DeepEqual(out.Questions[0].Options, options) {
		t.Fatalf("formal options missing: %#v err=%v", out, err)
	}
	if script.err != nil {
		t.Fatal(script.err)
	}
}
