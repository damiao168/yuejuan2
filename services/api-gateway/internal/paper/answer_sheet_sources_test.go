package paper

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestAnswerSheetSourceDeletionProtectsReferencesAndRemovesDraftsAtomically(t *testing.T) {
	for _, scenario := range []string{"unused", "drafts", "locked", "library source", "library instance", "bound", "questions", "import", "extra import source", "frozen", "other tenant", "other exam", "question paper"} {
		t.Run(scenario, func(t *testing.T) {
			s := NewMemoryStore()
			s.papers["source"] = Paper{ID: "source", TenantID: "tenant", ExamID: "exam", FileAssetID: "asset", Purpose: PaperPurposeAnswerSheet}
			s.templates["draft"] = AnswerSheetTemplate{ID: "draft", TenantID: "tenant", ExamID: "exam", ExamPaperID: "source", Status: "draft"}
			s.papers["other"] = Paper{ID: "other", TenantID: "tenant", ExamID: "exam", Purpose: PaperPurposeAnswerSheet}
			want := error(nil)
			tenantID, examID := "tenant", "exam"
			switch scenario {
			case "unused":
				delete(s.templates, "draft")
			case "locked":
				s.templates["locked"] = AnswerSheetTemplate{ID: "locked", TenantID: "tenant", ExamPaperID: "source", Status: "locked"}
				want = ErrAnswerSheetSourceInUse
			case "library source":
				s.librarySources["library"] = templateLibrarySource{TemplateID: "draft"}
				want = ErrAnswerSheetSourceInUse
			case "library instance":
				s.libraryInstances["exam/library"] = "draft"
				want = ErrAnswerSheetSourceInUse
			case "bound":
				s.bindings["exam"] = ExamTemplateBinding{TenantID: "tenant", TemplateID: "draft"}
				want = ErrAnswerSheetSourceInUse
			case "questions":
				s.questions["q"] = Question{TenantID: "tenant", ExamPaperID: "source"}
				want = ErrAnswerSheetSourceInUse
			case "import":
				s.imports["job"] = PaperImportJob{TenantID: "tenant", AnswerFileAssetID: "asset"}
				want = ErrAnswerSheetSourceInUse
			case "extra import source":
				s.imports["job"] = PaperImportJob{TenantID: "tenant", Sources: []PaperImportSource{{FileAssetID: "asset"}}}
				want = ErrAnswerSheetSourceInUse
			case "frozen":
				s.SetReadinessContext("exam", 100, 1, 1, "collecting")
				want = ErrExamFrozen
			case "other tenant":
				tenantID, want = "foreign", ErrNotFound
			case "other exam":
				examID, want = "foreign", ErrNotFound
			case "question paper":
				item := s.papers["source"]
				item.Purpose = PaperPurposeQuestion
				s.papers[item.ID] = item
				want = ErrNotFound
			}
			beforeTemplates := len(s.templates)
			result, err := s.DeleteAnswerSheetSource(context.Background(), tenantID, examID, "source")
			if !errors.Is(err, want) {
				t.Fatalf("deletion error=%v, want %v", err, want)
			}
			if want != nil {
				if len(s.templates) != beforeTemplates || len(s.papers) != 2 {
					t.Fatal("rejected deletion partially changed draft/source records")
				}
				return
			}
			if !result.Deleted || result.PaperID != "source" || len(result.DeletedTemplateIDs) != beforeTemplates || len(s.templates) != 0 || len(s.papers) != 1 || s.papers["other"].ID != "other" {
				t.Fatalf("deletion changed wrong records: %+v", result)
			}
		})
	}
}

