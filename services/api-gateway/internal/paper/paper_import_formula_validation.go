package paper

// 公式证据与归一化几何校验；accepted 的模型、裁片和选中证据必须保持一致。

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

func validPaperFormulaResult(documents []PaperImportParseDocument, regions []PaperImportFormulaRegion) bool {
	documentKeys := map[string]bool{}
	for _, document := range documents {
		documentKeys[fmt.Sprintf("%s:%d", document.SourceID, document.DocumentIndex)] = true
	}
	seen := map[string]bool{}
	for _, region := range regions {
		key := fmt.Sprintf("%s:%d:%d:%s", region.SourceID, region.DocumentIndex, region.PageNo, region.RegionID)
		if seen[key] || !documentKeys[fmt.Sprintf("%s:%d", region.SourceID, region.DocumentIndex)] || !validPaperFormulaRegion(region) {
			return false
		}
		seen[key] = true
	}
	return true
}

func validPaperFormulaRegion(region PaperImportFormulaRegion) bool {
	validValidationVersion := region.ValidationVersion == "latex-structure-render-v1" || region.ValidationVersion == "latex-structure-render-v2"
	if region.PageNo <= 0 || region.RegionID == "" || len(region.BBox) != 4 || region.DetectorModel != "PP-DocLayout_plus-L" || !finiteUnit(region.DetectorConfidence) || !finiteUnit(region.EdgeInkRatio) || len(region.CropSHA256) != 64 || (region.Status != "accepted" && region.Status != "review_required") || len(region.Candidates) > 2 || region.RecropCount < 0 || region.RecropCount > 8 || !validValidationVersion {
		return false
	}
	if region.Status == "accepted" && (!region.CropComplete || len(region.Candidates) == 0) {
		return false
	}
	if !validPaperFormulaBBox(region.BBox) {
		return false
	}
	selectedMatches, valid := validPaperFormulaCandidates(region)
	if !valid {
		return false
	}
	// 待核对区域允许没有模型候选；已接受区域必须选中一条校验通过的同模型、同 LaTeX 证据。
	return region.Status != "accepted" || (selectedMatches && strings.TrimSpace(region.SelectedLatex) != "")
}

func validPaperFormulaBBox(box []float64) bool {
	for index, value := range box {
		if !math.IsNaN(value) && !math.IsInf(value, 0) && ((index < 2 && value >= 0) || (index >= 2 && value > 0)) {
			continue
		}
		return false
	}
	return true
}

func validPaperFormulaCandidates(region PaperImportFormulaRegion) (bool, bool) {
	models := map[string]bool{}
	selectedMatches := false
	for _, candidate := range region.Candidates {
		if models[candidate.ModelVersion] || !validPaperFormulaCandidate(candidate) {
			return false, false
		}
		models[candidate.ModelVersion] = true
		if candidate.ModelVersion == region.SelectedModel && candidate.CanonicalLatex == region.SelectedLatex && candidate.Valid {
			selectedMatches = true
		}
	}
	return selectedMatches, true
}

func validPaperFormulaCandidate(candidate PaperImportFormulaCandidate) bool {
	if (candidate.ModelVersion != "PP-FormulaNet_plus-M" && candidate.ModelVersion != "PP-FormulaNet_plus-L") || !finiteUnit(candidate.Confidence) || (candidate.RenderSimilarity != nil && !finiteUnit(*candidate.RenderSimilarity)) {
		return false
	}
	if candidate.ValidationAction != "accept" && candidate.ValidationAction != "retry_l" && candidate.ValidationAction != "review" && candidate.ValidationAction != "recrop" {
		return false
	}
	return candidate.Valid == (candidate.SyntaxValid && candidate.StructureValid && candidate.ValidationAction == "accept")
}

func finiteUnit(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func selectedFormulaConfidence(region PaperImportFormulaRegion) float64 {
	for _, candidate := range region.Candidates {
		if candidate.ModelVersion == region.SelectedModel {
			// 当前 Paddle FormulaRecognition 构建不一定提供识别分数。
			// 将此时的零视为不可用，而非确定失败，但也绝不能仅用检测器分数替代：
			// 这种情况下，重新渲染后的相似度是唯一的识别级证据。
			if candidate.Confidence > 0 {
				return min(region.DetectorConfidence, candidate.Confidence)
			}
			if candidate.RenderSimilarity != nil {
				return min(region.DetectorConfidence, *candidate.RenderSimilarity)
			}
			return min(region.DetectorConfidence, 0.5)
		}
	}
	return 0
}

func paperBBoxOverlap(value any, region []float64) bool {
	box, ok := paperBBox(value)
	if !ok || len(region) != 4 {
		return false
	}
	ax2, ay2 := box[0]+box[2], box[1]+box[3]
	bx2, by2 := region[0]+region[2], region[1]+region[3]
	w, h := min(ax2, bx2)-max(box[0], region[0]), min(ay2, by2)-max(box[1], region[1])
	intersection := w * h
	return w > 0 && h > 0 && intersection >= min(box[2]*box[3], region[2]*region[3])*0.5
}

func paperBBox(value any) ([]float64, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var box []float64
	if json.Unmarshal(raw, &box) != nil || len(box) != 4 || box[2] <= 0 || box[3] <= 0 {
		return nil, false
	}
	return box, true
}
