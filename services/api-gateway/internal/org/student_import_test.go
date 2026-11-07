package org

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func studentImportMemoryFixture() *MemoryStore {
	store := NewMemoryStore()
	store.schools["school-a"] = School{ID: "school-a", TenantID: "tenant", Status: "active"}
	store.schools["school-b"] = School{ID: "school-b", TenantID: "tenant", Status: "active"}
	store.classes["class-a"] = Class{ID: "class-a", TenantID: "tenant", SchoolID: "school-a", Status: "active"}
	store.classes["class-b"] = Class{ID: "class-b", TenantID: "tenant", SchoolID: "school-b", Status: "active"}
	return store
}

func importStudentRow(number int, no, name, schoolID, classID string) StudentImportRow {
	return StudentImportRow{Row: number, Student: Student{StudentNo: no, Name: name, SchoolID: schoolID, ClassID: classID, Status: "active"}}
}

func TestAtomicStudentImportRejectsWholeBatchOnConflict(t *testing.T) {
	store := studentImportMemoryFixture()
	store.students["existing"] = Student{ID: "existing", TenantID: "tenant", StudentNo: "00123", Name: "原姓名", SchoolID: "school-a", ClassID: "class-a", Status: "active"}
	result, err := store.ImportStudentsAtomic(t.Context(), "tenant", []StudentImportRow{
		importStudentRow(2, "00124", "新学生", "school-a", "class-a"),
		importStudentRow(3, "00123", "不同姓名", "school-a", "class-a"),
	})
	if err != nil || result.Committed || result.Created != 0 || result.Skipped != 0 || len(result.Errors) != 1 || result.Errors[0].Row != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(store.students) != 1 || store.students["existing"].Name != "原姓名" {
		t.Fatal("rejected batch changed students")
	}
}

func TestAtomicStudentImportRetriesPreserveLeadingZerosAndSkipIdenticalRows(t *testing.T) {
	store := studentImportMemoryFixture()
	input := []StudentImportRow{importStudentRow(2, "00123", "合成学生", "school-a", "class-a"), importStudentRow(3, "00123", "合成学生", "school-a", "class-a")}
	first, err := store.ImportStudentsAtomic(t.Context(), "tenant", input)
	if err != nil || !first.Committed || first.Created != 1 || first.Skipped != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	again, err := store.ImportStudentsAtomic(t.Context(), "tenant", input)
	if err != nil || !again.Committed || again.Created != 0 || again.Skipped != 2 {
		t.Fatalf("again=%+v err=%v", again, err)
	}
	for _, student := range store.students {
		if student.StudentNo != "00123" {
			t.Fatalf("leading zeros lost: %+v", student)
		}
	}
}

func TestAtomicStudentImportDoesNotTransferOrReactivateExistingStudents(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Student)
	}{
		{"inactive", func(s *Student) { s.Status = "inactive" }},
		{"different class", func(s *Student) { s.ClassID = "old-class" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := studentImportMemoryFixture()
			student := Student{ID: "existing", TenantID: "tenant", StudentNo: "1", Name: "合成学生", SchoolID: "school-a", ClassID: "class-a", Status: "active"}
			tc.mutate(&student)
			store.students[student.ID] = student
			result, err := store.ImportStudentsAtomic(t.Context(), "tenant", []StudentImportRow{importStudentRow(2, "1", "合成学生", "school-a", "class-a")})
			if err != nil || result.Committed || len(result.Errors) != 1 || store.students[student.ID] != student {
				t.Fatalf("result=%+v err=%v student=%+v", result, err, store.students[student.ID])
			}
		})
	}
}

