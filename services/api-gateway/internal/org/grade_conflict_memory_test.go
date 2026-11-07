package org

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// 存量错配和历史年度由夹具直接写入，不以公开新建接口绕过当前届别规则。
func legacyMemoryGrade(store *MemoryStore, tenant string, input Grade) Grade {
	input.ID = store.id("grade")
	input.TenantID = tenant
	input.EntryYear = legacyGradeEntryYear(input)
	input.GradeCohortID = fmt.Sprintf("cohort-%s-%s-%d", input.SchoolID, input.EducationStage, input.EntryYear)
	input.Status = "active"
	store.grades[input.ID] = input
	return input
}

func TestCurrentGradeCreationAndCorrectionConflictRules(t *testing.T) {
	year := currentSchoolYearStart(time.Now())
	makeInput := func(entry, level int) Grade {
		return Grade{SchoolID: "school", Name: standardGradeName(level), EducationStage: "senior", EntryYear: entry, LevelNo: level}
	}
	t.Run("duplicate levels and cohorts", func(t *testing.T) {
		s := NewMemoryStore()
		g, err := s.CreateGrade(t.Context(), "tenant", makeInput(year, 10))
		if err != nil {
			t.Fatal(err)
		}
		for _, level := range []int{10, 11} {
			if _, err = s.CreateGrade(t.Context(), "tenant", makeInput(year, level)); !errors.Is(err, ErrGradeCohortExists) {
				t.Fatalf("duplicate level=%d: %v", level, err)
			}
		}
		wrong := 11
		if _, err = s.UpdateGrade(t.Context(), "tenant", g.ID, GradePatch{LevelNo: &wrong}); !errors.Is(err, ErrGradeLevelYearMismatch) {
			t.Fatalf("patch bypassed year: %v", err)
		}
		if err = s.DeleteGrade(t.Context(), "tenant", g.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CreateGrade(t.Context(), "tenant", makeInput(year, 10)); err != nil {
			t.Fatalf("empty deleted cohort cannot be reused: %v", err)
		}
	})
	t.Run("invalid date and levels", func(t *testing.T) {
		for _, tc := range []struct {
			input Grade
			want  error
		}{{makeInput(year, 11), ErrGradeLevelYearMismatch}, {makeInput(year+1, 10), ErrGradeEntryYearFuture}, {makeInput(year-3, 12), ErrGradeCohortGraduated}, {Grade{SchoolID: "school", Name: "高一", EducationStage: "senior", EntryYear: year, LevelNo: 10, AcademicYear: fmt.Sprintf("%d-%d", year-1, year)}, ErrGradeAcademicYearMismatch}} {
			if _, err := NewMemoryStore().CreateGrade(t.Context(), "tenant", tc.input); !errors.Is(err, tc.want) {
				t.Fatalf("input=%+v err=%v want=%v", tc.input, err, tc.want)
			}
		}
	})
	t.Run("old swapped levels can be corrected without a cycle", func(t *testing.T) {
		s := NewMemoryStore()
		old := legacyMemoryGrade(s, "tenant", makeInput(year-1, 10))
		current := legacyMemoryGrade(s, "tenant", makeInput(year, 11))
		level := 11
		if _, err := s.UpdateGrade(t.Context(), "tenant", old.ID, GradePatch{LevelNo: &level}); err != nil {
			t.Fatal(err)
		}
		level = 10
		if _, err := s.UpdateGrade(t.Context(), "tenant", current.ID, GradePatch{LevelNo: &level}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("new creation waits for old invalid occupancy to be repaired", func(t *testing.T) {
		s := NewMemoryStore()
		old := legacyMemoryGrade(s, "tenant", makeInput(year-1, 10))
		if _, err := s.CreateGrade(t.Context(), "tenant", makeInput(year, 10)); !errors.Is(err, ErrGradeLevelConflict) {
			t.Fatal(err)
		}
		level := 11
		if _, err := s.UpdateGrade(t.Context(), "tenant", old.ID, GradePatch{LevelNo: &level}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateGrade(t.Context(), "tenant", makeInput(year, 10)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("parallel duplicate creation has one winner", func(t *testing.T) {
		s := NewMemoryStore()
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Go(func() { _, err := s.CreateGrade(context.Background(), "tenant", makeInput(year, 10)); results <- err })
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
	})
	t.Run("retained graduated and future cohorts do not occupy current levels", func(t *testing.T) {
		s := NewMemoryStore()
		legacyMemoryGrade(s, "tenant", makeInput(year-3, 12))
		legacyMemoryGrade(s, "tenant", makeInput(year+1, 10))
		for _, grade := range []Grade{makeInput(year-2, 12), makeInput(year, 10)} {
			if _, err := s.CreateGrade(t.Context(), "tenant", grade); err != nil {
				t.Fatalf("historical noncurrent occupancy: %v", err)
			}
		}
	})
}

func TestGradeConflictHTTPStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{{ErrGradeCohortExists, 409}, {ErrGradeLevelConflict, 409}, {ErrGradeHistoricalRecordImmutable, 409}, {ErrGradeLevelYearMismatch, 400}, {ErrGradeEntryYearFuture, 400}, {ErrGradeCohortGraduated, 400}, {ErrGradeAcademicYearMismatch, 400}} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/grades", nil)
		w := httptest.NewRecorder()
		writeStoreError(w, r, tc.err, "grade_create_failed", "failed")
		if w.Code != tc.want {
			t.Fatalf("%v got %d", tc.err, w.Code)
		}
	}
}
