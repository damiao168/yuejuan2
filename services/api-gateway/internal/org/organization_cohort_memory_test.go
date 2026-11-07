package org

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestGradeLevelChangesKeepEntryCohortAndAnnualIdentity(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	year := currentSchoolYearStart(time.Now())
	for _, tc := range []struct {
		name               string
		entry, level, want int
	}{
		{"explicit fixed cohort", year, 10, year},
		{"legacy academic year derivation", 0, 11, year - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grade, err := store.CreateGrade(ctx, "tenant", Grade{SchoolID: "school", Name: "高二年级", LevelNo: tc.level, EntryYear: tc.entry, AcademicYear: fmt.Sprintf("%d-%d", year, year+1), EducationStage: "senior"})
			if err != nil || grade.EntryYear != tc.want {
				t.Fatalf("grade=%+v err=%v", grade, err)
			}
			level := tc.level
			updated, err := store.UpdateGrade(ctx, "tenant", grade.ID, GradePatch{LevelNo: &level})
			if err != nil || updated.EntryYear != tc.want || updated.GradeCohortID != grade.GradeCohortID || updated.AcademicYear != grade.AcademicYear {
				t.Fatalf("identity changed: %+v err=%v", updated, err)
			}
			level = 8
			if _, err = store.UpdateGrade(ctx, "tenant", grade.ID, GradePatch{LevelNo: &level}); !errors.Is(err, ErrInvalidGradeLevel) {
				t.Fatalf("cross-stage update: %v", err)
			}
		})
	}
}

func TestEmptyOrganizationDeletionChecksAllAnnualGradesAndInactiveStudents(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	old := legacyMemoryGrade(store, "tenant", Grade{SchoolID: "school", Name: "高一", LevelNo: 10, EntryYear: 2025, AcademicYear: "2025-2026", EducationStage: "senior"})
	current := legacyMemoryGrade(store, "tenant", Grade{SchoolID: "school", Name: "高二", LevelNo: 11, EntryYear: 2025, AcademicYear: "2026-2027", EducationStage: "senior"})
	cls, _ := store.CreateClass(ctx, "tenant", Class{SchoolID: "school", GradeID: old.ID, Name: "1班", Code: "c1"})
	student, _ := store.CreateStudent(ctx, "tenant", Student{SchoolID: "school", ClassID: cls.ID, StudentNo: "synthetic", Name: "Synthetic", Status: "inactive"})
	if err := store.DeleteClass(ctx, "tenant", cls.ID); !errors.Is(err, ErrClassHasStudents) {
		t.Fatalf("inactive student allowed deletion: %v", err)
	}
	if err := store.DeleteGrade(ctx, "tenant", current.ID); !errors.Is(err, ErrGradeHasClasses) {
		t.Fatalf("other annual grade class ignored: %v", err)
	}
	if err := store.DeleteStudent(ctx, "tenant", student.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteClass(ctx, "other-tenant", cls.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant deletion: %v", err)
	}
	if err := store.DeleteClass(ctx, "tenant", cls.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateStudent(ctx, "tenant", Student{ClassID: cls.ID}); !errors.Is(err, ErrInvalidParent) {
		t.Fatalf("deleted class accepted student: %v", err)
	}
	if err := store.DeleteGrade(ctx, "tenant", current.ID); err != nil {
		t.Fatal(err)
	}
	grades, _ := store.ListGrades(ctx, "tenant", "school")
	if len(grades) != 0 {
		t.Fatalf("old annual grade reappeared: %+v", grades)
	}
	if _, err := store.CreateClass(ctx, "tenant", Class{GradeID: old.ID}); !errors.Is(err, ErrInvalidParent) {
		t.Fatalf("deleted annual grade accepted class: %v", err)
	}
	cohorts, _ := store.ListGradeCohorts(ctx, "tenant", "school")
	if len(cohorts) != 1 || cohorts[0].EntryYear != 2025 {
		t.Fatalf("fixed cohort removed: %+v", cohorts)
	}
}
