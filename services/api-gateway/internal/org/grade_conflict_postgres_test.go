package org

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func configureConflictSchoolYear(t *testing.T, f cohortPostgresFixture, year int) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE academic_year SET is_current=false WHERE school_id=$1`, f.school); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO academic_year(tenant_id,school_id,name,start_year,end_year,starts_at,ends_at,is_current) VALUES($1,$2,($3::int)::text||'-'||($3::int+1)::text,$3::int,$3::int+1,make_date($3::int,9,1),make_date($3::int+1,8,31),true) ON CONFLICT(tenant_id,school_id,start_year) DO UPDATE SET is_current=true`, f.tenant, f.school, year); err != nil {
		t.Fatal(err)
	}
}

func TestCohortConflictPostgresCreationBoundariesAndRepair(t *testing.T) {
	f := newCohortPostgresFixture(t)
	configureConflictSchoolYear(t, f, 2026)
	input := func(entry, level int) Grade {
		return Grade{SchoolID: f.school, Name: standardGradeName(level), EducationStage: "senior", EntryYear: entry, LevelNo: level}
	}
	for _, tc := range []struct {
		grade Grade
		want  error
	}{{input(2026, 11), ErrGradeLevelYearMismatch}, {input(2027, 10), ErrGradeEntryYearFuture}, {input(2023, 12), ErrGradeCohortGraduated}, {Grade{SchoolID: f.school, Name: "高一", LevelNo: 10, EntryYear: 2026, EducationStage: "senior", AcademicYear: "2025-2026"}, ErrGradeAcademicYearMismatch}} {
		if _, err := f.store.CreateGrade(t.Context(), f.tenant, tc.grade); !errors.Is(err, tc.want) {
			t.Fatalf("input=%+v err=%v want=%v", tc.grade, err, tc.want)
		}
	}
	bad := f.grade(t, "2026-2027", 2025, 10)
	f.grade(t, "2023-2024", 2023, 12)
	f.grade(t, "2027-2028", 2027, 10)
	if _, err := f.store.CreateGrade(t.Context(), f.tenant, input(2026, 10)); !errors.Is(err, ErrGradeLevelConflict) {
		t.Fatalf("old invalid occupancy not blocked: %v", err)
	}
	level := 11
	if _, err := f.store.UpdateGrade(t.Context(), f.tenant, bad.ID, GradePatch{LevelNo: &level}); err != nil {
		t.Fatal(err)
	}
	created, err := f.store.CreateGrade(t.Context(), f.tenant, input(2026, 10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateGrade(t.Context(), f.tenant, input(2024, 12)); err != nil {
		t.Fatalf("graduated historical level blocked current cohort=%v", err)
	}
	for _, level := range []int{10, 11} {
		if _, err := f.store.CreateGrade(t.Context(), f.tenant, input(2026, level)); !errors.Is(err, ErrGradeCohortExists) {
			t.Fatalf("same cohort duplicate accepted level=%d err=%v", level, err)
		}
	}
	wrong := 11
	if _, err := f.store.UpdateGrade(t.Context(), f.tenant, created.ID, GradePatch{LevelNo: &wrong}); !errors.Is(err, ErrGradeLevelYearMismatch) {
		t.Fatalf("patch bypass=%v", err)
	}
	// 同年入学的初中与高中属于不同学段，不能互相挡住。
	junior := input(2026, 7)
	junior.EducationStage = "junior"
	junior.Name = "初一"
	if _, err := f.store.CreateGrade(t.Context(), f.tenant, junior); err != nil {
		t.Fatalf("cross-stage conflict=%v", err)
	}
	if err := f.store.DeleteGrade(t.Context(), f.tenant, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateGrade(t.Context(), f.tenant, input(2026, 10)); err != nil {
		t.Fatalf("soft-deleted empty cohort cannot reuse=%v", err)
	}
}

func TestCohortConflictSchoolSerializationDoesNotBlockChildForeignKeys(t *testing.T) {
	f := newCohortPostgresFixture(t)
	configureConflictSchoolYear(t, f, 2026)
	g := f.grade(t, "2026-2027", 2026, 10)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT id FROM grade_cohort WHERE id=$1 FOR SHARE`, g.GradeCohortID); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- f.store.DeleteGrade(t.Context(), f.tenant, g.ID) }()
	assertCohortOperationWaits(t, deleted)
	// 删除已经锁住学校并等待届别；子班级仍须取得学校外键KEY SHARE，不能相互等待。
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err = tx.ExecContext(ctx, `INSERT INTO school_class(tenant_id,school_id,grade_id,academic_year_id,grade_cohort_id,name,code,status) VALUES($1,$2,$3,$4,$5,'Synthetic','school-key-share','active')`, f.tenant, f.school, g.ID, g.AcademicYearID, g.GradeCohortID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-deleted; !errors.Is(err, ErrGradeHasClasses) {
		t.Fatalf("committed class lost to delete=%v", err)
	}
}

func TestCohortConflictPostgresConcurrentCreationHasOneWinner(t *testing.T) {
	f := newCohortPostgresFixture(t)
	configureConflictSchoolYear(t, f, 2026)
	input := Grade{SchoolID: f.school, Name: "高一", EntryYear: 2026, LevelNo: 10, EducationStage: "senior"}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() { _, err := f.store.CreateGrade(t.Context(), f.tenant, input); results <- err })
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrGradeCohortExists) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM grade WHERE deleted_at IS NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("persisted count=%d err=%v", count, err)
	}
}

func TestCohortConflictPostgresHistoricalRepresentativeAndSwappedRepair(t *testing.T) {
	f := newCohortPostgresFixture(t)
	configureConflictSchoolYear(t, f, 2026)
	historical := f.grade(t, "2025-2026", 2025, 10)
	current := f.grade(t, "2026-2027", 2025, 11)
	input := Grade{SchoolID: f.school, Name: "高一", EntryYear: 2026, LevelNo: 10, EducationStage: "senior"}
	created, err := f.store.CreateGrade(t.Context(), f.tenant, input)
	if err != nil {
		t.Fatalf("historical old level blocked current=%v", err)
	}
	level := 11
	if _, err := f.store.UpdateGrade(t.Context(), f.tenant, historical.ID, GradePatch{LevelNo: &level}); !errors.Is(err, ErrGradeHistoricalRecordImmutable) {
		t.Fatalf("historical patch=%v", err)
	}
	// 制造旧系统的交换错配；各自校正不能被对方的错误占位互相锁死。
	if _, err := f.db.Exec(`UPDATE grade SET level_no=CASE WHEN id=$1 THEN 10 ELSE 11 END WHERE id IN ($1,$2)`, current.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateGrade(t.Context(), f.tenant, current.ID, GradePatch{LevelNo: &level}); err != nil {
		t.Fatal(err)
	}
	level = 10
	if _, err := f.store.UpdateGrade(t.Context(), f.tenant, created.ID, GradePatch{LevelNo: &level}); err != nil {
		t.Fatal(err)
	}
}