func TestAtomicStudentImportRejectsSchoolMismatchAndConflictingFileDuplicates(t *testing.T) {
	for _, input := range [][]StudentImportRow{
		{importStudentRow(2, "1", "合成学生", "school-a", "class-b")},
		{importStudentRow(2, "1", "合成学生", "school-a", "class-a"), importStudentRow(3, "1", "同号异名", "school-a", "class-a")},
		{importStudentRow(2, "1", "合成学生", "other-school", "class-a")},
	} {
		store := studentImportMemoryFixture()
		result, err := store.ImportStudentsAtomic(t.Context(), "tenant", input)
		if err != nil || result.Committed || len(result.Errors) == 0 || len(store.students) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	store := studentImportMemoryFixture()
	result, err := store.ImportStudentsAtomic(t.Context(), "other-tenant", []StudentImportRow{importStudentRow(2, "1", "合成学生", "school-a", "class-a")})
	if err != nil || result.Committed || len(store.students) != 0 {
		t.Fatalf("cross tenant result=%+v err=%v", result, err)
	}
}

func TestAtomicStudentImportConcurrentRetriesCreateOnce(t *testing.T) {
	store := studentImportMemoryFixture()
	input := []StudentImportRow{importStudentRow(2, "00123", "合成学生", "school-a", "class-a")}
	var wg sync.WaitGroup
	results := make(chan CSVImportResult, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := store.ImportStudentsAtomic(context.Background(), "tenant", input)
			if err != nil {
				t.Error(err)
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	created, skipped := 0, 0
	for result := range results {
		if !result.Committed {
			t.Fatalf("concurrent import rejected: %+v", result)
		}
		created += result.Created
		skipped += result.Skipped
	}
	if created != 1 || skipped != 1 || len(store.students) != 1 {
		t.Fatalf("created=%d skipped=%d students=%d", created, skipped, len(store.students))
	}
}

func importCSVRequest(t *testing.T, store *MemoryStore, body string, schoolAdmin bool) (int, CSVImportResult, string) {
	t.Helper()
	user := auth.User{ID: "admin", TenantID: "tenant", Roles: []string{"tenant_admin"}, DataScope: map[string]any{"scope": "tenant"}}
	if schoolAdmin {
		user.Roles = []string{"school_admin"}
		user.DataScope = map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}
	}
	scope, err := auth.ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	audit := auth.NewMemoryStore()
	for _, class := range store.classes {
		audit.AddResourceBoundary("school_class", class.ID, auth.ResourceBoundary{TenantID: class.TenantID, SchoolID: class.SchoolID})
	}
	h := NewHandler(store, audit)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/students/import-csv", strings.NewReader(body))
	req = req.WithContext(auth.WithAccessScope(auth.WithUser(req.Context(), user), scope))
	rec := httptest.NewRecorder()
	h.ImportStudentsCSV(rec, req)
	var response struct {
		Result CSVImportResult `json:"result"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &response)
	return rec.Code, response.Result, rec.Body.String()
}

func TestStudentImportHandlerPreflightsAllRowsBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		schoolAdmin bool
		status      int
	}{
		{"late malformed record", "student_no,name,school_id,class_id\n00123,合成学生,school-a,class-a\nbad,row\n", false, http.StatusOK},
		{"late malformed quotes", "student_no,name,school_id,class_id\n00123,合成学生,school-a,class-a\n\"unfinished", false, http.StatusBadRequest},
		{"mixed school scope", "student_no,name,school_id,class_id\n00123,合成学生,school-a,class-a\n00124,校外学生,school-b,class-b\n", true, http.StatusOK},
		{"empty header", "student_no,name,school_id,class_id\n", false, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := studentImportMemoryFixture()
			status, result, body := importCSVRequest(t, store, tc.body, tc.schoolAdmin)
			if status != tc.status || result.Committed || result.Created != 0 || len(store.students) != 0 {
				t.Fatalf("status=%d result=%+v body=%s", status, result, body)
			}
		})
	}
}

func TestStudentImportHandlerAcceptsBOMAndReportsRepeat(t *testing.T) {
	store := studentImportMemoryFixture()
	body := "\ufeffstudent_no,name,school_id,class_id\n00123,合成学生,school-a,class-a\n"
	for attempt := 0; attempt < 2; attempt++ {
		status, result, raw := importCSVRequest(t, store, body, false)
		if status != http.StatusOK || !result.Committed || len(result.Errors) != 0 || result.Created != 1-attempt || result.Skipped != attempt {
			t.Fatalf("attempt=%d status=%d result=%+v body=%s", attempt, status, result, raw)
		}
	}
}

func TestStudentImportHandlerRejectsOversizedBatchBeforeWriting(t *testing.T) {
	store := studentImportMemoryFixture()
	body := "student_no,name,school_id,class_id\n" + strings.Repeat("1,合成学生,school-a,class-a\n", studentImportMaxRows+1)
	status, _, raw := importCSVRequest(t, store, body, false)
	if status != http.StatusRequestEntityTooLarge || len(store.students) != 0 {
		t.Fatalf("status=%d students=%d body=%s", status, len(store.students), raw)
	}
}
