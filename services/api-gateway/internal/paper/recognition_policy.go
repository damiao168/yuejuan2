package paper

import (
	"encoding/json"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

const paperRecognitionPolicyVersion = "paper-recognition.v5"

type PaperRecognitionPolicy struct {
	Version                   string  `json:"version"`
	SubjectCode               string  `json:"subject_code"`
	FormulaEnabled            bool    `json:"formula_enabled"`
	FormulaMode               string  `json:"formula_mode"`
	LayoutModel               string  `json:"layout_model,omitempty"`
	PrimaryFormulaModel       string  `json:"primary_formula_model,omitempty"`
	FallbackFormulaModel      string  `json:"fallback_formula_model,omitempty"`
	ROIPaddingPixels          int     `json:"roi_padding_pixels,omitempty"`
	MaxROIPaddingPixels       int     `json:"max_roi_padding_pixels,omitempty"`
	MaxPaddingHeightRatio     float64 `json:"max_padding_height_ratio,omitempty"`
	MinDetectorScore          float64 `json:"min_detector_score,omitempty"`
	AcceptDetectorScore       float64 `json:"accept_detector_score,omitempty"`
	MaxEdgeInkRatio           float64 `json:"max_edge_ink_ratio,omitempty"`
	FormulaBatchSize          int     `json:"formula_batch_size,omitempty"`
	FormulaBatchMode          string  `json:"formula_batch_mode,omitempty"`
	ValidationVersion         string  `json:"validation_version,omitempty"`
	RenderSimilarityThreshold float64 `json:"render_similarity_threshold,omitempty"`
}

func paperRecognitionPolicy(subject string) (PaperRecognitionPolicy, bool) {
	code, ok := assessment.NormalizeSubjectCode(subject)
	if !ok {
		return PaperRecognitionPolicy{}, false
	}
	policy := PaperRecognitionPolicy{
		Version:        paperRecognitionPolicyVersion,
		SubjectCode:    string(code),
		FormulaEnabled: code == assessment.SubjectMathematics,
		FormulaMode:    "text_only",
	}
	if policy.FormulaEnabled {
		policy.FormulaMode = "adaptive_batch_guarded"
		policy.LayoutModel = "PP-DocLayout_plus-L"
		policy.PrimaryFormulaModel = "PP-FormulaNet_plus-M"
		policy.FallbackFormulaModel = "PP-FormulaNet_plus-L"
		policy.ROIPaddingPixels = 12
		policy.MaxROIPaddingPixels = 96
		policy.MaxPaddingHeightRatio = 0.75
		policy.MinDetectorScore = 0.30
		policy.AcceptDetectorScore = 0.65
		policy.MaxEdgeInkRatio = 0.08
		// Throughput settings belong to a measured deployment profile, not to
		// the immutable semantic recognition policy. Older v3 tasks retain
		// their snapshotted numeric batch size for reproducibility.
		policy.FormulaBatchMode = "deployment_profile"
		policy.ValidationVersion = "latex-structure-render-v2"
		policy.RenderSimilarityThreshold = 0.34
	}
	return policy, true
}

func paperRecognitionPolicyJSON(policy PaperRecognitionPolicy) ([]byte, string) {
	raw, _ := json.Marshal(policy)
	return raw, contentHash(raw)
}
