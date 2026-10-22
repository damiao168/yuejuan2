package capture

import (
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestGradingGroupCompletionMatchesFrozenTemplateAndAssetOwner(t *testing.T) {
	for _, scenario := range []string{"valid", "foreign owner", "wrong group", "missing child", "moved child", "wrong members", "changed outer box"} {
		t.Run(scenario, func(t *testing.T) {
			fileStore := files.NewMemoryStore()
			owner := "run-1"
			if scenario == "foreign owner" {
				owner = "run-2"
			}
			asset, err := fileStore.Create(t.Context(), files.CreateAssetInput{TenantID: "tenant", ExamID: "exam", OwnerType: "answer_segment_crop", OwnerID: owner, OriginalName: "shared.png", ContentType: "image/png", SizeBytes: 10, HashSHA256: strings.Repeat("a", 64), StorageBucket: "b", StorageKey: "k"})
			if err != nil {
				t.Fatal(err)
			}
			box := paper.TemplateRegionBBox{X: .1, Y: .2, Width: .8, Height: .3}
			regions, crops := []paper.LayoutRegion{}, []SegmentCropInput{}
			for index, id := range []string{"q12", "q13", "q14"} {
				x := .15 + float64(index)*.23
				regions = append(regions, paper.LayoutRegion{QuestionID: id, X: x, Y: .3, Width: .2, Height: .1, GradingGroupID: "fill", GradingBBox: &box})
				crops = append(crops, SegmentCropInput{QuestionID: id, NormalizedBBox: map[string]any{"x": x, "y": .3, "width": .2, "height": .1, "grading_group": map[string]any{"id": "fill", "question_ids": []string{"q12", "q13", "q14"}, "file_asset_id": asset.ID, "sha256": strings.Repeat("a", 64), "normalized_bbox": map[string]float64{"x": .1, "y": .2, "width": .8, "height": .3}, "pixel_bbox": map[string]float64{"x": 30, "y": 40, "width": 240, "height": 60}}}})
			}
			group := crops[0].NormalizedBBox["grading_group"].(map[string]any)
			switch scenario {
			case "wrong group":
				group["id"] = "other"
			case "missing child":
				crops = crops[:2]
			case "moved child":
				crops[0].NormalizedBBox["x"] = .16
			case "wrong members":
				group["question_ids"] = []string{"q12", "q13", "unrelated"}
			case "changed outer box":
				group["normalized_bbox"].(map[string]float64)["x"] = .09
			}
			handler := &Handler{files: fileStore}
			err = handler.validateGradingGroupAssets(t.Context(), "tenant", "exam", "answer_segment_crop", "run-1", map[string]any{"question_regions": regions}, crops)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("scenario=%s err=%v", scenario, err)
			}
		})
	}
}
