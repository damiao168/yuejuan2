package server

import (
	appealpkg "edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/evidence"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newE2EMemoryFixture(t *testing.T) *e2eMemoryFixture {
	t.Helper()
	authStore := e2eAuthStore(t)
	orgStore := org.NewMemoryStore()
	examStore := exam.NewMemoryStore()
	paperStore := paper.NewMemoryStore()
	fileStore := files.NewMemoryStore()
	objectStore := files.NewMemoryObjectStorage()
	submissionStore := submission.NewMemoryStore()
	ocrStore := ocrpkg.NewMemoryStore()
	segmentStore := segment.NewMemoryStore()
	gradingStore := grading.NewMemoryStore()
	subjectiveStore := subjective.NewMemoryStore()
	evidenceStore := evidence.NewMemoryStore()
	reviewStore := review.NewMemoryStore()
	scoreStore := score.NewMemoryStore()
	scoreReleaseStore := scorerelease.NewMemoryStore()
	appealStore := appealpkg.NewMemoryStore()
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "api-gateway-e2e-test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
		Files: config.FileConfig{
			Bucket:            "edugrade-e2e-synthetic",
			MaxUploadBytes:    2 * 1024 * 1024,
			AllowedExtensions: []string{".pdf", ".png", ".jpg", ".jpeg", ".csv", ".docx"},
		},
		Security: config.SecurityConfig{MaxRequestBodyBytes: 2 * 1024 * 1024},
		Observability: config.ObservabilityConfig{
			SlowRequestThreshold: time.Second,
		},
	}
	router := NewMemoryRouter(cfg, logger.New(io.Discard, "error"), nil, objectStore, func(stores *ApplicationStores) {
		stores.Identity = IdentityStores{Auth: authStore, Org: orgStore}
		stores.Exam.Exam = examStore
		stores.Exam.Paper = paperStore
		stores.Exam.Files = fileStore
		stores.Exam.Submissions = submissionStore
		stores.Exam.Segments = segmentStore
		stores.Capture.OCR = ocrStore
		stores.Capture.OCRQueue = ocrpkg.NewMemoryQueue()
		stores.Grading.Grading = gradingStore
		stores.Grading.Subjective = subjectiveStore
		stores.Grading.Evidence = evidenceStore
		stores.Grading.Review = reviewStore
		stores.Release.Score = scoreStore
		stores.Release.ScoreRelease = scoreReleaseStore
		stores.Release.Appeal = appealStore
	})
	return &e2eMemoryFixture{
		router:            router,
		authStore:         authStore,
		orgStore:          orgStore,
		examStore:         examStore,
		paperStore:        paperStore,
		submissionStore:   submissionStore,
		gradingStore:      gradingStore,
		subjectiveStore:   subjectiveStore,
		evidenceStore:     evidenceStore,
		reviewStore:       reviewStore,
		scoreStore:        scoreStore,
		scoreReleaseStore: scoreReleaseStore,
		appealStore:       appealStore,
	}
}

func (f *e2eMemoryFixture) assertUnfinishedReviewCannotPublish(t *testing.T, adminToken string) {
	t.Helper()
	created, err := f.examStore.CreateExam(t.Context(), auth.AccessScope{TenantID: e2eTenantID, TenantWide: true}, e2eAdminID,
		exam.CreateInput{SchoolID: "synthetic-school", Name: "Synthetic incomplete review exam", Subject: "physics", ExamType: "quiz", TotalScore: 1})
	if err != nil {
		t.Fatal(err)
	}
	incompleteExamID := created.ID
	f.scoreStore.AddSegment(score.SegmentSeed{ExamID: incompleteExamID, SubmissionID: "incomplete-submission", StudentID: e2eStudentID, AnswerSegmentID: "incomplete-segment", QuestionID: "incomplete-question", QuestionNo: "1", MaxScore: 1})
	f.scoreStore.AddHumanGrade(score.GradeSeed{AnswerSegmentID: "incomplete-segment", Score: 1, MaxScore: 1})
	if _, err := f.scoreStore.FinalizeExam(t.Context(), e2eTenantID, incompleteExamID, e2eAdminID); err != nil {
		t.Fatal(err)
	}
	f.scoreStore.AddReviewTask(score.TaskSeed{ExamID: incompleteExamID, Status: "assigned"})
	syncMemoryPublicationFacts(t, f.scoreStore, f.scoreReleaseStore, e2eTenantID, incompleteExamID)
	rec := e2eExpectStatus(t, f.router, http.MethodPost, "/api/v1/exams/"+incompleteExamID+"/publish", adminToken, `{"reason":"should fail"}`, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "unfinished_review_tasks") {
		t.Fatalf("publish with unfinished review should expose quality issue, got %s", rec.Body.String())
	}
}

