package processing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// 班级身份的 SchoolIDs 只是组织导航信息；同校另一班的更新不能进入列表或 projected_at。
// 外校记录故意最新，防止先 LIMIT 再过滤造成空页或泄漏分页游标。
func TestExceptionCollectionScopesBeforePaginationAndProjectionTime(t *testing.T) {
	store := NewMemoryStore()
	identities := auth.NewMemoryStore()
	base := time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)
	for i, fixture := range []struct{ exam, school, class string }{
		{"exam-a1", "school-a", "class-a"}, {"exam-a2", "school-a", "class-b"}, {"exam-b", "school-b", "class-c"},
	} {
		store.PutState("tenant", PageState{ExamID: fixture.exam, PageID: fixture.exam + "-page", CurrentStage: StageQualityChecked, IssueCode: IssueLowImageQuality, UpdatedAt: base.Add(time.Duration(i) * time.Hour), SourceObservedAt: base.Add(time.Duration(i) * time.Hour)})
		identities.AddResourceBoundary("exam", fixture.exam, auth.ResourceBoundary{TenantID: "tenant", SchoolID: fixture.school, ExamID: fixture.exam, ClassIDs: []string{fixture.class}})
	}
	schoolUser := auth.User{TenantID: "tenant", ID: "school-admin", DataScope: map[string]any{"scope": "school", "school_id": "school-a"}}
	school, err := auth.ResolveDeclaredAccessScope(schoolUser)
	if err != nil {
		t.Fatal(err)
	}
	class := auth.AccessScope{TenantID: "tenant", ActorID: "teacher", SchoolIDs: []string{"school-a"}, ClassIDs: []string{"class-a"}}
	cases := []struct {
		name  string
		scope auth.AccessScope
		exam  string
		want  string
		more  bool
		at    time.Time
	}{
		{"school-without-exam-whitelist", school, "", "exam-a2", true, base.Add(time.Hour)},
		{"class-does-not-inherit-school-read", class, "", "exam-a1", false, base},
		{"explicit-filter-cannot-widen-school", school, "exam-b", "", false, time.Time{}},
		{"explicit-filter-cannot-widen-class", class, "exam-a2", "", false, time.Time{}},
		{"empty-scope-denies", auth.AccessScope{TenantID: "tenant", ActorID: "none"}, "", "", false, time.Time{}},
		{"explicit-exam-grant", auth.AccessScope{TenantID: "tenant", ActorID: "exam-owner", ExamIDs: []string{"exam-b"}}, "", "exam-b", false, base.Add(2 * time.Hour)},
		{"tenant-reader", auth.AccessScope{TenantID: "tenant", ActorID: "admin", TenantWide: true}, "", "exam-b", true, base.Add(2 * time.Hour)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filter := ExceptionFilter{ExamID: tc.exam, Limit: 1, Access: auth.ExamCollectionAccess{Scope: &tc.scope, Resolver: identities}}
			out, err := store.ListExceptions(t.Context(), "tenant", filter)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(out.Exceptions) != 0 || out.ProjectedAt != nil || out.NextCursor != "" || out.HasMore {
					t.Fatalf("denied scope leaked: %+v", out)
				}
				return
			}
			if len(out.Exceptions) != 1 || out.Exceptions[0].ExamID != tc.want || out.HasMore != tc.more || out.ProjectedAt == nil || !out.ProjectedAt.Equal(tc.at) {
				t.Fatalf("wrong scoped page: %+v", out)
			}
			if tc.more {
				filter.CursorAt, filter.CursorID = out.Exceptions[0].CreatedAt, out.Exceptions[0].ID
				next, err := store.ListExceptions(t.Context(), "tenant", filter)
				if err != nil || len(next.Exceptions) != 1 || next.Exceptions[0].ExamID == tc.want {
					t.Fatalf("next scoped page: %+v %v", next, err)
				}
			}
		})
	}
	bad := auth.AccessScope{TenantID: "foreign", ActorID: "actor", TenantWide: true}
	if _, err := store.ListExceptions(t.Context(), "tenant", ExceptionFilter{Access: auth.ExamCollectionAccess{Scope: &bad}}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("tenant mismatch: %v", err)
	}
}

func TestExceptionCollectionHandlerRequiresMatchingAuthenticatedScope(t *testing.T) {
	store := NewMemoryStore()
	identities := auth.NewMemoryStore()
	store.PutState("tenant", PageState{ExamID: "exam", PageID: "page", CurrentStage: StageQualityChecked, IssueCode: IssueLowImageQuality})
	identities.AddResourceBoundary("exam", "exam", auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school", ExamID: "exam"})
	user := auth.User{TenantID: "tenant", ID: "admin", DataScope: map[string]any{"scope": "school", "school_id": "school"}}
	scope, err := auth.ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(NewService(store, nil), identities)
	for _, tc := range []struct {
		name  string
		scope *auth.AccessScope
		want  int
	}{
		{"missing", nil, http.StatusForbidden}, {"school", &scope, http.StatusOK},
		{"wrong-actor", &auth.AccessScope{TenantID: "tenant", ActorID: "other", TenantWide: true}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/processing/exceptions", nil)
			ctx := auth.WithUser(context.Background(), user)
			if tc.scope != nil {
				ctx = auth.WithAccessScope(ctx, *tc.scope)
			}
			w := httptest.NewRecorder()
			h.ListExceptions(w, r.WithContext(ctx))
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.want == http.StatusOK {
				var out ListResult
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Exceptions) != 1 || out.ProjectedAt == nil {
					t.Fatalf("own school without exam whitelist: %s %v", w.Body.String(), err)
				}
			}
		})
	}
}
