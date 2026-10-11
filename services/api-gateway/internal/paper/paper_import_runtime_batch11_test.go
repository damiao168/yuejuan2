package paper

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func logPaperBatch11Proof(t *testing.T, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("BATCH11_PROOF=" + string(raw))
}

func TestBatch11ParseQueueValidatesSourceBindingBeforeInputWrite(t *testing.T) {
	for _, scenario := range []string{"wrong_document", "invalid_digest", "input_write_failure"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Unix(1, 0).UTC()
			job := PaperImportJob{ID: "import", Generation: 1, Sources: []PaperImportSource{{ID: "source", FileAssetID: "file", RoleHint: "auto"}}}
			revision := paperImportSourceConfigurationHash(job.Sources)
			steps := []paperBatch10Step{
				{kind: "query", fragment: "FROM paper_import_job WHERE", columns: 15, rows: [][]driver.Value{{"import", "tenant", "exam", "", "", "", "processing", "mathematics", []byte(`[]`), []byte(`[]`), "", "user", now, now, nil}}},
				{kind: "query", fragment: "FROM paper_import_source WHERE", columns: 8, rows: [][]driver.Value{{"source", "file", int64(0), "auto", "unknown", float64(0), "pending", now}}},
				{kind: "query", fragment: "FOR UPDATE OF job,run", columns: 4, rows: [][]driver.Value{{"run", int64(1), revision, "processing"}}},
				{kind: "exec", fragment: "SELECT t.id FROM agent_worker_task"},
			}
			input := PaperImportParseRequest{Documents: []PaperImportParseDocument{{SourceID: "source", FileAssetID: "file", Content: "题干"}}, Pages: []PaperImportDecodedPage{{SourceID: "source", PageNo: 1, FileAssetID: "page"}}}
			want := ErrInvalidInput
			if scenario == "wrong_document" {
				input.Documents[0].FileAssetID = "other"
			} else if scenario == "invalid_digest" {
				input.Pages[0].SHA256 = "invalid"
			} else {
				want = errors.New("input write failure")
				steps = append(steps, paperBatch10Step{kind: "query", fragment: "INSERT INTO paper_import_parse_input", err: want})
			}
			store, script := openPaperBatch10Store(t, steps)
			err := store.QueuePaperImportParse(context.Background(), "tenant", job, "user", input)
			if !errors.Is(err, want) {
				t.Fatalf("queue error = %v, want %v", err, want)
			}
			assertPaperBatch10Proof(t, script, "rollback", err.Error())
		})
	}
}

func TestBatch11DecodedVisualInputKeepsOrderAndRejectsDuplicateIdentity(t *testing.T) {
	job := PaperImportJob{Sources: []PaperImportSource{{ID: "text", FileAssetID: "text-file", DocumentIndex: 1}, {ID: "scan", FileAssetID: "scan-file", DocumentIndex: 0}}}
	base := []PaperImportParseDocument{{SourceID: "text", FileAssetID: "text-file", DocumentIndex: 1, Content: "答案"}}
	pages := []PaperImportDecodedPage{{SourceID: "scan", PageNo: 2, FileAssetID: "page-2"}, {SourceID: "scan", PageNo: 1, FileAssetID: "page-1"}}
	input, err := buildDecodedVisualParseInput(job, base, pages)
	if err != nil || input.Documents[0].SourceID != "scan" || input.Documents[1].Content != "答案" || input.Pages[0].PageNo != 1 || pages[0].PageNo != 2 || job.Sources[0].ID != "text" {
		t.Fatalf("decoded input changed order or source input: %#v err=%v", input, err)
	}
	logPaperBatch11Proof(t, input)
	for _, scenario := range []string{"duplicate_document", "duplicate_page", "missing_source", "wrong_page_index"} {
		t.Run(scenario, func(t *testing.T) {
			documents := append([]PaperImportParseDocument{}, base...)
			currentPages := append([]PaperImportDecodedPage{}, pages...)
			switch scenario {
			case "duplicate_document":
				documents = append(documents, base[0])
			case "duplicate_page":
				currentPages = append(currentPages, pages[0])
			case "missing_source":
				documents = nil
			case "wrong_page_index":
				currentPages[0].DocumentIndex = 1
			}
			_, err := buildDecodedVisualParseInput(job, documents, currentPages)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid visual input error = %v", err)
			}
			logPaperBatch11Proof(t, err.Error())
		})
	}
}

