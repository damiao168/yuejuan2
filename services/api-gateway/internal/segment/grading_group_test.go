package segment_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

func gradingGroupBBox(assetID string) map[string]any {
	return map[string]any{"x": .1, "y": .2, "width": .2, "height": .1, "grading_group": map[string]any{
		"id": "fill-12-14", "question_ids": []string{"q12", "q13", "q14"}, "file_asset_id": assetID, "sha256": strings.Repeat("a", 64),
		"normalized_bbox": map[string]float64{"x": .05, "y": .1, "width": .85, "height": .4},
		"pixel_bbox":      map[string]float64{"x": 15, "y": 20, "width": 255, "height": 80},
	}}
}

func TestSharedGradingImageKeepsIndividualImageAndCurrentRegistrationBoundary(t *testing.T) {
	ctx := context.Background()
	fileStore := files.NewMemoryStore()
	objects := files.NewMemoryObjectStorage()
	asset, err := fileStore.Create(ctx, files.CreateAssetInput{TenantID: tenantID, ExamID: "exam-1", OwnerType: "answer_segment_crop", OwnerID: "run-1", OriginalName: "shared.png", ContentType: "image/png", SizeBytes: 6, HashSHA256: strings.Repeat("a", 64), StorageBucket: "answers", StorageKey: "group.png"})
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(ctx, "answers", "group.png", bytes.NewBufferString("shared"), 6, "image/png"); err != nil {
		t.Fatal(err)
	}
	evidence := segment.SegmentEvidence{SegmentID: "s12", QuestionID: "q12", ExamID: "exam-1", RegistrationRunID: "run-1", ProcessingStatus: "completed", RegistrationStatus: "completed", NormalizedBBox: gradingGroupBBox(asset.ID), CropFileAssetID: "separate-child", CropSHA256: "child-hash"}
	service := segment.NewImageService(evidenceStore{evidence: evidence}, nil, fileStore, objects)
	group, err := service.GetGradingGroup(ctx, tenantID, "s12")
	if err != nil || group.ID != "fill-12-14" || len(group.QuestionIDs) != 3 {
		t.Fatalf("missing shared context: %#v %v", group, err)
	}
	resource, err := service.ReadGradingGroupImage(ctx, tenantID, "s12")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := resource.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, _ := io.ReadAll(reader)
	if string(data) != "shared" || evidence.CropFileAssetID != "separate-child" || evidence.NormalizedBBox["width"] != .2 {
		t.Fatal("shared reading replaced the child artifact")
	}
	if _, err := service.ReadGradingGroupImage(ctx, "other-tenant", "s12"); !errors.Is(err, segment.ErrNotFound) {
		t.Fatal("cross tenant group exposed")
	}
	evidence.RegistrationRunID = "run-2"
	service = segment.NewImageService(evidenceStore{evidence: evidence}, nil, fileStore, objects)
	if _, err := service.ReadGradingGroupImage(ctx, tenantID, "s12"); !errors.Is(err, segment.ErrEvidenceUnavailable) {
		t.Fatal("previous registration group accepted")
	}
}

func TestSharedGradingMetadataMustContainCurrentQuestionAndChild(t *testing.T) {
	for _, scenario := range []string{"missing member", "duplicate member", "outside child", "invalid hash", "invalid bbox"} {
		t.Run(scenario, func(t *testing.T) {
			bbox := gradingGroupBBox("asset")
			group := bbox["grading_group"].(map[string]any)
			switch scenario {
			case "missing member":
				group["question_ids"] = []string{"q13", "q14"}
			case "duplicate member":
				group["question_ids"] = []string{"q12", "q12"}
			case "outside child":
				bbox["x"] = .95
			case "invalid hash":
				group["sha256"] = "secret"
			case "invalid bbox":
				group["normalized_bbox"] = map[string]float64{"x": 0}
			}
			if _, err := segment.ParseGradingGroupCrop(bbox, "q12"); !errors.Is(err, segment.ErrInvalidInput) {
				t.Fatal("invalid shared metadata accepted")
			}
		})
	}
	if group, err := segment.ParseGradingGroupCrop(map[string]any{}, "q12"); group != nil || err != nil {
		t.Fatal("legacy ungrouped crop changed")
	}
}

func TestSharedOnlyAnswerHasGroupContextButNoIndividualImage(t *testing.T) {
	bbox := gradingGroupBBox("asset")
	bbox["answer_scope"] = "grading_group"
	bbox["x"], bbox["y"], bbox["width"], bbox["height"] = .05, .1, .85, .4
	if group, err := segment.ParseGradingGroupCrop(bbox, "q14"); err != nil || group == nil {
		t.Fatalf("shared-only member lost: %#v %v", group, err)
	}
	evidence := segment.SegmentEvidence{SegmentID: "s14", QuestionID: "q14", NormalizedBBox: bbox, ProcessingStatus: "completed", RegistrationStatus: "completed", CropFileAssetID: "asset", CropSHA256: strings.Repeat("a", 64)}
	service := segment.NewImageService(evidenceStore{evidence: evidence}, nil, nil, nil)
	if _, err := service.ReadCropImage(context.Background(), tenantID, "s14"); !errors.Is(err, segment.ErrEvidenceUnavailable) {
		t.Fatal("shared image exposed as individual")
	}
	if scope, err := service.GetAnswerScope(context.Background(), tenantID, "s14"); err != nil || scope != "grading_group" {
		t.Fatal("answer scope missing")
	}
}
