package paper

import "testing"

func TestPaperRecognitionPolicyUsesMathOnlyFormulaRoute(t *testing.T) {
	mathPolicy, ok := paperRecognitionPolicy("数学")
	if !ok || !mathPolicy.FormulaEnabled || mathPolicy.PrimaryFormulaModel != "PP-FormulaNet_plus-M" || mathPolicy.FallbackFormulaModel != "PP-FormulaNet_plus-L" {
		t.Fatalf("unexpected mathematics policy: %#v, ok=%v", mathPolicy, ok)
	}
	if mathPolicy.FormulaBatchSize != 0 || mathPolicy.FormulaBatchMode != "deployment_profile" || mathPolicy.MaxROIPaddingPixels != 96 || mathPolicy.MaxPaddingHeightRatio != .75 || mathPolicy.AcceptDetectorScore != .65 || mathPolicy.ValidationVersion != "latex-structure-render-v2" {
		t.Fatalf("mathematics policy did not snapshot the governed adaptive formula pipeline: %#v", mathPolicy)
	}
	chinesePolicy, ok := paperRecognitionPolicy("chinese")
	if !ok || chinesePolicy.FormulaEnabled || chinesePolicy.FormulaMode != "text_only" {
		t.Fatalf("unexpected chinese policy: %#v, ok=%v", chinesePolicy, ok)
	}
}

func TestPaperRecognitionPolicyHashIsDeterministic(t *testing.T) {
	policy, _ := paperRecognitionPolicy("mathematics")
	firstJSON, firstHash := paperRecognitionPolicyJSON(policy)
	secondJSON, secondHash := paperRecognitionPolicyJSON(policy)
	if string(firstJSON) != string(secondJSON) || firstHash == "" || firstHash != secondHash {
		t.Fatalf("policy snapshot is not deterministic")
	}
}
