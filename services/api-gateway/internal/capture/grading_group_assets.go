package capture

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

// validateGradingGroupAssets 把共享图绑定回任务冻结的组成员与外框；worker 不能另造组或借同校资产替换。
// 元数据继续随每题 normalized_bbox 保存，因此旧切片、修正应用及回滚无需另一套分组存储。
func (h *Handler) validateGradingGroupAssets(ctx context.Context, tenantID, examID, ownerType, ownerID string, payload map[string]any, crops []SegmentCropInput) error {
	var page paper.TemplatePage
	raw, err := json.Marshal(payload)
	if err != nil {
		return ErrInvalidInput
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return ErrInvalidInput
	}
	byQuestion := map[string]paper.LayoutRegion{}
	groupMembers := map[string]map[string]bool{}
	groupByQuestion := map[string]paper.TemplateGradingGroup{}
	groups, err := paper.ResolveTemplateGradingGroups(page)
	if err != nil {
		return ErrInvalidInput
	}
	for _, group := range groups {
		groupMembers[group.GroupID] = map[string]bool{}
		for _, id := range group.QuestionIDs {
			groupMembers[group.GroupID][id] = true
			groupByQuestion[id] = group
		}
	}
	for _, region := range page.QuestionRegions {
		byQuestion[region.QuestionID] = region
	}
	seen, groupAssets := map[string]bool{}, map[string]*segment.GradingGroupCrop{}
	for _, crop := range crops {
		group, err := segment.ParseGradingGroupCrop(crop.NormalizedBBox, crop.QuestionID)
		if err != nil {
			return ErrInvalidInput
		}
		region, hasChild := byQuestion[crop.QuestionID]
		expected, isMember := groupByQuestion[crop.QuestionID]
		if group == nil {
			if isMember || segment.AnswerScope(crop.NormalizedBBox) == "grading_group" {
				return ErrInvalidInput
			}
			continue
		}
		if seen[crop.QuestionID] || !isMember || expected.GroupID != group.ID || len(group.QuestionIDs) != len(groupMembers[group.ID]) {
			return ErrInvalidInput
		}
		seen[crop.QuestionID] = true
		for _, id := range group.QuestionIDs {
			if !groupMembers[group.ID][id] {
				return ErrInvalidInput
			}
		}
		box := expected.BBox
		geometry := map[string]float64{"x": region.X, "y": region.Y, "width": region.Width, "height": region.Height}
		if !hasChild {
			// 缺子题位置只能引用同组共享资产；它不是可供逐题模型识别的精确裁图。
			if segment.AnswerScope(crop.NormalizedBBox) != "grading_group" || crop.FileAssetID != group.FileAssetID || crop.SHA256 != group.SHA256 {
				return ErrInvalidInput
			}
			geometry = map[string]float64{"x": box.X, "y": box.Y, "width": box.Width, "height": box.Height}
		} else if segment.AnswerScope(crop.NormalizedBBox) != "question" {
			return ErrInvalidInput
		}
		for key, value := range geometry {
			actual, ok := crop.NormalizedBBox[key].(float64)
			if !ok || math.Abs(actual-value) > 1e-6 {
				return ErrInvalidInput
			}
		}
		for key, value := range map[string]float64{"x": box.X, "y": box.Y, "width": box.Width, "height": box.Height} {
			if math.Abs(group.NormalizedBBox[key]-value) > 1e-6 {
				return ErrInvalidInput
			}
		}
		if previous := groupAssets[group.ID]; previous != nil && !reflect.DeepEqual(previous, group) {
			return ErrInvalidInput
		}
		groupAssets[group.ID] = group
		asset, err := h.files.Get(ctx, tenantID, group.FileAssetID)
		if err != nil || asset.ExamID != examID || asset.OwnerType != ownerType || asset.OwnerID != ownerID || asset.HashSHA256 != group.SHA256 || asset.DeletedAt != nil || !strings.HasPrefix(asset.ContentType, "image/") || asset.SizeBytes <= 0 {
			return ErrInvalidInput
		}
	}
	for _, members := range groupMembers {
		for id := range members {
			if !seen[id] {
				return ErrInvalidInput
			}
		}
	}
	return nil
}
