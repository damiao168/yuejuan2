package evidence

import (
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type Engine struct {
	MinOCRConfidence float64
}

func NewEngine() *Engine {
	return &Engine{MinOCRConfidence: 0.8}
}

// 这里只验证 AI 结果是否能被来源事实支持；验证失败或 OCR 置信度不足时标记人工复核，不直接改成绩。
func (e *Engine) Verify(ctx Context) VerificationResult {
	result := VerificationResult{
		Passed:         true,
		Failed:         []Issue{},
		Warnings:       []Issue{},
		CorrectedFlags: []string{},
	}
	if ctx.Grade.SuggestedScore > ctx.Grade.MaxScore {
		result.fail("score_exceeds_max", "suggested_score exceeds max_score")
	}
	if len(ctx.Grade.Evidence) == 0 {
		result.fail("empty_evidence", "ai_grade evidence is empty")
	}
	if ctx.OCRConfidence != nil && *ctx.OCRConfidence < e.MinOCRConfidence {
		result.warn("low_ocr_confidence", "answer OCR confidence is below evidence verification threshold")
		result.CorrectedFlags = appendFlag(result.CorrectedFlags, "low_ocr_confidence")
		result.NeedsHumanReview = true
	}
	// rubric 只允许引用已存在的评分点；匹配分数还要与建议总分一致，避免证据和分数各说一套。
	rubricPoints := rubricPointScores(ctx.Rubric)
	if len(rubricPoints) == 0 && (len(ctx.Grade.MatchedPoints) > 0 || len(ctx.Grade.MissingPoints) > 0) {
		result.fail("rubric_missing", "rubric points are required when grade references matched or missing points")
	}
	if len(rubricPoints) > 0 {
		for _, point := range append(ctx.Grade.MatchedPoints, ctx.Grade.MissingPoints...) {
			if _, ok := rubricPoints[point.Code]; !ok {
				result.fail("rubric_point_not_found", "point "+point.Code+" is not present in rubric")
			}
		}
	}
	matchedTotal := 0.0
	for _, point := range ctx.Grade.MatchedPoints {
		matchedTotal += point.Score
	}
	if math.Abs(matchedTotal-ctx.Grade.SuggestedScore) > 0.0001 {
		result.fail("matched_score_mismatch", "suggested_score does not equal matched_points score total")
	}
	// 文本校验只做大小写和空白归一化，不把未出现在答卷中的证据当成有效依据。
	answerText := normalizeText(ctx.AnswerText)
	for _, evidence := range ctx.Grade.Evidence {
		if strings.TrimSpace(evidence.AnswerText) == "" {
			result.fail("empty_evidence_text", "evidence answer_text is empty")
		} else if !strings.Contains(answerText, normalizeText(evidence.AnswerText)) {
			result.fail("evidence_text_not_found", "evidence answer_text is not found in answer segment text")
		}
		if len(evidence.BBox) > 0 && !bboxInside(evidence.BBox, ctx.AnswerSegmentBBox) {
			result.fail("evidence_bbox_out_of_segment", "evidence bbox is outside answer segment bbox")
		}
	}
	if len(result.Failed) > 0 {
		result.Passed = false
		result.NeedsHumanReview = true
		result.CorrectedFlags = appendFlag(result.CorrectedFlags, "evidence_verification_failed")
	}
	if ctx.Grade.NeedsHumanReview {
		result.NeedsHumanReview = true
	}
	return result
}

func (r *VerificationResult) fail(code string, message string) {
	r.Failed = append(r.Failed, Issue{Code: code, Message: message})
}

func (r *VerificationResult) warn(code string, message string) {
	r.Warnings = append(r.Warnings, Issue{Code: code, Message: message})
}

func rubricPointScores(rubric paper.Rubric) map[string]float64 {
	out := map[string]float64{}
	for _, point := range rubric.Points {
		if point.ID != "" {
			out[point.ID] = point.Score
		}
	}
	return out
}

func normalizeText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), ""))
}

// 坐标按 x、y、width、height 解释；内框四条边都必须落在答题区域内。
func bboxInside(inner []float64, outer []float64) bool {
	if len(inner) != 4 || len(outer) != 4 {
		return false
	}
	ix1, iy1 := inner[0], inner[1]
	ix2, iy2 := inner[0]+inner[2], inner[1]+inner[3]
	ox1, oy1 := outer[0], outer[1]
	ox2, oy2 := outer[0]+outer[2], outer[1]+outer[3]
	return ix1 >= ox1 && iy1 >= oy1 && ix2 <= ox2 && iy2 <= oy2
}

func appendFlag(flags []string, flag string) []string {
	for _, existing := range flags {
		if existing == flag {
			return flags
		}
	}
	return append(flags, flag)
}
