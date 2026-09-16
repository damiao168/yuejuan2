package paper

import (
	"strings"
	"testing"
)

func acceptedFormulaRegion() PaperImportFormulaRegion {
	latex := `x^{2}+1`
	return PaperImportFormulaRegion{SourceID: "source", DocumentIndex: 0, PageNo: 1, RegionID: "f1", BBox: []float64{10, 20, 80, 20}, DetectorModel: "PP-DocLayout_plus-L", DetectorConfidence: .9, CropSHA256: strings.Repeat("a", 64), EdgeInkRatio: .01, CropComplete: true, ValidationVersion: "latex-structure-render-v1", Candidates: []PaperImportFormulaCandidate{{ModelVersion: "PP-FormulaNet_plus-M", RawLatex: latex, CanonicalLatex: latex, Confidence: .91, Valid: true, SyntaxValid: true, StructureValid: true, ValidationAction: "accept"}}, SelectedLatex: latex, SelectedModel: "PP-FormulaNet_plus-M", Status: "accepted"}
}

func TestMergePaperFormulaResultPreservesMixedTextAsSegments(t *testing.T) {
	input := PaperImportParseRequest{Documents: []PaperImportParseDocument{{
		SourceID: "source", DocumentIndex: 0,
		Blocks: []PaperImportOCRBlock{{
			SourceID: "source", DocumentIndex: 0, PageNo: 1, BlockID: "mixed",
			Text: "已知函数f(x)=x²+1，则求其最小值", BBox: []float64{0, 20, 220, 22},
		}},
	}}}
	region := acceptedFormulaRegion()
	region.BBox = []float64{55, 20, 85, 22}
	region.SelectedLatex = `f(x)=x^{2}+1`
	region.Candidates[0].RawLatex = region.SelectedLatex
	region.Candidates[0].CanonicalLatex = region.SelectedLatex
	merged := mergePaperFormulaResult(input, []PaperImportFormulaRegion{region})
	blocks := merged.Documents[0].Blocks
	if len(blocks) != 1 || blocks[0].Kind != "mixed" || len(blocks[0].Segments) != 3 {
		t.Fatalf("mixed line was not retained as text/formula/text segments: %#v", blocks)
	}
	if blocks[0].Segments[0].Text != "已知函数" || blocks[0].Segments[1].Latex != region.SelectedLatex || blocks[0].Segments[2].Text != "，则求其最小值" {
		t.Fatalf("unexpected mixed segments: %#v", blocks[0].Segments)
	}
	if blocks[0].RawText == "" || !strings.Contains(merged.Documents[0].Content, `\(f(x)=x^{2}+1\)`) {
		t.Fatalf("source evidence or canonical content missing: %#v %q", blocks[0], merged.Documents[0].Content)
	}
}

func TestValidPaperFormulaResultAllowsUncertainDetectorWithoutModelCall(t *testing.T) {
	region := acceptedFormulaRegion()
	region.DetectorConfidence = .53
	region.CropComplete = true
	region.Candidates = nil
	region.SelectedLatex = ""
	region.SelectedModel = ""
	region.Status = "review_required"
	if !validPaperFormulaResult([]PaperImportParseDocument{{SourceID: "source", DocumentIndex: 0}}, []PaperImportFormulaRegion{region}) {
		t.Fatal("review-only detector result should not require a FormulaNet candidate")
	}
}

func TestValidPaperFormulaResultRejectsUnmatchedSelection(t *testing.T) {
	documents := []PaperImportParseDocument{{SourceID: "source", DocumentIndex: 0}}
	region := acceptedFormulaRegion()
	if !validPaperFormulaResult(documents, []PaperImportFormulaRegion{region}) {
		t.Fatal("valid region was rejected")
	}
	region.SelectedLatex = `x^{3}`
	if validPaperFormulaResult(documents, []PaperImportFormulaRegion{region}) {
		t.Fatal("unmatched selected formula was accepted")
	}
}

