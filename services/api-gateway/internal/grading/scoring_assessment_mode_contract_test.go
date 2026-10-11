package grading

import (
	"context"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

// 配置保存和冻结必须保留管理员确认的模式；路由直接消费冻结策略，不能再被学科 AI 默认或已发布规则改写。
// 使用内存配置存储与实际评分路由组合验证边界，不代替 PostgreSQL 冻结事务验收。
func TestConfiguredAssessmentModeSurvivesFreezeAndScoringRoute(t *testing.T) {
	cases := []configuredModeRouteCase{
		{"ordinary selected response", "single_choice", "selected_response", assessment.EvidenceSelectedOption, assessment.ScoringRuleAuto, scoringRouteOMR, ""},
		{"ordinary exact text", "fill_blank", "exact_text", assessment.EvidenceExactText, assessment.ScoringRuleAuto, scoringRouteRuleInput, ""},
		{"ordinary numeric expression", "numeric", "numeric_expression", assessment.EvidenceNumericValue, assessment.ScoringRuleAuto, scoringRouteRuleInput, ""},
		{"double selected response", "single_choice", "selected_response", assessment.EvidenceSelectedOption, assessment.ScoringDualHuman, scoringRouteManualReview, "frozen_scoring_policy_requires_human"},
		{"blind double exact text", "fill_blank", "exact_text", assessment.EvidenceExactText, assessment.ScoringDualHuman, scoringRouteManualReview, "frozen_scoring_policy_requires_human"},
		{"explicit objective AI assist", "single_choice", "selected_response", assessment.EvidenceSelectedOption, assessment.ScoringAIAssist, scoringRouteManualReview, "ai_mode_unsupported_question_type"},
		{"explicit objective AI fast confirmation", "fill_blank", "exact_text", assessment.EvidenceExactText, assessment.ScoringAIFastConfirm, scoringRouteManualReview, "ai_mode_unsupported_question_type"},
		{"explicit human primary", "numeric", "numeric_expression", assessment.EvidenceNumericValue, assessment.ScoringHumanPrimary, scoringRouteManualReview, "frozen_scoring_policy_requires_human"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assertConfiguredModeFreezeAndRoute(t, test)
		})
	}
}

type configuredModeRouteCase struct {
	name, questionType, archetype string
	evidence                      assessment.EvidenceType
	mode                          assessment.ScoringMode
	wantKind, wantReason          string
}

func assertConfiguredModeFreezeAndRoute(t *testing.T, test configuredModeRouteCase) {
	t.Helper()
	ctx := context.Background()
	store := assessment.NewMemoryStore()
	profiles, err := store.ListSubjectProfiles(ctx, "tenant", assessment.StageJunior, assessment.SubjectMathematics)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles=(%d,%v)", len(profiles), err)
	}
	if profiles[0].ScoringDefault.Mode != assessment.ScoringAIAssist {
		t.Fatalf("fixture must retain subject AI default, got %q", profiles[0].ScoringDefault.Mode)
	}
	config, err := store.ConfigureQuestion(ctx, "tenant", "exam", "question", assessment.ConfigureQuestionInput{
		SubjectProfileID: profiles[0].ID, ArchetypeCode: test.archetype,
		AllowedEvidenceTypes: []assessment.EvidenceType{test.evidence}, RiskTier: assessment.RiskR2,
		ScoringPolicy: assessment.ScoringPolicy{Mode: test.mode, RequireEvidence: true, HumanReviewBelowConfidence: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.ScoringPolicy.Mode != test.mode {
		t.Fatalf("saved mode=%q want %q", config.ScoringPolicy.Mode, test.mode)
	}
	snapshot, err := store.FreezeQuestionSnapshot(ctx, "tenant", "exam", "question")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ScoringPolicySnapshot.Mode != test.mode {
		t.Fatalf("frozen mode=%q want %q", snapshot.ScoringPolicySnapshot.Mode, test.mode)
	}
	// 规则、答案和选项区域都齐全，人工或不支持的 AI 模式仍不能获得自动路径。
	route := routeScoringSegmentForMode(test.questionType, string(snapshot.ScoringPolicySnapshot.Mode), true, true, 4)
	if route.Kind != test.wantKind || route.Reason != test.wantReason {
		t.Fatalf("route=%#v want kind=%q reason=%q", route, test.wantKind, test.wantReason)
	}
}