func TestAnswerSheetSourceDeleteHTTPReturnsConflictAndDraftIDs(t *testing.T) {
	for _, locked := range []bool{false, true} {
		s := NewMemoryStore()
		s.papers["source"] = Paper{ID: "source", TenantID: "tenant", ExamID: "exam", Purpose: PaperPurposeAnswerSheet}
		s.templates["draft"] = AnswerSheetTemplate{ID: "draft", TenantID: "tenant", ExamPaperID: "source", Status: "draft"}
		if locked {
			item := s.templates["draft"]
			item.Status = "locked"
			s.templates[item.ID] = item
		}
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/exams/exam/papers/source", nil)
		r.SetPathValue("examId", "exam")
		r.SetPathValue("id", "source")
		r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "admin", TenantID: "tenant"}))
		w := httptest.NewRecorder()
		NewHandler(s, nil).DeleteAnswerSheetSource(w, r)
		if locked && (w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"answer_sheet_source_in_use"`)) {
			t.Fatalf("locked deletion response: %d %s", w.Code, w.Body.String())
		}
		if !locked && (w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted_template_ids":["draft"]`)) {
			t.Fatalf("draft deletion response: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestAnswerSheetDuplicateHashReusesSourceWithoutRenamingOrCrossExamReuse(t *testing.T) {
	s := NewMemoryStore()
	input := CreatePaperInput{Purpose: PaperPurposeAnswerSheet, File: FileAssetInput{OriginalName: "原答题卡.pdf", HashSHA256: strings.Repeat("a", 64)}}
	first, err := s.CreatePaper(t.Context(), "tenant", "exam", "admin", input)
	if err != nil {
		t.Fatal(err)
	}
	input.File.OriginalName = "重新命名.pdf"
	input.FileAssetID = "another-upload"
	duplicate, err := s.CreatePaper(t.Context(), "tenant", "exam", "admin", input)
	if err != nil || duplicate.ID != first.ID || !duplicate.Duplicate || duplicate.File.OriginalName != "原答题卡.pdf" || len(s.papers) != 1 {
		t.Fatalf("same bytes created/renamed source: %+v %v", duplicate, err)
	}
	other, err := s.CreatePaper(t.Context(), "tenant", "other-exam", "admin", input)
	if err != nil || other.ID == first.ID || other.Duplicate {
		t.Fatalf("cross-exam duplicate leaked: %+v %v", other, err)
	}
}

func TestAnswerSheetSourceDeletePostgresRollbackProtectsAllDraftChanges(t *testing.T) {
	for _, scenario := range []string{"success", "protected", "write failure"} {
		t.Run(scenario, func(t *testing.T) {
			steps := []paperBatch10Step{
				{kind: "exec", fragment: "pg_advisory_xact_lock"},
				{kind: "query", fragment: "FROM exam_paper", columns: 1, rows: [][]driver.Value{{"asset"}}},
				{kind: "query", fragment: "FROM answer_sheet_template", columns: 3, rows: [][]driver.Value{{"draft", "draft", false}}},
				{kind: "query", fragment: "SELECT status FROM exam", columns: 1, rows: [][]driver.Value{{"configured"}}},
				{kind: "query", fragment: "FROM exam_paper", columns: 1, rows: [][]driver.Value{{"asset"}}},
				{kind: "query", fragment: "EXISTS(SELECT 1 FROM question", columns: 1, rows: [][]driver.Value{{scenario == "protected"}}},
			}
			if scenario != "protected" {
				steps = append(steps, paperBatch10Step{kind: "exec", fragment: "UPDATE answer_sheet_template"})
				step := paperBatch10Step{kind: "exec", fragment: "UPDATE exam_paper"}
				if scenario == "write failure" {
					step.err = errors.New("write failed")
				}
				steps = append(steps, step)
			}
			script := &paperBatch10Script{steps: steps}
			db := sql.OpenDB(paperBatch10Connector{script: script})
			defer db.Close()
			result, err := NewPostgresStore(db).DeleteAnswerSheetSource(t.Context(), "tenant", "exam", "source")
			if script.unmatched != nil || script.next != len(steps) {
				t.Fatalf("unexpected SQL: %v %+v", script.unmatched, script.events)
			}
			last := script.events[len(script.events)-1]
			if scenario == "success" && (err != nil || !result.Deleted || last != "commit") {
				t.Fatalf("deletion did not commit: %+v %v %s", result, err, last)
			}
			if scenario != "success" && (err == nil || last != "rollback") {
				t.Fatalf("deletion did not roll back: %v %s", err, last)
			}
		})
	}
}