func e2eAuthStore(t *testing.T) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword(e2ePassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	allPermissions := []string{
		"system:read", "tenant:manage", "org:manage", "student:import", "exam:manage", "file:manage",
		"submission:manage", "ocr:manage", "segment:manage", "orchestrator:manage", "grading:manage",
		"evidence:manage", "review:manage", "arbitration:manage", "score:manage", "student:grade:read",
		"appeal:create", "appeal:read", "appeal:manage", "audit:read", "audit:export", "report:read", "report:export",
	}
	store := auth.NewMemoryStore()
	store.AddUser(auth.UserWithPassword{User: auth.User{ID: "audit-worker", TenantID: e2eTenantID, TenantCode: e2eTenantCode, Username: auditFixtureWorkerUsername, Status: "active", Roles: []string{"page_processing_worker"}, Permissions: []string{"ocr:manage"}, DataScope: map[string]any{"scope": "service"}}, PasswordHash: hash})
	for _, user := range []auth.User{
		{
			ID:          e2eAdminID,
			TenantID:    e2eTenantID,
			TenantCode:  e2eTenantCode,
			Username:    "e2e_admin",
			DisplayName: "Synthetic E2E Admin",
			Status:      "active",
			Roles:       []string{"tenant_admin"},
			Permissions: allPermissions,
			DataScope:   map[string]any{"scope": "tenant", "synthetic": true},
		},
		{
			ID:          e2eTeacherID,
			TenantID:    e2eTenantID,
			TenantCode:  e2eTenantCode,
			Username:    "e2e_teacher",
			DisplayName: "Synthetic E2E Teacher",
			Status:      "active",
			Roles:       []string{"teacher", "grader"},
			Permissions: []string{"review:manage", "review:work", "appeal:read", "appeal:manage"},
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		{
			ID:          e2eStudentUserID,
			TenantID:    e2eTenantID,
			TenantCode:  e2eTenantCode,
			Username:    "e2e_student",
			DisplayName: "Synthetic E2E Student",
			Status:      "active",
			Roles:       []string{"student"},
			Permissions: []string{"student:grade:read", "appeal:create", "appeal:read"},
			DataScope:   map[string]any{"scope": "self", "student_id": e2eStudentID, "synthetic": true},
		},
		{
			ID:          e2eOtherUserID,
			TenantID:    e2eTenantID,
			TenantCode:  e2eTenantCode,
			Username:    "e2e_other_student",
			DisplayName: "Synthetic E2E Other Student",
			Status:      "active",
			Roles:       []string{"student"},
			Permissions: []string{"student:grade:read", "appeal:create", "appeal:read"},
			DataScope:   map[string]any{"scope": "self", "student_id": e2eOtherStudentID, "synthetic": true},
		},
		{
			ID:          e2eLimitedUserID,
			TenantID:    e2eTenantID,
			TenantCode:  e2eTenantCode,
			Username:    "e2e_limited",
			DisplayName: "Synthetic E2E Limited",
			Status:      "active",
			Roles:       []string{"observer"},
			Permissions: []string{"system:read"},
			DataScope:   map[string]any{"scope": "none", "synthetic": true},
		},
	} {
		store.AddUser(auth.UserWithPassword{User: user, PasswordHash: hash})
	}
	return store
}
