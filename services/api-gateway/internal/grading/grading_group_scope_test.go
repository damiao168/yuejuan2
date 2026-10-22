package grading

import "testing"

func TestSharedOnlyAnswerNeverUsesStaleQuestionAnswerForAutomaticScore(t *testing.T) {
	for _, mode := range []string{"RULE_AUTO", "AI_ASSIST", "DUAL_HUMAN", "HUMAN_PRIMARY"} {
		route := routeScoringSegmentForScope("fill_blank", mode, "grading_group", true, true, 0)
		want := scoringRouteManualReview
		if mode == "RULE_AUTO" {
			want = scoringRouteObserveRule
		}
		// 共区旧正文不能直接判分；只有重新完成严格成员映射后才能使用逐题候选。
		if route.Kind != want {
			t.Fatalf("mode=%s route=%#v", mode, route)
		}
	}
	if route := routeScoringSegmentForScope("fill_blank", "RULE_AUTO", "question", true, true, 0); route.Kind != scoringRouteRuleInput {
		t.Fatal("individual rule path changed")
	}
}
