package org

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPostgresAtomicStudentImportRetriesAndRejectsWholeConflictingBatch(t *testing.T) {
	f := newCohortPostgresFixture(t)
	year := currentSchoolYearStart(time.Now())
	grade := f.grade(t, fmt.Sprintf("%d-%d", year, year+1), year, 10)
	class := f.class(t, grade, "import-1")
	input := []StudentImportRow{importStudentRow(2, "00123", "合成学生", f.school, class.ID), importStudentRow(3, "00123", "合成学生", f.school, class.ID)}
	first, err := f.store.ImportStudentsAtomic(t.Context(), f.tenant, input)
	if err != nil || !first.Committed || first.Created != 1 || first.Skipped != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	again, err := f.store.ImportStudentsAtomic(t.Context(), f.tenant, input)
	if err != nil || !again.Committed || again.Created != 0 || again.Skipped != 2 {
		t.Fatalf("again=%+v err=%v", again, err)
	}
	conflict, err := f.store.ImportStudentsAtomic(t.Context(), f.tenant, []StudentImportRow{importStudentRow(2, "00124", "未写入学生", f.school, class.ID), importStudentRow(3, "00123", "冲突姓名", f.school, class.ID)})
	if err != nil || conflict.Committed || conflict.Created != 0 || len(conflict.Errors) != 1 {
		t.Fatalf("conflict=%+v err=%v", conflict, err)
	}
	var students, enrollments int
	if err = f.db.QueryRow(`SELECT count(*),(SELECT count(*) FROM student_enrollment WHERE tenant_id=$1) FROM student WHERE tenant_id=$1 AND student_no='00123'`, f.tenant).Scan(&students, &enrollments); err != nil {
		t.Fatal(err)
	}
	if students != 1 || enrollments != 1 {
		t.Fatalf("students=%d enrollments=%d", students, enrollments)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM student WHERE tenant_id=$1 AND student_no='00124'`, f.tenant).Scan(&students); err != nil || students != 0 {
		t.Fatalf("conflict batch wrote new student: count=%d err=%v", students, err)
	}
	wrongParent, err := f.store.ImportStudentsAtomic(t.Context(), f.tenant, []StudentImportRow{importStudentRow(2, "00125", "合成学生", "not-a-school-uuid", class.ID)})
	if err != nil || wrongParent.Committed || len(wrongParent.Errors) != 1 {
		t.Fatalf("invalid parent must be an actionable row error: result=%+v err=%v", wrongParent, err)
	}
}

func TestPostgresAtomicStudentImportRollsBackWhenEnrollmentWriteFails(t *testing.T) {
	f := newCohortPostgresFixture(t)
	year := currentSchoolYearStart(time.Now())
	class := f.class(t, f.grade(t, fmt.Sprintf("%d-%d", year, year+1), year, 10), "import-failure")
	// 在学生已经进入同一语句的写入结果后，让第二条初始学籍失败；主档和学籍必须一起撤回。
	_, err := f.db.Exec(`CREATE SEQUENCE import_enrollment_count;
CREATE FUNCTION reject_import_enrollment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF nextval('import_enrollment_count')=2 THEN RAISE EXCEPTION 'synthetic enrollment failure'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER reject_import_enrollment BEFORE INSERT ON student_enrollment FOR EACH ROW EXECUTE FUNCTION reject_import_enrollment()`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.ImportStudentsAtomic(t.Context(), f.tenant, []StudentImportRow{importStudentRow(2, "00001", "合成学生一", f.school, class.ID), importStudentRow(3, "00999", "合成学生二", f.school, class.ID)})
	if err == nil || result.Committed || result.Created != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var students, enrollments int
	if err = f.db.QueryRow(`SELECT (SELECT count(*) FROM student WHERE tenant_id=$1),(SELECT count(*) FROM student_enrollment WHERE tenant_id=$1)`, f.tenant).Scan(&students, &enrollments); err != nil {
		t.Fatal(err)
	}
	if students != 0 || enrollments != 0 {
		t.Fatalf("failed enrollment left students=%d enrollments=%d", students, enrollments)
	}
}

func TestPostgresAtomicStudentImportConcurrentRetriesCreateOneIdentity(t *testing.T) {
	f := newCohortPostgresFixture(t)
	year := currentSchoolYearStart(time.Now())
	class := f.class(t, f.grade(t, fmt.Sprintf("%d-%d", year, year+1), year, 10), "import-concurrent")
	input := []StudentImportRow{importStudentRow(2, "00123", "合成学生", f.school, class.ID)}
	start := make(chan struct{})
	results := make(chan CSVImportResult, 2)
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := f.store.ImportStudentsAtomic(context.Background(), f.tenant, input)
			results <- result
			failures <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	created, skipped := 0, 0
	for result := range results {
		if !result.Committed {
			t.Fatalf("repeated import failed: %+v", result)
		}
		created += result.Created
		skipped += result.Skipped
	}
	if created != 1 || skipped != 1 {
		t.Fatalf("created=%d skipped=%d", created, skipped)
	}
	var students, enrollments int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM student WHERE tenant_id=$1),(SELECT count(*) FROM student_enrollment WHERE tenant_id=$1)`, f.tenant).Scan(&students, &enrollments); err != nil {
		t.Fatal(err)
	}
	if students != 1 || enrollments != 1 {
		t.Fatalf("students=%d enrollments=%d", students, enrollments)
	}
}