func TestValidPaperFormulaResultSupportsVersionedValidation(t *testing.T) {
	documents := []PaperImportParseDocument{{SourceID: "source", DocumentIndex: 0}}
	for _, version := range []string{"latex-structure-render-v1", "latex-structure-render-v2", "unknown"} {
		t.Run(version, func(t *testing.T) {
			region := acceptedFormulaRegion()
			region.ValidationVersion = version
			if got := validPaperFormulaResult(documents, []PaperImportFormulaRegion{region}); got != (version != "unknown") {
				t.Fatalf("validation version %q: got %v", version, got)
			}
		})
	}
}

func TestMergePaperFormulaResultReplacesOverlappingOCRInPlace(t *testing.T) {
	input := PaperImportParseRequest{Documents: []PaperImportParseDocument{
		{
			SourceID: "source", DocumentIndex: 0,
			Blocks: []PaperImportOCRBlock{
				{SourceID: "source", DocumentIndex: 0, PageNo: 1, BlockID: "before", Text: "题目", BBox: []float64{1, 1, 20, 10}},
				{SourceID: "source", DocumentIndex: 0, PageNo: 1, BlockID: "bad-formula", Text: "x2+1", BBox: []float64{12, 20, 70, 18}},
				{SourceID: "source", DocumentIndex: 0, PageNo: 1, BlockID: "after", Text: "答案", BBox: []float64{1, 50, 20, 10}},
			},
		},
	}}
	merged := mergePaperFormulaResult(input, []PaperImportFormulaRegion{acceptedFormulaRegion()})
	blocks := merged.Documents[0].Blocks
	if len(blocks) != 3 || blocks[1].Kind != "formula" || blocks[1].Text != `\(x^{2}+1\)` || strings.Contains(merged.Documents[0].Content, "x2+1") {
		t.Fatalf("unexpected merged blocks/content: %#v %q", blocks, merged.Documents[0].Content)
	}
}

func TestMergePaperFormulaResultUsesGeometryWhenOCRDroppedMathGlyphs(t *testing.T) {
	input := PaperImportParseRequest{Documents: []PaperImportParseDocument{{
		SourceID: "source", DocumentIndex: 0,
		Blocks: []PaperImportOCRBlock{{
			SourceID: "source", DocumentIndex: 0, PageNo: 1, BlockID: "mixed",
			Text: "已知f(x)=21nx+x2-ax，则判断", BBox: []float64{0, 20, 300, 24}, Confidence: .88,
		}},
	}}}
	region := acceptedFormulaRegion()
	region.BBox = []float64{50, 20, 180, 24}
	region.SelectedLatex = `f(x)=2\ln x+x^{2}-ax`
	region.Candidates[0].RawLatex = region.SelectedLatex
	region.Candidates[0].CanonicalLatex = region.SelectedLatex

	merged := mergePaperFormulaResult(input, []PaperImportFormulaRegion{region})
	blocks := merged.Documents[0].Blocks
	if len(blocks) != 1 || blocks[0].Kind != "mixed" || !strings.Contains(blocks[0].Text, `\(f(x)=2\ln x+x^{2}-ax\)`) {
		t.Fatalf("geometry fallback did not create a mixed line: %#v", blocks)
	}
	if blocks[0].Text != `已知\(f(x)=2\ln x+x^{2}-ax\)，则判断` || blocks[0].RawText == "" || len(blocks[0].Segments) != 3 {
		t.Fatalf("bad OCR fragment or provenance was not reconciled: %#v", blocks[0])
	}
	if blocks[0].Confidence > .6 || blocks[0].ReviewStatus != "review_required" || len(merged.ExtraIssues) == 0 {
		t.Fatalf("approximate alignment must remain review-only: %#v", blocks[0])
	}
}

func TestMergeInlineFormulaGeometryDoesNotDeleteChineseProse(t *testing.T) {
	block := PaperImportOCRBlock{Text: "此处正文不可删除", BBox: []float64{0, 20, 100, 20}, Confidence: .9}
	region := acceptedFormulaRegion()
	region.BBox = []float64{10, 20, 80, 20}
	if _, ok := mergeInlineFormulaByGeometry(block, region); ok {
		t.Fatal("geometry fallback must not remove Chinese prose")
	}
}

