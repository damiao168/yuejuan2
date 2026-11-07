package imagequality

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/submission"
)

type auditNormalizedRunStore struct {
	Store
	run Run
}

func (s auditNormalizedRunStore) GetRun(context.Context, string, string) (Run, error) {
	return s.run, nil
}

type auditNormalizedFileStore struct {
	files.Store
	asset files.FileAsset
}

func (s auditNormalizedFileStore) Get(context.Context, string, string) (files.FileAsset, error) {
	return s.asset, nil
}

type auditNormalizedSubmissionStore struct {
	submission.Store
	item submission.Submission
}

func (s auditNormalizedSubmissionStore) Get(context.Context, string, string) (submission.Submission, error) {
	return s.item, nil
}

func TestAuditNormalizedAssetMustBelongToCurrentPageExamAndRun(t *testing.T) {
	now := time.Now().UTC()
	expires := now.Add(time.Minute)
	run := Run{ID: "run", TenantID: "tenant", SubmissionID: "submission", SubmissionPageID: "page", CreatedAt: now, LeaseExpiresAt: &expires}
	valid := files.FileAsset{ID: "file", TenantID: "tenant", OwnerType: "submission_page_normalized", OwnerID: "page", ExamID: "exam", SubmissionID: "submission", ContentType: "image/png", HashSHA256: "hash", CreatedAt: now.Add(time.Second)}
	for _, scenario := range []string{"valid", "wrong_page", "wrong_exam", "wrong_submission", "generic", "same_page_duplicate", "after_lease", "missing_hash"} {
		t.Run(scenario, func(t *testing.T) {
			asset := valid
			switch scenario {
			case "wrong_page":
				asset.OwnerID = "other"
			case "wrong_exam":
				asset.ExamID = "other"
			case "wrong_submission":
				asset.SubmissionID = "other"
			case "generic":
				asset.OwnerType = "generic"
			case "same_page_duplicate":
				asset.CreatedAt = now.Add(-time.Second)
			case "after_lease":
				asset.CreatedAt = expires.Add(time.Second)
			case "missing_hash":
				asset.HashSHA256 = ""
			}
			h := Handler{store: auditNormalizedRunStore{run: run}, files: auditNormalizedFileStore{asset: asset}, submissions: auditNormalizedSubmissionStore{item: submission.Submission{ID: "submission", ExamID: "exam"}}}
			req := httptest.NewRequest(http.MethodPost, "/result", nil)
			req.SetPathValue("runId", "run")
			rec := httptest.NewRecorder()
			allowed := h.validateNormalizedResultAsset(rec, req, auth.User{TenantID: "tenant"}, ResultInput{NormalizedFileAssetID: "file"})
			if allowed != (scenario == "valid" || scenario == "same_page_duplicate") {
				t.Fatalf("allowed=%v status=%d body=%s", allowed, rec.Code, rec.Body.String())
			}
		})
	}
}
