package capture

import (
	"context"
	"net/http"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type correctionCallbackStore struct {
	*MemoryStore
	correction RegistrationCorrection
	command    CorrectionPreviewResultInput
	commandErr error
}

func (s *correctionCallbackStore) GetRegistrationCorrection(_ context.Context, tenantID, id string) (RegistrationCorrection, error) {
	if tenantID != "tenant" || id != s.correction.ID {
		return RegistrationCorrection{}, ErrNotFound
	}
	return s.correction, nil
}

func (s *correctionCallbackStore) GetRegistrationCorrectionContext(_ context.Context, tenantID, runID string) (RegistrationCorrectionContext, error) {
	if tenantID != "tenant" || runID != s.correction.BaseRegistrationRunID {
		return RegistrationCorrectionContext{}, ErrNotFound
	}
	return RegistrationCorrectionContext{ExamID: "exam", RegistrationRunID: runID}, nil
}

func (s *correctionCallbackStore) SubmitRegistrationCorrectionResultCommand(_ context.Context, tenantID, id string, input CorrectionPreviewResultInput, _ workerruntime.Store) (RegistrationCorrection, error) {
	if tenantID != "tenant" || id != s.correction.ID {
		return RegistrationCorrection{}, ErrNotFound
	}
	s.command = input
	if s.commandErr != nil {
		return RegistrationCorrection{}, s.commandErr
	}
	out := s.correction
	out.Status = "preview_ready"
	out.PreviewRegisteredFileAssetID = input.PreviewRegisteredFileAssetID
	return out, nil
}

func TestRegistrationCorrectionHTTPRejectsAssetsFromAnotherCorrection(t *testing.T) {
	runtime, assets := workerruntime.NewMemoryStore(), files.NewMemoryStore()
	task := remediationMemoryRuntimeTask(t, runtime, "page_registration_correction", "correction")
	store := &correctionCallbackStore{MemoryStore: NewMemoryStore(), correction: RegistrationCorrection{
		ID: "correction", BaseRegistrationRunID: "base", RuntimeTaskID: task.ID, Status: "preview_running",
	}}
	source := createCaptureWorkflowAsset(t, assets, "exam", "source-hash")
	store.registrations["base"] = RegistrationRun{ID: "base", SourceFileAssetID: source.ID}

	createPreview := func(owner, hash string) files.FileAsset {
		t.Helper()
		asset, err := assets.Create(t.Context(), files.CreateAssetInput{TenantID: "tenant", ExamID: "exam", OwnerType: "page_registration_correction_preview", OwnerID: owner, HashSHA256: hash, SizeBytes: 100})
		if err != nil {
			t.Fatal(err)
		}
		return asset
	}
	preview := createPreview("correction", "corrected-page")
	crop := createPreview("correction", "corrected-crop")
	otherPreview := createPreview("other-correction", "other-corrected-page")
	otherCrop := createPreview("other-correction", "other-corrected-crop")
	audit := &collectionAuditRecorder{}
	mux := http.NewServeMux()
	handler := NewHandler(store, assets, nil, runtime, audit)
	mux.HandleFunc("POST /api/v1/internal/page-registration-corrections/{id}/result", handler.CompleteRegistrationCorrection)
	const path = "/api/v1/internal/page-registration-corrections/correction/result"
	valid := CorrectionPreviewResultInput{
		TaskID: task.ID, LeaseToken: task.LeaseToken, ResultVersion: "v1", PreviewRegisteredFileAssetID: preview.ID, PreviewRegisteredSHA256: preview.HashSHA256,
		Segments: []SegmentCropInput{{QuestionID: "question", FileAssetID: crop.ID, SHA256: crop.HashSHA256}},
	}

	// 同一场考试可有多次修正。预览和切片必须属于当前修正，避免把别的答卷产物应用到本页。
	invalid := valid
	invalid.PreviewRegisteredFileAssetID, invalid.PreviewRegisteredSHA256 = otherPreview.ID, otherPreview.HashSHA256
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path, captureWorkflowBody(t, invalid), nil, nil), http.StatusBadRequest, "correction_preview_asset_invalid")
	invalid = valid
	invalid.Segments = []SegmentCropInput{{QuestionID: "question", FileAssetID: otherCrop.ID, SHA256: otherCrop.HashSHA256}}
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path, captureWorkflowBody(t, invalid), nil, nil), http.StatusBadRequest, "correction_segment_asset_invalid")
	if store.command.TaskID != "" || len(audit.events) != 0 {
		t.Fatalf("invalid preview reached storage or success audit: input=%+v events=%+v", store.command, audit.events)
	}

	store.commandErr = workerruntime.ErrLeaseMismatch
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path, captureWorkflowBody(t, valid), nil, nil), http.StatusConflict, "capture_worker_lease_invalid")
	if len(audit.events) != 0 {
		t.Fatalf("failed preview wrote success audit: %+v", audit.events)
	}
	store.commandErr = nil
	type response struct{ Correction RegistrationCorrection }
	out := captureWorkflowJSON[response](t, captureWorkflowRequest(t, mux, "POST", path, captureWorkflowBody(t, valid), nil, nil), http.StatusOK).Correction
	if out.Status != "preview_ready" || out.PreviewRegisteredFileAssetID != preview.ID || len(store.command.Segments) != 1 || store.command.Segments[0].FileAssetID != crop.ID {
		t.Fatalf("valid preview lost validated assets: result=%+v command=%+v", out, store.command)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "page.registration_correction_preview_ready" || audit.events[0].TargetID != "correction" {
		t.Fatalf("valid preview did not record the correction audit: %+v", audit.events)
	}
}
