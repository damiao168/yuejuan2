package org

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type cohortPostgresFixture struct {
	db             *sql.DB
	store          *PostgresStore
	tenant, school string
}

func newCohortPostgresFixture(t *testing.T) cohortPostgresFixture {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("YUEJUAN_ORG_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("YUEJUAN_ORG_TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimPrefix(parsed.Path, "/"), "_test") {
		t.Fatal("organization tests require an explicitly named *_test database")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	schema := "org_cohort_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	// 复制正式迁移后的字段、默认值和索引，在随机 schema 中执行真实 SQL；不改 public 的种子或业务数据。
	for _, table := range []string{"tenant", "school", "academic_year", "grade_cohort", "grade", "school_class", "student", "student_enrollment", "app_user", "role", "user_role"} {
		if _, err = admin.Exec(`CREATE TABLE ` + schema + `.` + table + ` (LIKE public.` + table + ` INCLUDING ALL)`); err != nil {
			t.Fatal(err)
		}
	}
	for _, relation := range []struct{ table, column, parent string }{
		{"school", "tenant_id", "tenant"}, {"academic_year", "school_id", "school"}, {"grade_cohort", "school_id", "school"},
		{"grade", "school_id", "school"}, {"grade", "academic_year_id", "academic_year"}, {"grade", "grade_cohort_id", "grade_cohort"},
		{"school_class", "grade_id", "grade"}, {"school_class", "academic_year_id", "academic_year"}, {"school_class", "grade_cohort_id", "grade_cohort"},
		{"student", "class_id", "school_class"}, {"student_enrollment", "student_id", "student"}, {"student_enrollment", "class_id", "school_class"},
	} {
		if _, err = admin.Exec(`ALTER TABLE ` + schema + `.` + relation.table + ` ADD FOREIGN KEY (` + relation.column + `) REFERENCES ` + schema + `.` + relation.parent + `(id)`); err != nil {
			t.Fatal(err)
		}
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(6)
	t.Cleanup(func() {
		db.Close()
		clean := stdlib.OpenDB(*config)
		defer clean.Close()
		if _, err := clean.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	})
	f := cohortPostgresFixture{db: db, store: NewPostgresStore(db), tenant: uuid.NewString()}
	if _, err = db.Exec(`INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic org','synthetic','active')`, f.tenant); err != nil {
		t.Fatal(err)
	}
	school, err := f.store.CreateSchool(t.Context(), f.tenant, School{Name: "Synthetic school", Code: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	f.school = school.ID
	return f
}

func (f cohortPostgresFixture) grade(t *testing.T, year string, entry, level int) Grade {
	t.Helper()
	// 历史兼容夹具直接构造存量年度记录；公开CreateGrade现在只能新建不存在的当前届别。
	start := currentSchoolYearStart(time.Now())
	if year != "" {
		if _, err := fmt.Sscanf(year, "%d-", &start); err != nil {
			t.Fatal(err)
		}
	} else {
		year = fmt.Sprintf("%d-%d", start, start+1)
	}
	var yearID, cohortID string
	err := f.db.QueryRow(`INSERT INTO academic_year(tenant_id,school_id,name,start_year,end_year,starts_at,ends_at,is_current) VALUES($1,$2,$3,$4,$4+1,make_date($4,9,1),make_date($4+1,8,31),false) ON CONFLICT(tenant_id,school_id,start_year) DO UPDATE SET updated_at=now() RETURNING id::text`, f.tenant, f.school, year+"学年", start).Scan(&yearID)
	if err != nil {
		t.Fatal(err)
	}
	err = f.db.QueryRow(`INSERT INTO grade_cohort(tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name) VALUES($1,$2,'senior',$3,$3+3,$4) ON CONFLICT(tenant_id,school_id,education_stage,entry_year) DO UPDATE SET updated_at=now() RETURNING id::text`, f.tenant, f.school, entry, fmt.Sprintf("%d级", entry)).Scan(&cohortID)
	if err != nil {
		t.Fatal(err)
	}
	g := Grade{TenantID: f.tenant, SchoolID: f.school, Name: standardGradeName(level), LevelNo: level, EntryYear: entry, AcademicYear: year, EducationStage: "senior", AcademicYearID: yearID, GradeCohortID: cohortID, Status: "active"}
	err = f.db.QueryRow(`INSERT INTO grade(tenant_id,school_id,name,level_no,academic_year,education_stage,academic_year_id,grade_cohort_id,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text`, g.TenantID, g.SchoolID, g.Name, g.LevelNo, g.AcademicYear, g.EducationStage, g.AcademicYearID, g.GradeCohortID, g.Status).Scan(&g.ID)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func (f cohortPostgresFixture) class(t *testing.T, grade Grade, code string) Class {
	t.Helper()
	c, err := f.store.CreateClass(t.Context(), f.tenant, Class{SchoolID: f.school, GradeID: grade.ID, Name: code, Code: code})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCohortPostgresLifecycleKeepsHistoryAndRejectsNonEmptyParents(t *testing.T) {
	f := newCohortPostgresFixture(t)
	old := f.grade(t, "2025-2026", 2025, 10)
	current := f.grade(t, "2026-2027", 2025, 11)
	if old.GradeCohortID != current.GradeCohortID || old.EntryYear != 2025 || current.EntryYear != 2025 {
		t.Fatalf("cohort mismatch: %+v %+v", old, current)
	}
	level := 12
	if _, err := f.db.Exec(`INSERT INTO academic_year(tenant_id,school_id,name,start_year,end_year,starts_at,ends_at,is_current) VALUES($1,$2,'2027-2028学年',2027,2028,'2027-09-01','2028-08-31',true)`, f.tenant, f.school); err != nil {
		t.Fatal(err)
	}
	updated, err := f.store.UpdateGrade(t.Context(), f.tenant, current.ID, GradePatch{LevelNo: &level})
	if err != nil || updated.EntryYear != 2025 || updated.GradeCohortID != current.GradeCohortID || updated.AcademicYearID != current.AcademicYearID {
		t.Fatalf("patch changed identity: %+v %v", updated, err)
	}
	level = 8
	if _, err = f.store.UpdateGrade(t.Context(), f.tenant, current.ID, GradePatch{LevelNo: &level}); !errors.Is(err, ErrInvalidGradeLevel) {
		t.Fatalf("cross-stage patch=%v", err)
	}
	oldClass := f.class(t, old, "old-1")
	newClass := f.class(t, current, "current-1")
	student, err := f.store.CreateStudent(t.Context(), f.tenant, Student{SchoolID: f.school, ClassID: oldClass.ID, StudentNo: "S001", Name: "Synthetic", Status: "inactive"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.DeleteClass(t.Context(), f.tenant, oldClass.ID); !errors.Is(err, ErrClassHasStudents) {
		t.Fatalf("inactive student class deletion=%v", err)
	}
	if err = f.store.DeleteGrade(t.Context(), f.tenant, current.ID); !errors.Is(err, ErrGradeHasClasses) {
		t.Fatalf("other annual grade class ignored=%v", err)
	}
	if _, err = f.db.Exec(`CREATE TABLE historical_snapshot (id UUID PRIMARY KEY, class_id UUID NOT NULL REFERENCES school_class(id), grade_id UUID NOT NULL REFERENCES grade(id), note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO historical_snapshot VALUES($1,$2,$3,'frozen')`, uuid.NewString(), oldClass.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	// 同年目标班用于保留旧学籍并检验转出后空班可删除；历史快照中的名称和引用不重写。
	otherOldClass := f.class(t, old, "old-2")
	if _, err = f.store.TransferStudent(t.Context(), f.tenant, student.ID, otherOldClass.ID, "2026-01-10"); err != nil {
		t.Fatal(err)
	}
	if err = f.store.DeleteClass(t.Context(), f.tenant, oldClass.ID); err != nil {
		t.Fatalf("transferred historical enrollment blocked empty class=%v", err)
	}
	var historyRows int
	if err = f.db.QueryRow(`SELECT count(*) FROM student_enrollment WHERE student_id=$1 AND class_id=$2 AND status='transferred'`, student.ID, oldClass.ID).Scan(&historyRows); err != nil || historyRows != 1 {
		t.Fatalf("history=%d %v", historyRows, err)
	}
	if _, err = f.store.CreateStudent(t.Context(), f.tenant, Student{SchoolID: f.school, ClassID: oldClass.ID, StudentNo: "after-delete", Name: "Rejected"}); !errors.Is(err, ErrInvalidParent) {
		t.Fatalf("deleted class accepted student=%v", err)
	}
	if err = f.store.DeleteClass(t.Context(), uuid.NewString(), newClass.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant delete=%v", err)
	}
	if err = f.store.DeleteClass(t.Context(), f.tenant, newClass.ID); err != nil {
		t.Fatal(err)
	}
	// 仅在隔离夹具移除该生，让整届空年级删除保留旧记录与外键引用；生产永久删除另有业务检查。
	if _, err = f.db.Exec(`UPDATE student SET deleted_at=now() WHERE id=$1`, student.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.DeleteClass(t.Context(), f.tenant, otherOldClass.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.DeleteGrade(t.Context(), f.tenant, current.ID); err != nil {
		t.Fatal(err)
	}
	grades, err := f.store.ListGrades(t.Context(), f.tenant, f.school)
	if err != nil || len(grades) != 0 {
		t.Fatalf("deleted annual record returned=%+v %v", grades, err)
	}
	if _, err = f.store.CreateClass(t.Context(), f.tenant, Class{SchoolID: f.school, GradeID: old.ID, Name: "Rejected", Code: "after-delete"}); !errors.Is(err, ErrInvalidParent) {
		t.Fatalf("deleted grade accepted class=%v", err)
	}
	var kept int
	if err = f.db.QueryRow(`SELECT count(*) FROM historical_snapshot snap JOIN school_class cls ON cls.id=snap.class_id JOIN grade g ON g.id=snap.grade_id JOIN grade_cohort cohort ON cohort.id=g.grade_cohort_id WHERE snap.note='frozen' AND cohort.entry_year=2025`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("history no longer joins=%d %v", kept, err)
	}
}

func TestCohortPostgresDefaultYearUsesConfiguredSchoolYear(t *testing.T) {
	f := newCohortPostgresFixture(t)
	_, err := f.db.Exec(`INSERT INTO academic_year(tenant_id,school_id,name,start_year,end_year,starts_at,ends_at,is_current) VALUES($1,$2,'2024-2025学年',2024,2025,'2024-09-01','2025-08-31',true)`, f.tenant, f.school)
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.store.CreateGrade(t.Context(), f.tenant, Grade{SchoolID: f.school, Name: "高一", EntryYear: 2024, LevelNo: 10, EducationStage: "senior"})
	if err != nil {
		t.Fatal(err)
	}
	if g.AcademicYear != "2024-2025" || g.EntryYear != 2024 {
		t.Fatalf("configured year not used=%+v", g)
	}
}

func TestCohortPostgresCrossYearTransferKeepsOneCurrentClassAndHistory(t *testing.T) {
	f := newCohortPostgresFixture(t)
	old := f.grade(t, "2025-2026", 2025, 10)
	current := f.grade(t, "2026-2027", 2025, 11)
	oldClass, newClass := f.class(t, old, "old"), f.class(t, current, "current")
	student, err := f.store.CreateStudent(t.Context(), f.tenant, Student{SchoolID: f.school, ClassID: oldClass.ID, StudentNo: "cross-year", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.TransferStudent(t.Context(), f.tenant, student.ID, newClass.ID, "2025-08-01"); !errors.Is(err, ErrInvalidEnrollmentDate) {
		t.Fatalf("backward date accepted=%v", err)
	}
	var active int
	if err = f.db.QueryRow(`SELECT count(*) FROM student_enrollment WHERE student_id=$1 AND status='enrolled' AND end_date IS NULL`, student.ID).Scan(&active); err != nil || active != 1 {
		t.Fatalf("failed transfer changed active history=%d %v", active, err)
	}
	if _, err = f.store.TransferStudent(t.Context(), f.tenant, student.ID, newClass.ID, "2026-09-01"); err != nil {
		t.Fatal(err)
	}
	if err = f.store.DeleteClass(t.Context(), f.tenant, oldClass.ID); err != nil {
		t.Fatalf("old class is empty after cross-year transfer=%v", err)
	}
	if err = f.store.DeleteClass(t.Context(), f.tenant, newClass.ID); !errors.Is(err, ErrClassHasStudents) {
		t.Fatalf("new class ignored student=%v", err)
	}
	entries, err := f.store.ListStudentEnrollments(t.Context(), f.tenant, student.ID)
	if err != nil || len(entries) != 2 {
		t.Fatalf("history=%+v %v", entries, err)
	}
	for _, entry := range entries {
		if entry.ClassID == oldClass.ID && (entry.Status != "transferred" || entry.EndDate != "2026-09-01") {
			t.Fatalf("old history not closed=%+v", entry)
		}
	}
	students, err := f.store.ListStudents(t.Context(), f.tenant, StudentListFilter{})
	if err != nil || len(students) != 1 || students[0].ClassID != newClass.ID {
		t.Fatalf("current class=%+v %v", students, err)
	}
	if _, err = f.store.TransferStudent(t.Context(), f.tenant, student.ID, newClass.ID, "2026-08-31"); !errors.Is(err, ErrInvalidEnrollmentDate) {
		t.Fatalf("later active enrollment overwritten=%v", err)
	}
	// 管理员可能为留级或资料更正调回另一存档学年的班级；current 标记不能覆盖真实的新在读关系。
	returnClass := f.class(t, old, "return-to-old-year")
	if _, err = f.store.TransferStudent(t.Context(), f.tenant, student.ID, returnClass.ID, "2026-10-01"); err != nil {
		t.Fatal(err)
	}
	students, err = f.store.ListStudents(t.Context(), f.tenant, StudentListFilter{})
	if err != nil || len(students) != 1 || students[0].ClassID != returnClass.ID {
		t.Fatalf("historical year selected over current enrollment=%+v %v", students, err)
	}
	name := "Synthetic corrected"
	updated, err := f.store.UpdateStudent(t.Context(), f.tenant, student.ID, StudentPatch{Name: &name})
	if err != nil || updated.ClassID != returnClass.ID {
		t.Fatalf("update response reverted to old class=%+v %v", updated, err)
	}
}

func TestCohortPostgresParentLocksSerializeDeleteAndChildCreation(t *testing.T) {
	f := newCohortPostgresFixture(t)
	g := f.grade(t, "2026-2027", 2026, 10)
	c := f.class(t, g, "locked")
	t.Run("student cannot enter class after deletion", func(t *testing.T) {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`SELECT id FROM school_class WHERE id=$1 FOR UPDATE`, c.ID); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := f.store.CreateStudent(t.Context(), f.tenant, Student{SchoolID: f.school, ClassID: c.ID, StudentNo: "race", Name: "Rejected"})
			result <- err
		}()
		assertCohortOperationWaits(t, result)
		if _, err = tx.Exec(`UPDATE school_class SET deleted_at=now() WHERE id=$1`, c.ID); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = <-result; !errors.Is(err, ErrInvalidParent) {
			t.Fatalf("create after delete=%v", err)
		}
	})
	t.Run("class cannot enter annual grade after cohort deletion", func(t *testing.T) {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`SELECT id FROM grade_cohort WHERE id=$1 FOR UPDATE`, g.GradeCohortID); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := f.store.CreateClass(t.Context(), f.tenant, Class{SchoolID: f.school, GradeID: g.ID, Name: "Rejected", Code: "race"})
			result <- err
		}()
		assertCohortOperationWaits(t, result)
		if _, err = tx.Exec(`UPDATE grade SET deleted_at=now() WHERE grade_cohort_id=$1`, g.GradeCohortID); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = <-result; !errors.Is(err, ErrInvalidParent) {
			t.Fatalf("create after grade delete=%v", err)
		}
	})
}

func TestCohortPostgresDeletionSeesCommittedChildrenAndTransferRejectsDeletedTarget(t *testing.T) {
	f := newCohortPostgresFixture(t)
	g := f.grade(t, "2026-2027", 2026, 10)
	source, target := f.class(t, g, "source"), f.class(t, g, "target")
	student, err := f.store.CreateStudent(t.Context(), f.tenant, Student{SchoolID: f.school, ClassID: source.ID, StudentNo: "source-student", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("transfer must recheck deleted target after waiting", func(t *testing.T) {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`SELECT id FROM school_class WHERE id=$1 FOR UPDATE`, target.ID); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := f.store.TransferStudent(t.Context(), f.tenant, student.ID, target.ID, "2026-10-01")
			result <- err
		}()
		assertCohortOperationWaits(t, result)
		if _, err = tx.Exec(`UPDATE school_class SET deleted_at=now() WHERE id=$1`, target.ID); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = <-result; !errors.Is(err, ErrInvalidParent) {
			t.Fatalf("transfer after target deletion=%v", err)
		}
		var current string
		if err = f.db.QueryRow(`SELECT class_id::text FROM student WHERE id=$1`, student.ID).Scan(&current); err != nil || current != source.ID {
			t.Fatalf("failed transfer changed current class=%s %v", current, err)
		}
	})
	t.Run("delete must see a child committed while waiting for class lock", func(t *testing.T) {
		c := f.class(t, g, "inserting")
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`SELECT id FROM school_class WHERE id=$1 FOR SHARE`, c.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO student(tenant_id,school_id,class_id,student_no,name,status) VALUES($1,$2,$3,'concurrent-student','Synthetic','inactive')`, f.tenant, f.school, c.ID); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- f.store.DeleteClass(t.Context(), f.tenant, c.ID) }()
		assertCohortOperationWaits(t, result)
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = <-result; !errors.Is(err, ErrClassHasStudents) {
			t.Fatalf("deletion missed committed child=%v", err)
		}
	})
	t.Run("cohort deletion must see a newly committed annual class", func(t *testing.T) {
		empty := f.grade(t, "2026-2027", 2024, 12)
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`SELECT id FROM grade_cohort WHERE id=$1 FOR SHARE`, empty.GradeCohortID); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO school_class(tenant_id,school_id,grade_id,academic_year_id,grade_cohort_id,name,code,status) VALUES($1,$2,$3,$4,$5,'Synthetic','concurrent-class','active')`, f.tenant, f.school, empty.ID, empty.AcademicYearID, empty.GradeCohortID); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- f.store.DeleteGrade(t.Context(), f.tenant, empty.ID) }()
		assertCohortOperationWaits(t, result)
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = <-result; !errors.Is(err, ErrGradeHasClasses) {
			t.Fatalf("cohort deletion missed committed class=%v", err)
		}
	})
}

func assertCohortOperationWaits(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("operation ignored parent lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSchoolYearStartUsesSeptemberAndShanghaiTime(t *testing.T) {
	for _, tc := range []struct {
		date string
		want int
	}{{"2027-01-01T00:00:00Z", 2026}, {"2027-08-31T15:59:59Z", 2026}, {"2027-08-31T16:00:00Z", 2027}} {
		now, err := time.Parse(time.RFC3339, tc.date)
		if err != nil {
			t.Fatal(err)
		}
		if got := currentSchoolYearStart(now); got != tc.want {
			t.Fatal(fmt.Sprintf("%s school year=%d want=%d", tc.date, got, tc.want))
		}
	}
}
