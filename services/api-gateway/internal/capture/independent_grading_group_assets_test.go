package capture

import (
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"strings"
	"testing"
)

func TestIndependentGroupCompletionAcceptsSharedOnlyMemberAndRejectsFakeChild(t *testing.T) {
	for _, scenario := range []string{"valid", "missing scope", "fake child asset", "missing member", "child marked shared"} {
		t.Run(scenario, func(t *testing.T) {
			store := files.NewMemoryStore()
			asset, err := store.Create(t.Context(), files.CreateAssetInput{TenantID: "tenant", ExamID: "exam", OwnerType: "answer_segment_crop", OwnerID: "run", OriginalName: "group.png", ContentType: "image/png", SizeBytes: 10, HashSHA256: strings.Repeat("a", 64), StorageBucket: "b", StorageKey: "g"})
			if err != nil {
				t.Fatal(err)
			}
			box := paper.TemplateRegionBBox{X: .1, Y: .2, Width: .8, Height: .3}
			regions := []paper.LayoutRegion{{QuestionID: "q12", X: .15, Y: .3, Width: .2, Height: .1}}
			groups := []paper.TemplateGradingGroup{{GroupID: "fill", QuestionIDs: []string{"q12", "q14"}, BBox: box}}
			shared := map[string]any{"id": "fill", "question_ids": []string{"q12", "q14"}, "file_asset_id": asset.ID, "sha256": asset.HashSHA256, "normalized_bbox": map[string]float64{"x": .1, "y": .2, "width": .8, "height": .3}, "pixel_bbox": map[string]float64{"x": 30, "y": 40, "width": 240, "height": 60}}
			crops := []SegmentCropInput{{QuestionID: "q12", NormalizedBBox: map[string]any{"x": .15, "y": .3, "width": .2, "height": .1, "grading_group": shared}}, {QuestionID: "q14", FileAssetID: asset.ID, SHA256: asset.HashSHA256, NormalizedBBox: map[string]any{"x": .1, "y": .2, "width": .8, "height": .3, "answer_scope": "grading_group", "grading_group": shared}}}
			switch scenario {
			case "missing scope":
				delete(crops[1].NormalizedBBox, "answer_scope")
			case "fake child asset":
				crops[1].FileAssetID = "invented-child"
			case "missing member":
				crops = crops[:1]
			case "child marked shared":
				crops[0].NormalizedBBox["answer_scope"] = "grading_group"
			}
			err = (&Handler{files: store}).validateGradingGroupAssets(t.Context(), "tenant", "exam", "answer_segment_crop", "run", map[string]any{"question_regions": regions, "grading_groups": groups}, crops)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("scenario=%s err=%v", scenario, err)
			}
		})
	}
}
