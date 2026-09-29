package mathunderstanding

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

type SpatialGraphOptions struct{ MinimumNextScore float64 }

// BuildSpatialRelations 根据位置、连接符和共用字母产生候选连线。
// 这些分数只是排列线索，不能证明两个步骤在数学上有推导关系。
func BuildSpatialRelations(blocks []MathAnswerBlock, options SpatialGraphOptions) []SpatialRelation {
	minimum := options.MinimumNextScore
	if minimum <= 0 {
		minimum = 0.42
	}
	out := make([]SpatialRelation, 0)
	for i := range blocks {
		for j := range blocks {
			if i == j || blocks[i].Status == "crossed_out" || blocks[j].Status == "crossed_out" {
				continue
			}
			from, to := blocks[i], blocks[j]
			geometry, evidence := transitionGeometry(from.BoundingBox, to.BoundingBox)
			connector := 0.0
			if from.Kind == "connector" || strings.ContainsAny(from.Text, "→↓⇒") {
				connector = 1
				evidence = append(evidence, "connector")
			}
			dependency := sharedSymbolScore(from.Normalized+from.Text, to.Normalized+to.Text)
			final := clamp(.65*geometry + .2*connector + .15*dependency)
			if final < minimum {
				continue
			}
			out = append(out, SpatialRelation{ID: fmt.Sprintf("spatial-%s-%s", from.ID, to.ID), FromID: from.ID, ToID: to.ID, Kind: "continues", GeometryScore: geometry, ConnectorScore: connector, MathDependencyScore: dependency, Confidence: final, Evidence: evidence})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromID == out[j].FromID {
			if out[i].Confidence == out[j].Confidence {
				return out[i].ToID < out[j].ToID
			}
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].FromID < out[j].FromID
	})
	return out
}

// transitionGeometry 按输入图像的 0–1 坐标计算；向上或同行向左的候选不给几何分。
func transitionGeometry(from, to BoundingBox) (float64, []string) {
	fromX, fromY := from.X+from.Width/2, from.Y+from.Height/2
	toX, toY := to.X+to.Width/2, to.Y+to.Height/2
	dx, dy := toX-fromX, toY-fromY
	if dy < -.02 || (math.Abs(dy) <= .02 && dx <= 0) {
		return 0, nil
	}
	xOverlap := overlap(from.X, from.X+from.Width, to.X, to.X+to.Width) / math.Max(math.Min(from.Width, to.Width), .000001)
	yOverlap := overlap(from.Y, from.Y+from.Height, to.Y, to.Y+to.Height) / math.Max(math.Min(from.Height, to.Height), .000001)
	evidence := []string{}
	score := 0.0
	if dy > .01 && xOverlap > .25 {
		score = .82 - math.Min(dy, .5)*.35
		evidence = append(evidence, "same_column")
	}
	if math.Abs(dy) <= .08 && dx > 0 && yOverlap > .3 {
		horizontal := .72 - math.Min(dx, .6)*.25
		if horizontal > score {
			score = horizontal
		}
		evidence = append(evidence, "same_row")
	}
	if dy > .08 && math.Abs(dx) < .45 && xOverlap == 0 {
		diagonal := .62 - .2*math.Abs(dx) - .15*dy
		if diagonal > score {
			score = diagonal
		}
		evidence = append(evidence, "converging_candidate")
	}
	if math.Abs(from.X-to.X) < .04 {
		score += .12
		evidence = append(evidence, "left_aligned")
	}
	return clamp(score), evidence
}

func sharedSymbolScore(left, right string) float64 {
	a, b := map[rune]bool{}, map[rune]bool{}
	for _, r := range left {
		if unicode.IsLetter(r) {
			a[unicode.ToLower(r)] = true
		}
	}
	for _, r := range right {
		if unicode.IsLetter(r) {
			b[unicode.ToLower(r)] = true
		}
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for r := range a {
		if b[r] {
			shared++
		}
	}
	return float64(shared) / float64(len(a))
}

func overlap(a1, a2, b1, b2 float64) float64 { return math.Max(0, math.Min(a2, b2)-math.Max(a1, b1)) }
func clamp(value float64) float64            { return math.Max(0, math.Min(1, value)) }
