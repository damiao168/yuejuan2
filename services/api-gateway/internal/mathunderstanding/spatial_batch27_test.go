package mathunderstanding

import (
	"reflect"
	"testing"
)

func TestBatch27SpatialRelationsKeepEqualConfidenceTargetOrdering(t *testing.T) {
	blocks := []MathAnswerBlock{block("a", .1, .1), block("z", .1, .3), block("b", .1, .3)}
	blocks[0].Kind = "connector"
	relations := BuildSpatialRelations(blocks, SpatialGraphOptions{MinimumNextScore: .1})
	var targets []string
	var confidence float64
	for _, relation := range relations {
		if relation.FromID == "a" {
			targets = append(targets, relation.ToID)
			if confidence != 0 && relation.Confidence != confidence {
				t.Fatalf("fixture did not produce equal confidence: %#v", relation)
			}
			confidence = relation.Confidence
		}
	}
	if !reflect.DeepEqual(targets, []string{"b", "z"}) {
		t.Fatalf("equal confidence must use target ID rather than input order: %v", targets)
	}
}
