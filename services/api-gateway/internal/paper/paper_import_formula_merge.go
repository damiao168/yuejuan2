package paper

// 公式与正文合并只处理输入值；保留原 UTF-8 偏移、正文顺序和人工复核语义。

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func mergePaperFormulaResult(input PaperImportParseRequest, regions []PaperImportFormulaRegion) PaperImportParseRequest {
	for documentIndex := range input.Documents {
		document := &input.Documents[documentIndex]
		input.ExtraIssues = append(input.ExtraIssues, mergePaperFormulaDocument(document, regions)...)
		sortPaperImportOCRBlocks(document.Blocks)
		parts := make([]string, 0, len(document.Blocks))
		for _, block := range document.Blocks {
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
		document.Content = strings.Join(parts, "\n")
	}
	return input
}

func mergePaperFormulaDocument(document *PaperImportParseDocument, regions []PaperImportFormulaRegion) []PaperImportIssue {
	var issues []PaperImportIssue
	for _, region := range regions {
		if region.SourceID != document.SourceID || region.DocumentIndex != document.DocumentIndex {
			continue
		}
		if region.Status != "accepted" || strings.TrimSpace(region.SelectedLatex) == "" {
			issues = append(issues, PaperImportIssue{Code: "FORMULA_REVIEW_REQUIRED", Severity: "warning", Certainty: "confirmed", Message: fmt.Sprintf("第 %d 页有数学公式需要人工核对", region.PageNo), SourceRefs: []PaperImportSourceRef{{SourceID: document.SourceID, FileAssetID: document.FileAssetID, DocumentIndex: document.DocumentIndex, PageNo: region.PageNo, BlockID: region.RegionID, BBox: region.BBox}}, ResolutionHint: "请对照原图核对公式"})
			continue
		}
		blocks, review := mergeAcceptedPaperFormulaRegion(document.Blocks, region)
		if review {
			issues = append(issues, formulaMergeReviewIssue(*document, region))
		}
		document.Blocks = blocks
	}
	return issues
}

type paperFormulaBlockMerge struct {
	kept           []PaperImportOCRBlock
	insertAt       int
	mergedInline   bool
	reviewRequired bool
}

func mergeAcceptedPaperFormulaRegion(blocks []PaperImportOCRBlock, region PaperImportFormulaRegion) ([]PaperImportOCRBlock, bool) {
	formulaBlock := PaperImportOCRBlock{SourceID: region.SourceID, DocumentIndex: region.DocumentIndex, BlockID: region.RegionID, PageNo: region.PageNo, Text: "\\(" + region.SelectedLatex + "\\)", BBox: region.BBox, Confidence: selectedFormulaConfidence(region), Kind: "formula", CanonicalLatex: region.SelectedLatex, Engine: "paddle-formula", EngineVersion: region.SelectedModel, ReviewStatus: "accepted", Segments: []PaperImportContentSegment{{Kind: "formula", Latex: region.SelectedLatex, BBox: region.BBox, SourceBlockID: region.RegionID}}}
	merge := paperFormulaBlockMerge{kept: make([]PaperImportOCRBlock, 0, len(blocks)+1), insertAt: -1}
	for _, block := range blocks {
		merge.appendBlock(block, region)
	}
	if merge.mergedInline {
		return merge.kept, merge.reviewRequired
	}
	if merge.insertAt < 0 {
		merge.insertAt = paperFormulaGeometryInsertAt(merge.kept, region)
	}
	insertAt := max(0, min(merge.insertAt, len(merge.kept)))
	merge.kept = append(merge.kept, PaperImportOCRBlock{})
	copy(merge.kept[insertAt+1:], merge.kept[insertAt:])
	merge.kept[insertAt] = formulaBlock
	return merge.kept, merge.reviewRequired
}

// 一个区域最多内联到一块正文；其余重叠块仍按原顺序保留或删除，插入位置也按该顺序确定。
func (m *paperFormulaBlockMerge) appendBlock(block PaperImportOCRBlock, region PaperImportFormulaRegion) {
	if block.PageNo != region.PageNo || !paperBBoxOverlap(block.BBox, region.BBox) {
		m.kept = append(m.kept, block)
		return
	}
	if paperFormulaReplacementSafe(block, region) {
		if m.insertAt < 0 {
			m.insertAt = len(m.kept)
		}
		return
	}
	if !m.mergedInline {
		if mixed, ok, review := mergeOverlappingPaperFormulaBlock(block, region); ok {
			m.kept = append(m.kept, mixed)
			m.mergedInline, m.reviewRequired = true, review
			return
		}
	}
	if m.insertAt < 0 {
		m.insertAt = len(m.kept) + 1
	}
	m.kept = append(m.kept, block)
}

func mergeOverlappingPaperFormulaBlock(block PaperImportOCRBlock, region PaperImportFormulaRegion) (PaperImportOCRBlock, bool, bool) {
	if mixed, ok := mergeInlineFormulaSegment(block, region); ok {
		return mixed, true, mixed.ReviewStatus == "review_required"
	}
	if mixed, ok := mergeInlineFormulaByGeometry(block, region); ok {
		return mixed, true, true
	}
	return block, false, false
}

// 覆盖比例只是几何筛选；还要结合文本，避免把与公式重叠的中文正文整块替换。
func paperFormulaReplacementSafe(block PaperImportOCRBlock, region PaperImportFormulaRegion) bool {
	box, ok := paperBBox(block.BBox)
	if !ok {
		return false
	}
	ax2, ay2 := box[0]+box[2], box[1]+box[3]
	bx2, by2 := region.BBox[0]+region.BBox[2], region.BBox[1]+region.BBox[3]
	w, h := min(ax2, bx2)-max(box[0], region.BBox[0]), min(ay2, by2)-max(box[1], region.BBox[1])
	coverage := max(0, w) * max(0, h) / (box[2] * box[3])
	if coverage < 0.82 {
		return false
	}
	plainBlock := normalizeFormulaSearchText(block.Text)
	plainFormula := formulaSearchText(region.SelectedLatex)
	if len([]rune(plainFormula)) >= 2 && plainBlock == plainFormula {
		return true
	}
	for _, char := range block.Text {
		if unicode.In(char, unicode.Han) {
			return false
		}
	}
	formulaLike := 0
	total := 0
	for _, char := range plainBlock {
		if unicode.IsSpace(char) {
			continue
		}
		total++
		if unicode.IsDigit(char) || unicode.IsLetter(char) || strings.ContainsRune("+-=*/()[]{}<>.,:|", char) {
			formulaLike++
		}
	}
	return coverage >= 0.94 && total > 0 && float64(formulaLike)/float64(total) >= 0.9
}

func mergeInlineFormulaSegment(block PaperImportOCRBlock, region PaperImportFormulaRegion) (PaperImportOCRBlock, bool) {
	if block.Kind == "formula" {
		return block, false
	}
	plainFormula := formulaSearchText(region.SelectedLatex)
	if len([]rune(plainFormula)) < 3 {
		return block, false
	}
	searchable, starts, ends := normalizeFormulaSearchWithOffsets(block.Text)
	start := strings.Index(searchable, plainFormula)
	if start < 0 {
		sourceStart, sourceEnd, ok := approximateFormulaRange(block.Text, plainFormula)
		if !ok {
			return block, false
		}
		mixed, ok := replaceInlineFormulaRange(block, region, sourceStart, sourceEnd)
		if ok {
			mixed.ReviewStatus = "review_required"
			mixed.Confidence = min(mixed.Confidence, 0.6)
		}
		return mixed, ok
	}
	end := start + len(plainFormula)
	if start >= len(starts) || end <= 0 || end > len(ends) {
		return block, false
	}
	sourceStart, sourceEnd := starts[start], ends[end-1]
	return replaceInlineFormulaRange(block, region, sourceStart, sourceEnd)
}

func replaceInlineFormulaRange(block PaperImportOCRBlock, region PaperImportFormulaRegion, sourceStart, sourceEnd int) (PaperImportOCRBlock, bool) {
	if sourceStart < 0 || sourceEnd <= sourceStart || sourceEnd > len(block.Text) {
		return block, false
	}
	// 绝不能替换已插入公式内部的子串。第二个 ROI 不能创建嵌套数学分隔符或改动之前的证据。
	if strings.Count(block.Text[:sourceStart], "\\(") > strings.Count(block.Text[:sourceStart], "\\)") || strings.Contains(block.Text[sourceStart:sourceEnd], "\\(") || strings.Contains(block.Text[sourceStart:sourceEnd], "\\)") {
		return block, false
	}
	if block.RawText == "" {
		block.RawText = block.Text
	}
	previousRequiresReview := block.ReviewStatus == "review_required"
	block.Text = block.Text[:sourceStart] + "\\(" + region.SelectedLatex + "\\)" + block.Text[sourceEnd:]
	block.Kind = "mixed"
	block.ReviewStatus = "accepted"
	if previousRequiresReview {
		block.ReviewStatus = "review_required"
	}
	block.Confidence = mergedFormulaConfidence(block.Confidence, selectedFormulaConfidence(region))
	block.Segments = inlineFormulaSegments(block.Text, block.BlockID, block.Segments, region)
	return block, true
}

func mergeInlineFormulaByGeometry(block PaperImportOCRBlock, region PaperImportFormulaRegion) (PaperImportOCRBlock, bool) {
	// Paddle 文本 OCR 返回行级框，而公式检测器返回行内 ROI。
	// OCR 已丢失上标或竖线时，文本匹配无法找到公式，因此使用可信水平 ROI 只替换该行的对应部分。
	// 已混合的块不再重复这种近似处理，因为插入的 LaTeX 已不具备原始行的字符几何关系。
	if block.Kind == "mixed" || block.Kind == "formula" || strings.TrimSpace(block.Text) == "" {
		return block, false
	}
	box, ok := paperBBox(block.BBox)
	if !ok || len(region.BBox) != 4 {
		return block, false
	}
	tx, ty, tw, th := box[0], box[1], box[2], box[3]
	fx, fy, fw, fh := region.BBox[0], region.BBox[1], region.BBox[2], region.BBox[3]
	verticalOverlap := min(ty+th, fy+fh) - max(ty, fy)
	if verticalOverlap <= 0 || verticalOverlap/min(th, fh) < 0.55 {
		return block, false
	}
	overlapLeft, overlapRight := max(tx, fx), min(tx+tw, fx+fw)
	if overlapRight <= overlapLeft {
		return block, false
	}
	runes := []rune(block.Text)
	coverage := (overlapRight - overlapLeft) * verticalOverlap / (tw * th)
	if coverage < 0.70 && len(runes) > 12 {
		return block, false
	}
	start := max(0, min(len(runes), int(math.Round((overlapLeft-tx)/tw*float64(len(runes))))))
	end := max(start, min(len(runes), int(math.Round((overlapRight-tx)/tw*float64(len(runes))))))
	if end <= start {
		return block, false
	}
	for _, char := range runes[start:end] {
		if unicode.In(char, unicode.Han) {
			return block, false
		}
	}
	if block.RawText == "" {
		block.RawText = block.Text
	}
	block.Text = string(runes[:start]) + "\\(" + region.SelectedLatex + "\\)" + string(runes[end:])
	block.Kind = "mixed"
	block.ReviewStatus = "review_required"
	block.Confidence = min(mergedFormulaConfidence(block.Confidence, selectedFormulaConfidence(region)), 0.6)
	block.Segments = inlineFormulaSegments(block.Text, block.BlockID, block.Segments, region)
	return block, true
}

func formulaMergeReviewIssue(document PaperImportParseDocument, region PaperImportFormulaRegion) PaperImportIssue {
	return PaperImportIssue{
		Code: "FORMULA_ALIGNMENT_REVIEW_REQUIRED", Severity: "warning", Certainty: "suspected",
		Message:        fmt.Sprintf("第 %d 页有公式与正文的对齐位置需要人工核对", region.PageNo),
		SourceRefs:     []PaperImportSourceRef{{SourceID: document.SourceID, FileAssetID: document.FileAssetID, DocumentIndex: document.DocumentIndex, PageNo: region.PageNo, BlockID: region.RegionID, BBox: region.BBox}},
		ResolutionHint: "公式已作为候选替换 OCR 片段，原文已保留；请核对公式两侧文字及符号是否完整",
	}
}

func mergedFormulaConfidence(textConfidence, formulaConfidence float64) float64 {
	if formulaConfidence <= 0 {
		return 0
	}
	if textConfidence <= 0 {
		return formulaConfidence
	}
	return min(textConfidence, formulaConfidence)
}

func inlineFormulaSegments(text, sourceBlockID string, previous []PaperImportContentSegment, current PaperImportFormulaRegion) []PaperImportContentSegment {
	knownBoxes := map[string][]float64{current.SelectedLatex: current.BBox}
	for _, segment := range previous {
		if segment.Kind == "formula" && segment.Latex != "" && len(segment.BBox) == 4 {
			knownBoxes[segment.Latex] = segment.BBox
		}
	}
	segments := []PaperImportContentSegment{}
	for len(text) > 0 {
		start := strings.Index(text, "\\(")
		if start < 0 {
			if text != "" {
				segments = append(segments, PaperImportContentSegment{Kind: "text", Text: text, SourceBlockID: sourceBlockID})
			}
			break
		}
		if start > 0 {
			segments = append(segments, PaperImportContentSegment{Kind: "text", Text: text[:start], SourceBlockID: sourceBlockID})
		}
		end := strings.Index(text[start+2:], "\\)")
		if end < 0 {
			segments = append(segments, PaperImportContentSegment{Kind: "text", Text: text[start:], SourceBlockID: sourceBlockID})
			break
		}
		latex := text[start+2 : start+2+end]
		segments = append(segments, PaperImportContentSegment{Kind: "formula", Latex: latex, BBox: knownBoxes[latex], SourceBlockID: sourceBlockID})
		text = text[start+2+end+2:]
	}
	return segments
}

// 这里只生成 OCR 片段的查找键，会丢弃部分 LaTeX 结构，不能用来判断数学等价。
func formulaSearchText(value string) string {
	value = strings.NewReplacer(
		`\times`, "*", `\cdot`, "*", `\div`, "/", `\leq`, "<=", `\geq`, ">=", `\neq`, "!=",
		`\left`, "", `\right`, "", `\{`, "", `\}`, "", `\|`, "|", `\.`, ".", "^{", "{", "_{", "{",
	).Replace(value)
	value = formulaCommandPattern.ReplaceAllString(value, "")
	value = strings.NewReplacer("{", "", "}", "", "^", "", "_", "").Replace(value)
	return normalizeFormulaSearchText(value)
}

func approximateFormulaRange(value, formula string) (int, int, bool) {
	searchable, starts, ends := normalizeFormulaSearchRunesWithOffsets(value)
	target := []rune(formula)
	if len(target) < 4 || len(target) > 128 || len(searchable) < 2 || len(searchable) > 1024 {
		return 0, 0, false
	}
	maxEdits := min(6, max(1, int(math.Ceil(float64(len(target))*0.30))))
	minLength := max(2, len(target)-maxEdits)
	maxLength := min(len(searchable), len(target)+maxEdits)
	bestDistance, bestStart, bestEnd := maxEdits+1, -1, -1
	for start := range searchable {
		for length := minLength; length <= maxLength && start+length <= len(searchable); length++ {
			if formulaRangeContainsProse(searchable[start : start+length]) {
				continue
			}
			distance := formulaEditDistance(searchable[start:start+length], target, bestDistance)
			// 距离相同仍保留先找到的范围，避免同一文本出现多个近似片段时改变定位顺序。
			if distance < bestDistance {
				bestDistance, bestStart, bestEnd = distance, start, start+length
			}
		}
	}
	if bestStart < 0 || bestDistance > maxEdits {
		return 0, 0, false
	}
	return starts[bestStart], ends[bestEnd-1], true
}

// 近似匹配不能跨过中文正文或已插入的 LaTeX 分隔符。
func formulaRangeContainsProse(chars []rune) bool {
	for _, char := range chars {
		if unicode.In(char, unicode.Han) || char == '\\' {
			return true
		}
	}
	return false
}

func normalizeFormulaSearchRunesWithOffsets(value string) ([]rune, []int, []int) {
	output := []rune{}
	starts := []int{}
	ends := []int{}
	for sourceStart, char := range value {
		sourceEnd := sourceStart + len(string(char))
		normalized := strings.ToLower(norm.NFKC.String(string(char)))
		normalized = strings.NewReplacer("×", "*", "·", "*", "÷", "/", "−", "-").Replace(normalized)
		for _, normalizedChar := range normalized {
			if unicode.IsSpace(normalizedChar) {
				continue
			}
			output = append(output, normalizedChar)
			starts = append(starts, sourceStart)
			ends = append(ends, sourceEnd)
		}
	}
	return output, starts, ends
}

func formulaEditDistance(left, right []rune, stopAfter int) int {
	previous := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftChar := range left {
		current := make([]int, len(right)+1)
		current[0] = leftIndex + 1
		rowMinimum := current[0]
		for rightIndex, rightChar := range right {
			cost := 0
			if leftChar != rightChar {
				cost = 1
			}
			current[rightIndex+1] = min(
				min(current[rightIndex]+1, previous[rightIndex+1]+1),
				previous[rightIndex]+cost,
			)
			rowMinimum = min(rowMinimum, current[rightIndex+1])
		}
		previous = current
		if rowMinimum > stopAfter && leftIndex >= len(right) {
			return stopAfter + 1
		}
	}
	return previous[len(right)]
}

func normalizeFormulaSearchText(value string) string {
	value = strings.ToLower(norm.NFKC.String(value))
	value = strings.NewReplacer("×", "*", "·", "*", "÷", "/", "−", "-", " ", "", "\t", "", "\n", "").Replace(value)
	return value
}

// strings.Index 返回字节偏移，因此每个归一化后的 UTF-8 字节都映射回原字符的完整边界。
func normalizeFormulaSearchWithOffsets(value string) (string, []int, []int) {
	var builder strings.Builder
	starts := []int{}
	ends := []int{}
	for sourceStart, char := range value {
		sourceEnd := sourceStart + len(string(char))
		normalized := strings.ToLower(norm.NFKC.String(string(char)))
		normalized = strings.NewReplacer("×", "*", "·", "*", "÷", "/", "−", "-").Replace(normalized)
		for _, output := range normalized {
			if unicode.IsSpace(output) {
				continue
			}
			encoded := string(output)
			builder.WriteString(encoded)
			for range len(encoded) {
				starts = append(starts, sourceStart)
				ends = append(ends, sourceEnd)
			}
		}
	}
	return builder.String(), starts, ends
}

func paperFormulaGeometryInsertAt(blocks []PaperImportOCRBlock, region PaperImportFormulaRegion) int {
	for index, block := range blocks {
		if block.PageNo != region.PageNo {
			if block.PageNo > region.PageNo {
				return index
			}
			continue
		}
		box, ok := paperBBox(block.BBox)
		if ok && (box[1] > region.BBox[1] || (box[1] == region.BBox[1] && box[0] > region.BBox[0])) {
			return index
		}
	}
	return len(blocks)
}

var formulaCommandPattern = regexp.MustCompile(`\\[A-Za-z]+`)
