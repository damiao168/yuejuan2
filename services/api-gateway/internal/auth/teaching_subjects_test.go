package auth

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestTeachingSubjectsNormalizeSharedExamAliasesAndRejectUnknown(t *testing.T) {
	got, err := NormalizeTeachingSubjects([]string{" 数学 ", "math", "POLITICS", "english"})
	if err != nil || !slices.Equal(got, []string{"english", "ethics_politics", "mathematics"}) {
		t.Fatalf("normalized subjects=%v error=%v", got, err)
	}
	if _, err := NormalizeTeachingSubjects([]string{"unknown"}); !errors.Is(err, ErrInvalidTeachingSubjects) {
		t.Fatalf("unknown subject error=%v", err)
	}
	for _, roles := range [][]string{{"school_admin", "teacher"}, {"tenant_admin", "grader"}, {"platform_admin", "arbitrator"}} {
		if IsReviewTeacherRoles(roles) || validateTeachingSubjects(roles, nil) != nil {
			t.Fatalf("administrator must retain management qualification without subject binding: %v", roles)
		}
	}
}

func TestManagedSubjectProfileComparesSnapshotAndRevokesStaleCredentials(t *testing.T) {
	store, actor, scope := managedAccessFixture(t)
	ctx, now := context.Background(), time.Now()
	created, err := store.CreateManagedUser(ctx, actor, scope, CreateManagedUserInput{
		Username: "subject-teacher", DisplayName: "Teacher", RoleCodes: []string{"teacher", "grader"}, SchoolID: "school-1", TeachingSubjects: []string{"math"},
	}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := store.FindUserByLogin(ctx, actor.TenantCode, created.Username)
	if _, err := store.CreateSession(ctx, CreateSessionInput{TenantID: actor.TenantID, UserID: created.ID, TokenHash: "subject-session", ExpiresAt: now.Add(time.Hour), SecurityEpoch: before.SecurityEpoch}); err != nil {
		t.Fatal(err)
	}
	input := UpdateManagedUserProfileInput{DisplayName: created.DisplayName, ExpectedDisplayName: created.DisplayName,
		TeachingSubjects: []string{"physics"}, ExpectedTeachingSubjects: []string{"math"}}
	updated, identityChanged, err := store.UpdateManagedUserProfile(ctx, actor, scope, created.ID, input)
	if err != nil || identityChanged || !slices.Equal(updated.TeachingSubjects, []string{"physics"}) {
		t.Fatalf("subject profile update=%+v identityChanged=%t error=%v", updated, identityChanged, err)
	}
	if _, err := store.FindUserBySession(ctx, "subject-session", now); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old subject credential remains valid: %v", err)
	}
	if _, _, err := store.UpdateManagedUserProfile(ctx, actor, scope, created.ID, input); !errors.Is(err, ErrManagedUserProfileConflict) {
		t.Fatalf("stale subject snapshot error=%v", err)
	}
	input.ExpectedTeachingSubjects = []string{"physics"}
	input.TeachingSubjects = []string{}
	if _, _, err := store.UpdateManagedUserProfile(ctx, actor, scope, created.ID, input); !errors.Is(err, ErrInvalidTeachingSubjects) {
		t.Fatalf("empty review teacher subjects error=%v", err)
	}
	input.TeachingSubjects, input.ExpectedTeachingSubjects = nil, nil
	input.DisplayName = "Updated teacher"
	updated, _, err = store.UpdateManagedUserProfile(ctx, actor, scope, created.ID, input)
	if err != nil || !slices.Equal(updated.TeachingSubjects, []string{"physics"}) {
		t.Fatalf("omitted subjects must retain existing qualification: %+v error=%v", updated, err)
	}
	input.ExpectedDisplayName = updated.DisplayName
	input.TeachingSubjects = []string{"physics"}
	if _, _, err := store.UpdateManagedUserProfile(ctx, actor, scope, created.ID, input); !errors.Is(err, errInvalidManagedProfile) {
		t.Fatalf("unpaired subject snapshot error=%v", err)
	}
}