func TestMergeInlineFormulaDoesNotNestMathDelimiters(t *testing.T) {
	block := PaperImportOCRBlock{Text: `已知\(x^{2}+1\)，求值`, Kind: "mixed", BBox: []float64{0, 20, 100, 20}, Confidence: .9}
	region := acceptedFormulaRegion()
	if _, ok := mergeInlineFormulaSegment(block, region); ok {
		t.Fatal("a second ROI must not wrap an existing formula again")
	}
}

func TestMergeInlineFormulaGeometryDoesNotWrapFormulaBlockAgain(t *testing.T) {
	block := PaperImportOCRBlock{Text: `\(x^{2}+1\)`, Kind: "formula", BBox: []float64{10, 20, 80, 20}, Confidence: .9}
	if _, ok := mergeInlineFormulaByGeometry(block, acceptedFormulaRegion()); ok {
		t.Fatal("geometry fallback must not wrap an existing formula again")
	}
}

func TestFormulaSearchTextNormalizesEscapedSetDelimiters(t *testing.T) {
	if got := formulaSearchText(`A=\{x|-2<x<2.\}`); got != "a=x|-2<x<2." {
		t.Fatalf("escaped delimiters prevented formula matching: %q", got)
	}
}

func TestMergePaperFormulaResultRepairsDroppedSetBuilderBarsWithoutDuplicates(t *testing.T) {
	input := PaperImportParseRequest{Documents: []PaperImportParseDocument{{
		SourceID: "source", DocumentIndex: 0,
		Blocks: []PaperImportOCRBlock{
			{SourceID: "source", DocumentIndex: 0, PageNo: 1, BlockID: "line", Text: "1.已知集合A= {x−2<x <2}，B =", BBox: []float64{77, 191, 367, 30}, Confidence: .81},
			{SourceID: "source", DocumentIndex: 0, PageNo: 1, BlockID: "tail", Text: "≤0}，则", BBox: []float64{511, 181, 104, 52}, Confidence: .71},
		},
	}}}
	setA := acceptedFormulaRegion()
	setA.RegionID = "set-a"
	setA.BBox = []float64{179, 164, 215, 84}
	setA.SelectedLatex = `A=\{x|-2<x<2.\}`
	setA.Candidates[0].RawLatex = setA.SelectedLatex
	setA.Candidates[0].CanonicalLatex = setA.SelectedLatex
	setB := acceptedFormulaRegion()
	setB.RegionID = "set-b"
	setB.BBox = []float64{385, 162, 186, 88}
	setB.SelectedLatex = `B=\{x|\frac{x-3}{x+1}\leq0.\}`
	setB.Candidates[0].RawLatex = setB.SelectedLatex
	setB.Candidates[0].CanonicalLatex = setB.SelectedLatex

	merged := mergePaperFormulaResult(input, []PaperImportFormulaRegion{setA, setB})
	content := merged.Documents[0].Content
	if !strings.Contains(content, `\(A=\{x|-2<x<2.\}\)`) || !strings.Contains(content, `\(B=\{x|\frac{x-3}{x+1}\leq0.\}\)`) {
		t.Fatalf("canonical set formulas were not retained: %q", content)
	}
	if strings.Contains(content, "{x−2<x <2}") || strings.Contains(content, "≤0}") {
		t.Fatalf("broken OCR set fragments were duplicated: %q", content)
	}
}

func TestSelectedFormulaConfidenceFallsBackToRenderEvidence(t *testing.T) {
	region := acceptedFormulaRegion()
	region.DetectorConfidence = .9
	region.Candidates[0].Confidence = 0
	renderSimilarity := .61
	region.Candidates[0].RenderSimilarity = &renderSimilarity
	if got := selectedFormulaConfidence(region); got != renderSimilarity {
		t.Fatalf("expected render-backed confidence, got %v", got)
	}
}
