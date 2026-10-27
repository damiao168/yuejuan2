package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
)

func TestCollectionScopeRemediationRoutes(t *testing.T) {
	for _, tenantWide := range []bool{false, true} {
		name := "school"
		if tenantWide {
			name = "tenant"
		}
		t.Run(name, func(t *testing.T) {
			stores := NewMemoryApplicationStores()
			identities := stores.Identity.Auth.(*auth.MemoryStore)
			issues := stores.AIFoundation.Disagreement.(*aidisagreement.MemoryStore)
			gold := stores.Grading.GoldPaper.(*goldpaper.MemoryStore)
			jobs := stores.Grading.Regrade.(*regrade.MemoryStore)
			issueService, regradeService := aidisagreement.NewService(issues), regrade.NewService(jobs)
			foreignIDs := map[string]string{}
			for _, suffix := range []string{"a", "b"} {
				examID, questionID, submissionID := "exam-"+suffix, "question-"+suffix, "submission-"+suffix
				boundary := auth.ResourceBoundary{TenantID: "tenant-1", SchoolID: "school-" + suffix, ExamID: examID, SubmissionID: submissionID}
				issues.AddActualComparison("tenant-1", aidisagreement.ActualComparison{ExamID: examID, QuestionID: questionID, SubmissionID: submissionID, AnswerSegmentID: "segment-" + suffix, AICandidateID: "ai-" + suffix, HumanGradeID: "human-" + suffix, AISuggestedScore: 5, HumanScore: 1, MaxScore: 5, AIConfidence: .9, RiskTier: "R3"})
				issue, _, err := issueService.Capture(t.Context(), "tenant-1", aidisagreement.CaptureInput{AICandidateID: "ai-" + suffix, HumanGradeID: "human-" + suffix})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = issueService.Classify(t.Context(), "tenant-1", issue.ID, "reviewer", aidisagreement.ClassifyInput{Taxonomy: aidisagreement.TaxonomyOCRError, ExpectedRevision: issue.Revision}); err != nil {
					t.Fatal(err)
				}
				identities.AddResourceBoundary("ai_human_disagreement", issue.ID, boundary)
				gold.SetSource("tenant-1", goldpaper.Source{ExamID: examID, QuestionID: questionID, SubmissionID: submissionID, SnapshotID: "snapshot-" + suffix, MaxScore: 5, RubricSnapshot: map[string]any{"source": suffix}, AvailableGradeIDs: []string{"grade-" + suffix}, ImageBinding: goldpaper.ImageBinding{SegmentID: "segment-" + suffix, AssetID: "asset-" + suffix, SHA256: strings.Repeat("a", 64), OwnerType: "answer_segment_crop", OwnerID: "run-" + suffix}})
				goldItem, err := gold.Nominate(t.Context(), "tenant-1", examID, questionID, "nominator", goldpaper.NominateInput{SubmissionID: submissionID, ReferenceScore: 3, Explanation: "synthetic", SourceGradeIDs: []string{"grade-" + suffix}})
				if err != nil {
					t.Fatal(err)
				}
				identities.AddResourceBoundary("gold_paper", goldItem.ID, boundary)
				jobs.SeedPublishedRelease("tenant-1", examID, "release-"+suffix, 1, true, map[string][]regrade.SourceItem{questionID: {{SubmissionID: submissionID, OldFinalGradeID: "final-" + suffix, OldScore: 2, MaxScore: 5}}})
				job, err := regradeService.Create(t.Context(), "tenant-1", examID, questionID, "manager", regrade.CreateInput{SourceReleaseID: "release-" + suffix, ReasonCode: regrade.ReasonAnswerKeyError, ReasonText: "synthetic correction", Strategy: regrade.StrategyHumanRecheck, IdempotencyKey: "collection-scope-" + suffix, AssigneeID: "grader"})
				if err != nil {
					t.Fatal(err)
				}
				identities.AddResourceBoundary("regrade_job", job.Job.ID, boundary)
				if suffix == "b" {
					foreignIDs = map[string]string{"/api/v1/ai-human-disagreements/": issue.ID, "/api/v1/gold-papers/": goldItem.ID, "/api/v1/regrade-jobs/": job.Job.ID}
				}
			}
			user := auth.User{ID: "scope-manager", TenantID: "tenant-1", Username: "scope-manager", Status: "active", Roles: []string{"school_admin"}, Permissions: []string{"review:manage"}, DataScope: map[string]any{"scope": "school", "school_id": "school-a"}}
			if tenantWide {
				user.Roles = []string{"tenant_admin"}
				user.DataScope = map[string]any{"scope": "tenant"}
			}
			router := reviewBoundaryRouter(t, stores, user)
			for prefix, id := range foreignIDs {
				response := reviewBoundaryRequest(router, http.MethodGet, prefix+id, "")
				want := http.StatusForbidden
				if tenantWide {
					want = http.StatusOK
				}
				if response.Code != want {
					t.Fatalf("detail %s: %d %s", prefix, response.Code, response.Body.String())
				}
			}
			for _, endpoint := range []struct{ path, key string }{{"/api/v1/ai-human-disagreements", "disagreements"}, {"/api/v1/ai-human-disagreements/dataset", "dataset"}, {"/api/v1/gold-papers", "gold_papers"}, {"/api/v1/regrade-jobs", "regrade_jobs"}} {
				for _, query := range []string{"", "?exam_id=exam-b"} {
					response := reviewBoundaryRequest(router, http.MethodGet, endpoint.path+query, "")
					var payload map[string]json.RawMessage
					if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != http.StatusOK {
						t.Fatalf("%s status=%d body=%s err=%v", endpoint.path, response.Code, response.Body.String(), err)
					}
					var items []json.RawMessage
					if err := json.Unmarshal(payload[endpoint.key], &items); err != nil {
						t.Fatal(err)
					}
					want := 1
					if tenantWide && query == "" {
						want = 2
					} else if !tenantWide && query != "" {
						want = 0
					}
					if len(items) != want {
						t.Fatalf("%s%s count=%d want=%d body=%s", endpoint.path, query, len(items), want, response.Body.String())
					}
					if !tenantWide && (strings.Contains(response.Body.String(), `"exam_id":"exam-b"`) || strings.Contains(response.Body.String(), `"submission-b"`)) {
						t.Fatalf("foreign identifiers leaked: %s", response.Body.String())
					}
				}
			}
		})
	}
}

func TestCollectionHandlersRequireAuthenticatedScope(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"disagreements": aidisagreement.NewHandler(aidisagreement.NewService(aidisagreement.NewMemoryStore())).List,
		"dataset":       aidisagreement.NewHandler(aidisagreement.NewService(aidisagreement.NewMemoryStore())).Dataset,
		"gold":          goldpaper.NewHandler(goldpaper.NewMemoryStore(), nil).List,
		"regrade":       regrade.NewHandler(regrade.NewService(regrade.NewMemoryStore()), nil).List,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request = request.WithContext(auth.WithUser(context.Background(), auth.User{ID: "manager", TenantID: "tenant"}))
			response := httptest.NewRecorder()
			handler(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("missing scope accepted: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
