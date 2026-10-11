package mathunderstanding

import (
	"reflect"
	"testing"
)

func TestBatch26SolutionEdgesKeepThresholdDuplicateAndCycleRules(t *testing.T) {
	input := SolutionBuildInput{
		AnswerSegmentID: "batch26", BuilderVersion: "builder-26", FormulaModelVersion: "formula-26",
		Blocks: []MathAnswerBlock{block("a", .1, .1), block("b", .1, .3), block("c", .1, .5)},
		Relations: []SpatialRelation{
			{FromID: "a", ToID: "b", Kind: "continues", Confidence: .5},
			{FromID: "a", ToID: "b", Kind: "implies", Confidence: .5},
			{FromID: "b", ToID: "c", Confidence: .49},
			{FromID: "c", ToID: "c", Confidence: 1},
			{FromID: "unknown", ToID: "a", Confidence: 1},
			{FromID: "a", ToID: "c", Confidence: .6},
		},
	}
	graph, err := BuildSolutionGraph(input)
	if err != nil || len(graph.Steps) != 3 || len(graph.Edges) != 2 {
		t.Fatalf("graph=%#v err=%v", graph, err)
	}
	want := []SolutionEdge{
		{FromStepID: graph.Steps[0].ID, ToStepID: graph.Steps[1].ID, Kind: "derives"},
		{FromStepID: graph.Steps[0].ID, ToStepID: graph.Steps[2].ID, Kind: "next"},
	}
	if !reflect.DeepEqual(graph.Edges, want) || graph.BuilderVersion != input.BuilderVersion || graph.FormulaModelVersion != input.FormulaModelVersion {
		t.Fatalf("edge order, duplicate precedence or versions changed: %#v", graph)
	}
	// 达标的回边必须交给原环检测拒绝，不能为生成图而删掉有效关系。
	input.Relations = append(input.Relations, SpatialRelation{FromID: "b", ToID: "a", Confidence: .5})
	if _, err := BuildSolutionGraph(input); err == nil {
		t.Fatal("cycle was silently removed")
	}
}

func TestBatch26SpatialThresholdAndCrossedOutFiltering(t *testing.T) {
	blocks := []MathAnswerBlock{block("a", .1, .1), block("b", .1, .3), block("discarded", .1, .5)}
	blocks[0].Kind = "connector"
	blocks[2].Status = "crossed_out"
	relations := BuildSpatialRelations(blocks, SpatialGraphOptions{MinimumNextScore: .1})
	var forward SpatialRelation
	for _, relation := range relations {
		if relation.FromID == "discarded" || relation.ToID == "discarded" || relation.FromID == relation.ToID {
			t.Fatalf("excluded pair admitted: %#v", relation)
		}
		if relation.FromID == "a" && relation.ToID == "b" {
			forward = relation
		}
	}
	if forward.ID == "" || forward.ConnectorScore != 1 || forward.MathDependencyScore != 1 || forward.Kind != "continues" {
		t.Fatalf("candidate facts changed: %#v", forward)
	}
	boundary := BuildSpatialRelations(blocks, SpatialGraphOptions{MinimumNextScore: forward.Confidence})
	if len(boundary) != 1 || !reflect.DeepEqual(boundary[0], forward) {
		t.Fatalf("exact threshold changed: %#v", boundary)
	}
	if got := BuildSpatialRelations(blocks, SpatialGraphOptions{MinimumNextScore: forward.Confidence + .000001}); len(got) != 0 {
		t.Fatalf("below-threshold pair admitted: %#v", got)
	}
}
