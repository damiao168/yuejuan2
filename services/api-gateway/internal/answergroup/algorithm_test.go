package answergroup

import (
	"reflect"
	"testing"
)

type chainSimilarityProvider struct{}

func (chainSimilarityProvider) Version() string { return "chain-test" }
func (chainSimilarityProvider) Represent(text string) Representation {
	return Representation{Normalized: text, Hash: text}
}
func (chainSimilarityProvider) Similarity(left, right Representation) float64 {
	if left.Normalized == right.Normalized {
		return 1
	}
	if left.Normalized == "b" || right.Normalized == "b" {
		return 0.9
	}
	return 0.6
}

func TestCompleteLinkRejectsSimilarityChainsAndPreservesTies(t *testing.T) {
	// a-b、b-c 同样相似，但 a-c 未过阈值；输入顺序不能决定先合并哪一对。
	for _, order := range [][]string{{"a", "b", "c"}, {"c", "b", "a"}, {"b", "c", "a"}} {
		answers := make([]SourceAnswer, 0, len(order))
		for _, id := range order {
			answers = append(answers, SourceAnswer{SegmentID: id, AnswerText: id})
		}
		clusters := clusterAnswers(answers, chainSimilarityProvider{}, 0.8)
		var groups [][]string
		for _, cluster := range clusters {
			var ids []string
			for _, answer := range cluster.Answers {
				ids = append(ids, answer.Source.SegmentID)
			}
			groups = append(groups, ids)
		}
		if want := [][]string{{"a", "b"}, {"c"}}; !reflect.DeepEqual(groups, want) {
			t.Fatalf("order %v: groups=%v want=%v", order, groups, want)
		}
	}
}

func TestMaterializeMembersKeepsStableSamplesAndSingletonManualReview(t *testing.T) {
	cluster := groupedAnswers{
		Answers: []representedAnswer{
			{Source: SourceAnswer{SegmentID: "b"}},
			{Source: SourceAnswer{SegmentID: "a"}},
		},
		Similarity: [][]float64{{1, 0.9}, {0.9, 1}},
	}
	members, homogeneity := materializeMembers(cluster, 0.25)
	if homogeneity != 0.9 || !members[1].Representative || !members[1].Boundary || members[0].Representative || members[0].Boundary {
		t.Fatalf("equal averages must choose the smaller segment ID: %#v homogeneity=%v", members, homogeneity)
	}
	cluster.Answers = cluster.Answers[:1]
	cluster.Similarity = [][]float64{{1}}
	members, homogeneity = materializeMembers(cluster, 0.25)
	if homogeneity != 0 || members[0].Similarity != 0 || !members[0].Outlier || members[0].Boundary || !members[0].Representative {
		t.Fatalf("singleton must remain an outlier requiring manual review: %#v homogeneity=%v", members, homogeneity)
	}
}