func TestBatch11FormulaValidationKeepsEvidenceBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*PaperImportFormulaRegion)
		want   bool
	}{
		{name: "accepted", want: true},
		{name: "review_without_candidate", want: true, change: func(r *PaperImportFormulaRegion) {
			r.Status, r.Candidates, r.SelectedLatex, r.SelectedModel = "review_required", nil, "", ""
		}},
		{name: "incomplete_crop", change: func(r *PaperImportFormulaRegion) { r.CropComplete = false }},
		{name: "invalid_box", change: func(r *PaperImportFormulaRegion) { r.BBox[2] = 0 }},
		{name: "nonfinite_box", change: func(r *PaperImportFormulaRegion) { r.BBox[0] = math.NaN() }},
		{name: "duplicate_model", change: func(r *PaperImportFormulaRegion) { r.Candidates = append(r.Candidates, r.Candidates[0]) }},
		{name: "invalid_action", change: func(r *PaperImportFormulaRegion) { r.Candidates[0].ValidationAction = "unknown" }},
		{name: "inconsistent_validity", change: func(r *PaperImportFormulaRegion) { r.Candidates[0].StructureValid = false }},
		{name: "selection_mismatch", change: func(r *PaperImportFormulaRegion) { r.SelectedLatex = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			region := acceptedFormulaRegion()
			if test.change != nil {
				test.change(&region)
			}
			got := validPaperFormulaResult([]PaperImportParseDocument{{SourceID: "source"}}, []PaperImportFormulaRegion{region})
			if got != test.want {
				t.Fatalf("formula validity = %v, want %v", got, test.want)
			}
			logPaperBatch11Proof(t, got)
		})
	}
}

func TestBatch11FormulaMergeKeepsDocumentIssueOrderAndOneInlineMerge(t *testing.T) {
	region := acceptedFormulaRegion()
	region.BBox = []float64{0, 20, 100, 20}
	input := PaperImportParseRequest{Documents: []PaperImportParseDocument{{SourceID: "source", FileAssetID: "file", Blocks: []PaperImportOCRBlock{{SourceID: "source", PageNo: 1, BlockID: "first", Text: "已知x2+1，求值", BBox: []float64{0, 20, 100, 20}}, {SourceID: "source", PageNo: 1, BlockID: "second", Text: "再取x2+1，求值", BBox: []float64{0, 20, 100, 20}}}}, {SourceID: "other", FileAssetID: "other-file"}}, ExtraIssues: []PaperImportIssue{{Code: "BASE"}}}
	other := region
	other.SourceID, other.RegionID, other.Status = "other", "other-region", "review_required"
	merged := mergePaperFormulaResult(input, []PaperImportFormulaRegion{other, region})
	blocks := merged.Documents[0].Blocks
	if len(blocks) != 2 || !strings.Contains(blocks[0].Text, `\(x^{2}+1\)`) || blocks[1].Text != "再取x2+1，求值" {
		t.Fatalf("region must inline only once: %#v", blocks)
	}
	if len(merged.ExtraIssues) != 2 || merged.ExtraIssues[0].Code != "BASE" || merged.ExtraIssues[1].SourceRefs[0].SourceID != "other" {
		t.Fatalf("document issue order changed: %#v", merged.ExtraIssues)
	}
	logPaperBatch11Proof(t, merged)
}

