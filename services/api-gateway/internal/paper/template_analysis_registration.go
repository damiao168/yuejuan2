package paper

import "math"

// TemplateAnalysisRegistration 保存像素检测的定位基准，坐标始终对应原始显示页。
// 模型使用矫正副本时，工作节点须先逆变换候选，不能把两个坐标系混入模板。
type TemplateAnalysisRegistration struct {
	Version           string                  `json:"version"`
	Status            string                  `json:"status"`
	CoordinateSpace   string                  `json:"coordinate_space"`
	Width             int                     `json:"width,omitempty"`
	Height            int                     `json:"height,omitempty"`
	RegistrationMarks []LayoutRegion          `json:"registration_marks"`
	AnchorPoints      []TemplateAnalysisPoint `json:"anchor_points,omitempty"`
	StableQuad        []TemplateAnalysisPoint `json:"stable_quad"`
	Confidence        float64                 `json:"confidence,omitempty"`
}

type TemplateAnalysisPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// TemplateAnalysisFillGroup 是共框批阅范围；逐题候选与正式成绩仍保持各自的题号。
type TemplateAnalysisFillGroup struct {
	GroupID        string               `json:"group_id"`
	QuestionLabels []string             `json:"question_labels"`
	BBox           TemplateAnalysisBBox `json:"bbox"`
	ReasonCodes    []string             `json:"reason_codes"`
}

func validateTemplateAnalysisRegistration(value *TemplateAnalysisRegistration, input TemplatePageAnalysisInput) error {
	if value == nil {
		return nil
	}
	if value.Version != "four-corner-printed-anchors-v1" || value.CoordinateSpace != "source_normalized" {
		return ErrInvalidInput
	}
	if value.Status == "not_found" {
		if len(value.RegistrationMarks) != 0 || len(value.AnchorPoints) != 0 || len(value.StableQuad) != 0 {
			return ErrInvalidInput
		}
		return nil
	}
	if value.Status != "detected" || value.Width != input.Width || value.Height != input.Height || len(value.RegistrationMarks) != 4 || len(value.AnchorPoints) != 4 || len(value.StableQuad) != 4 || math.IsNaN(value.Confidence) || math.IsInf(value.Confidence, 0) || value.Confidence <= 0 || value.Confidence > 1 {
		return ErrInvalidInput
	}
	corners := []string{"top_left", "top_right", "bottom_right", "bottom_left"}
	for i, region := range value.RegistrationMarks {
		if validateRegion(region) != nil || region.ID == "" || region.Label != corners[i] || region.QuestionID != "" || len(region.OptionRegions) != 0 || region.Origin != "local_detector" || region.ReviewState != "suggested" || region.SemanticCheck != "supported" {
			return ErrInvalidInput
		}
		point := value.AnchorPoints[i]
		if !templateAnalysisPointValid(point) || point.X < region.X-2/float64(input.Width) || point.X > region.X+region.Width+2/float64(input.Width) || point.Y < region.Y-2/float64(input.Height) || point.Y > region.Y+region.Height+2/float64(input.Height) {
			return ErrInvalidInput
		}
	}
	area := 0.0
	for i, point := range value.StableQuad {
		if !templateAnalysisPointValid(point) {
			return ErrInvalidInput
		}
		next, after := value.StableQuad[(i+1)%4], value.StableQuad[(i+2)%4]
		if (next.X-point.X)*(after.Y-next.Y)-(next.Y-point.Y)*(after.X-next.X) <= 0 {
			return ErrInvalidInput
		}
		area += point.X*next.Y - next.X*point.Y
	}
	if area/2 < .4 {
		return ErrInvalidInput
	}
	return nil
}

func templateAnalysisPointValid(point TemplateAnalysisPoint) bool {
	return !math.IsNaN(point.X) && !math.IsNaN(point.Y) && !math.IsInf(point.X, 0) && !math.IsInf(point.Y, 0) && point.X >= 0 && point.X <= 1 && point.Y >= 0 && point.Y <= 1
}

func validateTemplateAnalysisFillGroups(result TemplatePageAnalysis, official []string) error {
	allowed, used, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, label := range official {
		allowed[label] = true
	}
	if len(result.FillGradingGroups) > 128 {
		return ErrInvalidInput
	}
	for _, group := range result.FillGradingGroups {
		if result.SemanticStatus != "completed" || group.GroupID == "" || len(group.GroupID) > 128 || ids[group.GroupID] || len(group.QuestionLabels) < 2 || len(group.QuestionLabels) > 128 || validateTemplateAnalysisBBox(group.BBox) != nil {
			return ErrInvalidInput
		}
		ids[group.GroupID] = true
		verified := false
		for _, code := range group.ReasonCodes {
			verified = verified || code == "LOCAL_FILL_SECTION_VERIFIED"
		}
		if !verified {
			return ErrInvalidInput
		}
		for _, label := range group.QuestionLabels {
			if !allowed[label] || used[label] {
				return ErrInvalidInput
			}
			used[label] = true
			count := 0
			for _, row := range result.Candidates {
				if row.PrintedQuestionLabel != nil && *row.PrintedQuestionLabel == label && row.Role == "answer" && row.SemanticCheck == "supported" && row.AssociationStatus == "associated" {
					if !templateAnalysisBoxContains(group.BBox, row.ProposedBBox) {
						return ErrInvalidInput
					}
					count++
				}
			}
			if count != 1 {
				return ErrInvalidInput
			}
		}
	}
	return nil
}

func templateAnalysisBoxContains(outer, inner TemplateAnalysisBBox) bool {
	return inner.X >= outer.X-1e-6 && inner.Y >= outer.Y-1e-6 && inner.X+inner.Width <= outer.X+outer.Width+1e-6 && inner.Y+inner.Height <= outer.Y+outer.Height+1e-6
}
