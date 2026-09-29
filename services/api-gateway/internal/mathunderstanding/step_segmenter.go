package mathunderstanding

import (
	"math"
	"sort"
	"strings"
)

type stepBlockGroup struct {
	blocks          []MathAnswerBlock
	box             BoundingBox
	attachmentScore float64
	attachments     int
}

// SegmentSolutionSteps 把同一行中靠近的文字和公式合成步骤，保留各自的原始块 ID。
// 划掉的块和连接符不作为步骤；这种几何分组不判断数学正误。
func SegmentSolutionSteps(blocks []MathAnswerBlock, formulaByBlock map[string][]string) ([]SolutionStep, map[string]string) {
	active := make([]MathAnswerBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Status != "crossed_out" && block.Kind != "connector" {
			active = append(active, block)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		leftY := active[i].BoundingBox.Y + active[i].BoundingBox.Height/2
		rightY := active[j].BoundingBox.Y + active[j].BoundingBox.Height/2
		if math.Abs(leftY-rightY) <= .01 {
			return active[i].BoundingBox.X < active[j].BoundingBox.X
		}
		return leftY < rightY
	})

	groups := make([]stepBlockGroup, 0, len(active))
	for _, block := range active {
		bestIndex, bestScore := -1, 0.0
		for index := range groups {
			score := sameLineScore(groups[index].box, block.BoundingBox)
			if score >= .55 && score > bestScore {
				bestIndex, bestScore = index, score
			}
		}
		if bestIndex < 0 {
			groups = append(groups, stepBlockGroup{blocks: []MathAnswerBlock{block}, box: block.BoundingBox})
			continue
		}
		group := &groups[bestIndex]
		group.blocks = append(group.blocks, block)
		group.box = unionBox(group.box, block.BoundingBox)
		group.attachmentScore += bestScore
		group.attachments++
	}

	sort.SliceStable(groups, func(i, j int) bool {
		leftY := groups[i].box.Y + groups[i].box.Height/2
		rightY := groups[j].box.Y + groups[j].box.Height/2
		if math.Abs(leftY-rightY) <= .01 {
			return groups[i].box.X < groups[j].box.X
		}
		return leftY < rightY
	})

	steps := make([]SolutionStep, 0, len(groups))
	stepByBlock := make(map[string]string, len(active))
	for _, group := range groups {
		sort.SliceStable(group.blocks, func(i, j int) bool {
			return group.blocks[i].BoundingBox.X < group.blocks[j].BoundingBox.X
		})
		blockIDs := make([]string, 0, len(group.blocks))
		formulaIDs := []string{}
		parts := []string{}
		recognitionConfidence, structureConfidence := 1.0, 1.0
		for _, block := range group.blocks {
			blockIDs = append(blockIDs, block.ID)
			formulaIDs = append(formulaIDs, formulaByBlock[block.ID]...)
			part := strings.TrimSpace(block.Normalized)
			if part == "" {
				part = strings.TrimSpace(block.Text)
			}
			if part != "" {
				parts = append(parts, part)
			}
			recognitionConfidence = math.Min(recognitionConfidence, block.RecognitionConfidence)
			structureConfidence = math.Min(structureConfidence, block.StructureConfidence)
		}
		if group.attachments > 0 {
			structureConfidence = math.Min(structureConfidence, group.attachmentScore/float64(group.attachments))
		}
		normalized := strings.Join(parts, " ")
		stepID := "step-" + blockIDs[0]
		box := group.box
		step := SolutionStep{
			ID:                    stepID,
			OrderHint:             len(steps) + 1,
			BlockIDs:              blockIDs,
			FormulaIDs:            formulaIDs,
			NormalizedText:        normalized,
			BoundingBox:           &box,
			Kind:                  classifyStepKind(normalized, len(formulaIDs) > 0),
			RecognitionConfidence: clamp(recognitionConfidence),
			StructureConfidence:   clamp(structureConfidence),
			Confidence:            clamp(math.Min(recognitionConfidence, structureConfidence)),
		}
		steps = append(steps, step)
		for _, blockID := range blockIDs {
			stepByBlock[blockID] = stepID
		}
	}
	return steps, stepByBlock
}

func sameLineScore(left, right BoundingBox) float64 {
	vertical := overlap(left.Y, left.Y+left.Height, right.Y, right.Y+right.Height) /
		math.Max(math.Min(left.Height, right.Height), .000001)
	leftBaseline, rightBaseline := left.Y+left.Height, right.Y+right.Height
	baselineDelta := math.Abs(leftBaseline - rightBaseline)
	baseline := clamp(1 - baselineDelta/math.Max(math.Max(left.Height, right.Height), .000001))
	gap := math.Max(0, math.Max(left.X, right.X)-math.Min(left.X+left.Width, right.X+right.Width))
	maxGap := math.Max(.035, math.Min(.12, 1.5*math.Max(left.Height, right.Height)))
	if gap > maxGap || vertical < .25 && baseline < .6 {
		return 0
	}
	return clamp(.7*vertical + .3*baseline - .15*(gap/maxGap))
}

func unionBox(left, right BoundingBox) BoundingBox {
	x, y := math.Min(left.X, right.X), math.Min(left.Y, right.Y)
	rightEdge := math.Max(left.X+left.Width, right.X+right.Width)
	bottom := math.Max(left.Y+left.Height, right.Y+right.Height)
	return BoundingBox{X: x, Y: y, Width: rightEdge - x, Height: bottom - y}
}

func classifyStepKind(normalized string, hasFormula bool) string {
	compact := strings.ToLower(strings.ReplaceAll(normalized, " ", ""))
	if strings.ContainsAny(compact, "若当") || strings.Contains(compact, "case") {
		return "branch"
	}
	if strings.Contains(compact, "所以") || strings.Contains(compact, "因此") || strings.Contains(compact, "故") || strings.HasPrefix(compact, "答") || strings.Contains(compact, "∴") {
		return "conclusion"
	}
	if strings.Contains(compact, "设") || strings.Contains(compact, "令") || strings.Contains(compact, "已知") || strings.HasPrefix(compact, "由") {
		return "setup"
	}
	if hasFormula {
		if strings.ContainsAny(compact, "=<>≤≥") {
			return "transformation"
		}
		return "calculation"
	}
	return "explanation"
}