func TestBatch11ApproximateFormulaRangeKeepsFirstTieAndOriginalOffsets(t *testing.T) {
	value := "甲1234乙1234"
	start, end, ok := approximateFormulaRange(value, "1234")
	if !ok || start != len("甲") || value[start:end] != "1234" {
		t.Fatalf("formula range = %d %d %v", start, end, ok)
	}
	logPaperBatch11Proof(t, []any{start, end, ok, value[start:end]})
}

func TestBatch11LoadParseRunKeepsAddedSourceOrderAndHashGate(t *testing.T) {
	for _, scenario := range []string{"added_sources", "invalid_snapshot", "hash_mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			assertPaperParseRunSourceOrderAndHashGate(t, scenario)
		})
	}
}

func TestBatch11ReplaceSourceRestoreFailureRollsBackSoftDelete(t *testing.T) {
	steps := paperBatch10NewCommandSteps()
	steps = append(steps,
		paperBatch10Step{kind: "query", fragment: "SELECT exam_id::text FROM paper_import_job", columns: 1, rows: [][]driver.Value{{"exam"}}},
		paperBatch10Step{kind: "query", fragment: "SELECT status FROM exam", columns: 1, rows: [][]driver.Value{{"draft"}}},
		paperBatch10Step{kind: "query", fragment: "SELECT status,current_generation", columns: 2, rows: [][]driver.Value{{"processing", int64(1)}}},
		paperBatch10Step{kind: "query", fragment: "SELECT id::text FROM paper_import_source", columns: 1, rows: [][]driver.Value{{"source"}}},
		paperBatch10Step{kind: "exec", fragment: "UPDATE paper_import_source SET deleted_at"},
		paperBatch10Step{kind: "exec", fragment: "UPDATE paper_import_source SET document_index", result: driver.RowsAffected(0)},
	)
	store, script := openPaperBatch10Store(t, steps)
	_, err := store.ReplacePaperImportSources(context.Background(), "tenant", "import", "user", ReplacePaperImportSourcesInput{CommandID: "command", ExpectedGeneration: 1, Sources: []ReplacePaperImportSourceInput{{ID: "source", RoleHint: "auto"}}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("source restore failure error = %v", err)
	}
	assertPaperBatch10Proof(t, script, "rollback", err.Error())
}

func assertPaperParseRunSourceOrderAndHashGate(t *testing.T, scenario string) {
	t.Helper()
	input := PaperImportParseRequest{Documents: []PaperImportParseDocument{{SourceID: "new-b", Content: "B"}, {SourceID: "old", Content: "old"}, {SourceID: "new-a", Content: "A"}}}
	documents, _ := json.Marshal(input.Documents)
	serialized, _ := json.Marshal(input)
	hash := contentHash(serialized)
	snapshot := []byte(`[{"id":"old"}]`)
	if scenario == "invalid_snapshot" {
		snapshot = []byte(`invalid`)
	} else if scenario == "hash_mismatch" {
		hash = "different"
	}
	store, script := openPaperBatch10Store(t, []paperBatch10Step{{kind: "query", fragment: "FROM paper_import_parse_input input", columns: 10, rows: [][]driver.Value{{"import", "run", int64(2), "revision", hash, documents, []byte(`null`), []byte(`null`), "add_sources", snapshot}}}})
	binding, err := store.LoadPaperImportParseRunInput(context.Background(), "tenant", "input")
	if scenario == "added_sources" {
		if err != nil || binding.InputID != "input" || !reflect.DeepEqual(binding.NewSourceIDs, []string{"new-b", "new-a"}) {
			t.Fatalf("added sources lost order or binding: %#v err=%v", binding, err)
		}
		logPaperBatch11Proof(t, binding)
	} else {
		want := ErrConflict
		if scenario == "invalid_snapshot" {
			want = ErrInvalidInput
		}
		if !errors.Is(err, want) || binding.ImportID != "" {
			t.Fatalf("failed load exposed binding: %#v err=%v", binding, err)
		}
		logPaperBatch11Proof(t, err.Error())
	}
	assertPaperBatch10Proof(t, script, "", scenario)
}
