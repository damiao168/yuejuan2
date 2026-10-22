package paper

import (
	"errors"
	"math"
	"strings"
)

// TemplateRegionBBox 使用来源页的归一化坐标；共区外框与每道题的子框采用同一坐标系。
type TemplateRegionBBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// TemplateGradingGroup 独立保存整区与正式题目成员；缺少单题 ROI 的成员仍可用整区人工批阅。
// 分组不合并题目、分值或量规，也不把整区几何冒充该题的精确作答位置。
type TemplateGradingGroup struct {
	GroupID     string             `json:"group_id"`
	QuestionIDs []string           `json:"question_ids"`
	BBox        TemplateRegionBBox `json:"bbox"`
}

// ResolveTemplateGradingGroups 合并新页级分组与历史逐题附属字段。
// 两种表示同时存在时，旧字段须属于页级成员并使用相同外框，不能覆盖新的成员映射。
func ResolveTemplateGradingGroups(page TemplatePage) ([]TemplateGradingGroup, error) {
	groups := []TemplateGradingGroup{}
	indices := map[string]int{}
	for _, group := range page.GradingGroups {
		if _, exists := indices[group.GroupID]; exists {
			return nil, errors.New("duplicate grading group")
		}
		indices[group.GroupID] = len(groups)
		group.QuestionIDs = append([]string(nil), group.QuestionIDs...)
		groups = append(groups, group)
	}
	independentCount := len(groups)
	for _, region := range page.QuestionRegions {
		if region.GradingGroupID == "" && region.GradingBBox == nil {
			continue
		}
		if region.GradingGroupID == "" || region.GradingBBox == nil {
			return nil, errors.New("grading_group_id and grading_bbox must be supplied together")
		}
		index, exists := indices[region.GradingGroupID]
		if !exists {
			index = len(groups)
			indices[region.GradingGroupID] = index
			groups = append(groups, TemplateGradingGroup{GroupID: region.GradingGroupID, BBox: *region.GradingBBox})
		}
		group := &groups[index]
		if !gradingBoxesEqual(group.BBox, *region.GradingBBox) {
			return nil, errors.New("grading group members must share one grading_bbox")
		}
		found := false
		for _, id := range group.QuestionIDs {
			found = found || id == region.QuestionID
		}
		if index < independentCount {
			if !found {
				return nil, errors.New("legacy grading member conflicts with page grading group")
			}
		} else {
			if found {
				return nil, errors.New("duplicate grading group member")
			}
			group.QuestionIDs = append(group.QuestionIDs, region.QuestionID)
		}
	}
	return groups, nil
}

// 草稿先核对成员、页归属和几何；锁定及模板库复用再核对正式题型。
func validateTemplateGradingGroups(layout TemplateLayout, questions []Question) error {
	questionTypes := map[string]string{}
	for _, question := range questions {
		if question.Status != "deleted" {
			questionTypes[question.ID] = question.QuestionType
		}
	}
	groupPages, questionPages := map[string]int{}, map[string]int{}
	for _, page := range layout.Pages {
		for _, region := range page.QuestionRegions {
			questionPages[region.QuestionID] = page.PageNo
		}
	}
	memberGroups := map[string]string{}
	for _, page := range layout.Pages {
		for _, region := range append(append([]LayoutRegion{}, page.RegistrationMarks...), page.IdentityRegions...) {
			if region.GradingGroupID != "" || region.GradingBBox != nil {
				return errors.New("grading groups may only reference question regions")
			}
		}
		groups, err := ResolveTemplateGradingGroups(page)
		if err != nil {
			return err
		}
		for index, group := range groups {
			id, box := group.GroupID, group.BBox
			if strings.TrimSpace(id) != id || id == "" || len(id) > 128 || len(group.QuestionIDs) < 2 || len(group.QuestionIDs) > 500 {
				return errors.New("grading groups require an id and at least two question members")
			}
			if err := validateRegion(LayoutRegion{X: box.X, Y: box.Y, Width: box.Width, Height: box.Height}); err != nil {
				return err
			}
			if _, ok := groupPages[id]; ok {
				return errors.New("grading groups cannot span pages")
			}
			groupPages[id] = page.PageNo
			members := map[string]bool{}
			for _, questionID := range group.QuestionIDs {
				if strings.TrimSpace(questionID) != questionID || questionID == "" || members[questionID] || memberGroups[questionID] != "" {
					return errors.New("question may only belong to one grading group")
				}
				if otherPage, ok := questionPages[questionID]; ok && otherPage != page.PageNo {
					return errors.New("grading group member region is on another page")
				}
				if questions != nil && questionTypes[questionID] != "fill_blank" {
					return errors.New("only fill_blank questions may share a grading region")
				}
				members[questionID], memberGroups[questionID] = true, id
			}
			for _, region := range page.QuestionRegions {
				if members[region.QuestionID] && !gradingBoxContains(box, region) {
					return errors.New("grading_bbox must contain every member question region")
				}
				if !members[region.QuestionID] && gradingBoxOverlaps(box, region) {
					return errors.New("grading_bbox overlaps an unrelated question or identity region")
				}
			}
			for _, identity := range page.IdentityRegions {
				if gradingBoxOverlaps(box, identity) {
					return errors.New("grading_bbox overlaps an unrelated question or identity region")
				}
			}
			// 没有子题 ROI 的另一组也占据真实作答区域，不能因缺少子框漏过重叠检查。
			for _, other := range groups[index+1:] {
				if gradingBoxOverlaps(box, LayoutRegion{X: other.BBox.X, Y: other.BBox.Y, Width: other.BBox.Width, Height: other.BBox.Height}) {
					return errors.New("grading groups overlap")
				}
			}
		}
	}
	return nil
}

func gradingBoxContains(box TemplateRegionBBox, region LayoutRegion) bool {
	return box.X <= region.X+1e-6 && box.Y <= region.Y+1e-6 && box.X+box.Width >= region.X+region.Width-1e-6 && box.Y+box.Height >= region.Y+region.Height-1e-6
}

func gradingBoxOverlaps(box TemplateRegionBBox, region LayoutRegion) bool {
	return math.Min(box.X+box.Width, region.X+region.Width)-math.Max(box.X, region.X) > 1e-6 && math.Min(box.Y+box.Height, region.Y+region.Height)-math.Max(box.Y, region.Y) > 1e-6
}

func gradingBoxesEqual(a, b TemplateRegionBBox) bool {
	return math.Abs(a.X-b.X) <= 1e-6 && math.Abs(a.Y-b.Y) <= 1e-6 && math.Abs(a.Width-b.Width) <= 1e-6 && math.Abs(a.Height-b.Height) <= 1e-6
}
