package segment_test

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

func TestCropImageReaderFailsClosedOnOwnerMismatch(t *testing.T) {
	fileStore := files.NewMemoryStore()
	asset, err := fileStore.Create(context.Background(), files.CreateAssetInput{
		TenantID: tenantID, ExamID: "exam-1", OwnerType: "generic", OwnerID: "registration-1",
		OriginalName: "crop.png", ContentType: "image/png", SizeBytes: 3,
		HashSHA256: "crop-hash", StorageBucket: "answers", StorageKey: "crop.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := evidenceStore{evidence: segment.SegmentEvidence{
		SegmentID: "segment-1", ExamID: "exam-1", RegistrationRunID: "registration-1",
		CropFileAssetID: asset.ID, CropSHA256: "crop-hash",
		ProcessingStatus: "completed", RegistrationStatus: "completed",
	}}
	reader := segment.NewImageService(store, nil, fileStore, files.NewMemoryObjectStorage())
	if _, err := reader.ReadCropImage(context.Background(), tenantID, "segment-1"); !errors.Is(err, segment.ErrEvidenceUnavailable) {
		t.Fatalf("wrong owner: got %v, want evidence unavailable", err)
	}
	if _, err := reader.ReadCropImage(context.Background(), "other-tenant", "segment-1"); !errors.Is(err, segment.ErrNotFound) {
		t.Fatalf("cross-tenant segment: got %v, want not found", err)
	}
}
