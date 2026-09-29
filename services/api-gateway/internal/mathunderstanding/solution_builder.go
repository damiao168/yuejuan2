package mathunderstanding

import (
	"fmt"
	"sort"
)

type SolutionBuildInput struct {
	AnswerSegmentID     string
	Blocks              []MathAnswerBlock
	Formulas            []FormulaArtifact
	Relations           []SpatialRelation
	Verifications       []MathVerification
	BuilderVersion      string
	FormulaModelVersion string
	MinimumEdgeScore    float64
}

// BuildSolutionGraph 把达到阈值的空间关系转成步骤连线；发现环时返回错误，不自动删边。
func BuildSolutionGraph(input SolutionBuildInput) (SolutionGraph, error) {
	if input.AnswerSegmentID == "" || input.BuilderVersion == "" || input.FormulaModelVersion == "" {
		return SolutionGraph{}, ErrInvalidInput
	}
	minimum := input.MinimumEdgeScore
	if minimum <= 0 {
		minimum = .5
	}
	formulaByBlock := map[string][]string{}
	for _, formula := range input.Formulas {
		formulaByBlock[formula.BlockID] = append(formulaByBlock[formula.BlockID], formula.ID)
	}
	steps, stepByBlock := SegmentSolutionSteps(input.Blocks, formulaByBlock)
	edgeMap := map[string]SolutionEdge{}
	for _, relation := range input.Relations {
		from, fromOK := stepByBlock[relation.FromID]
		to, toOK := stepByBlock[relation.ToID]
		if !fromOK || !toOK || from == to || relation.Confidence < minimum {
			continue
		}
		kind := "next"
		if relation.Kind == "implies" {
			kind = "derives"
		}
		edgeMap[from+":"+to] = SolutionEdge{FromStepID: from, ToStepID: to, Kind: kind}
	}
	edges := make([]SolutionEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromStepID == edges[j].FromStepID {
			return edges[i].ToStepID < edges[j].ToStepID
		}
		return edges[i].FromStepID < edges[j].FromStepID
	})
	// 整体置信度取最弱步骤，避免其他步骤的高分掩盖一个识别不清的步骤。
	confidence := 1.0
	if len(steps) == 0 {
		confidence = 0
	}
	for _, step := range steps {
		confidence = min(confidence, step.Confidence)
	}
	graph := SolutionGraph{ID: fmt.Sprintf("solution-%s", input.AnswerSegmentID), AnswerSegmentID: input.AnswerSegmentID, BuilderVersion: input.BuilderVersion, FormulaModelVersion: input.FormulaModelVersion, OverallConfidence: confidence, RequiresHumanReview: confidence < .75 || len(steps) == 0, Steps: steps, Edges: edges}
	blocks, formulas := map[string]bool{}, map[string]bool{}
	for _, b := range input.Blocks {
		blocks[b.ID] = true
	}
	for _, f := range input.Formulas {
		formulas[f.ID] = true
	}
	if err := validateGraph(graph, blocks, formulas); err != nil {
		return SolutionGraph{}, err
	}
	return graph, nil
}
