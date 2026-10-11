package grading

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// TestScoringCapabilityMatrixSurvivesConfigurationAndFreeze 锁定前端能力提示所依据的 11 题型 × 6 模式。
// 路由输入按规则、答案和选项区域均就绪核对；内存保存与冻结接真实路由，不代替 PostgreSQL 事务或外部 AI 准入验收。
func TestScoringCapabilityMatrixSurvivesConfigurationAndFreeze(t *testing.T) {
	modes := [6]assessment.ScoringMode{
		assessment.ScoringRuleAuto, assessment.ScoringAIAssist, assessment.ScoringAIFastConfirm,
		assessment.ScoringHumanPrimary, assessment.ScoringDualHuman, assessment.ScoringManualOnly,
	}
	human := scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: "frozen_scoring_policy_requires_human"}
	unsupportedAI := scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: "ai_mode_unsupported_question_type"}
	aiConfirmation := scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceRuleReview, Reason: "ai_assist_teacher_confirmation"}
	unsupportedRule := scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceRuleReview, Reason: "rule_review_required"}
	manualType := scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: "manual_only_question_type"}
	omr := [6]scoringSegmentRoute{{Kind: scoringRouteOMR}, unsupportedAI, unsupportedAI, human, human, human}
	textRule := [6]scoringSegmentRoute{{Kind: scoringRouteRuleInput}, unsupportedAI, unsupportedAI, human, human, human}
	subjective := [6]scoringSegmentRoute{unsupportedRule, aiConfirmation, aiConfirmation, human, human, human}
	manual := [6]scoringSegmentRoute{manualType, unsupportedAI, unsupportedAI, human, human, human}
	cases := []scoringMatrixCase{
		{"single_choice", "selected_response", assessment.EvidenceSelectedOption, omr},
		{"multiple_choice", "selected_response", assessment.EvidenceSelectedOption, omr},
		{"true_false", "selected_response", assessment.EvidenceSelectedOption, omr},
		{"fill_blank", "exact_text", assessment.EvidenceExactText, textRule},
		{"numeric", "numeric_expression", assessment.EvidenceNumericValue, textRule},
		{"short_answer", "short_constructed", assessment.EvidenceTextSpan, subjective},
		{"calculation", "structured_steps", assessment.EvidenceTextSpan, subjective},
		{"essay", "extended_response", assessment.EvidenceTextSpan, subjective},
		{"discussion", "extended_response", assessment.EvidenceTextSpan, subjective},
		{"formula", "numeric_expression", assessment.EvidenceMathExpression, manual},
		{"coding", "short_constructed", assessment.EvidenceTextSpan, manual},
	}
	for _, item := range cases {
		for i, mode := range modes {
			t.Run(item.questionType+"/"+string(mode), func(t *testing.T) {
				assertScoringMatrixModeFreeze(t, item, modes, i, mode)
			})
		}
	}
}

// TestScoringCapabilityDeclarationsMatchRuleEngine 让能力清单变化触发前端矩阵的复核，并核对实际规则引擎。
// 主观适配器的四题型与教师确认边界另由 subjective/scoring_coverage_test.go 和 ApplyReviewPolicy 的测试核实。
func TestScoringCapabilityDeclarationsMatchRuleEngine(t *testing.T) {
	groups := []scoringCapabilityGroup{
		{"rule", paper.RuleGradedQuestionTypes(), []string{"fill_blank", "multiple_choice", "numeric", "single_choice", "true_false"}},
		{"ai", paper.AIAssistedQuestionTypes(), []string{"calculation", "discussion", "essay", "short_answer"}},
		{"manual", paper.ManualOnlyQuestionTypes(), []string{"coding", "formula"}},
	}
	engine := NewEngine()
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			assertDeclaredScoringCapabilityGroup(t, engine, group)
		})
	}
}

func TestUnknownScoringCapabilitiesStayManual(t *testing.T) {
	for _, questionType := range []string{"", "future_question_type"} {
		for _, mode := range []string{"RULE_AUTO", "AI_ASSIST", "AI_FAST_CONFIRM", "HUMAN_PRIMARY", "DUAL_HUMAN", "MANUAL_ONLY", "", "FUTURE_MODE"} {
			t.Run(questionType+"/"+mode, func(t *testing.T) {
				// 未知题型不能从完备配置取得自动路径；未知模式也不能按默认策略悄悄执行。
				if got := routeScoringSegmentForMode(questionType, mode, true, true, 4); got.Kind != scoringRouteManualReview || got.Source != reviewSourceHumanGraded || got.Reason == "" {
					t.Fatalf("unknown capabilities must remain actionable manual work: %#v", got)
				}
			})
		}
	}
}

type scoringMatrixCase struct {
	questionType, archetype string
	evidence                assessment.EvidenceType
	routes                  [6]scoringSegmentRoute
}
type scoringCapabilityGroup struct {
	name      string
	got, want []string
}

func assertScoringMatrixModeFreeze(t *testing.T, item scoringMatrixCase, modes [6]assessment.ScoringMode, i int, mode assessment.ScoringMode) {
	t.Helper()
	if !paper.IsValidQuestionType(item.questionType) {
		t.Fatalf("configured question type must remain valid: %s", item.questionType)
	}
	ctx := context.Background()
	store := assessment.NewMemoryStore()
	profiles, err := store.ListSubjectProfiles(ctx, "tenant", assessment.StageJunior, assessment.SubjectMathematics)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("subject profiles=(%d,%v)", len(profiles), err)
	}
	if profiles[0].ScoringDefault.Mode != assessment.ScoringAIAssist {
		t.Fatalf("fixture must retain the differing subject default: %q", profiles[0].ScoringDefault.Mode)
	}
	input := assessment.ConfigureQuestionInput{
		SubjectProfileID: profiles[0].ID, ArchetypeCode: item.archetype,
		AllowedEvidenceTypes: []assessment.EvidenceType{item.evidence}, RiskTier: assessment.RiskR2,
		ScoringPolicy: assessment.ScoringPolicy{Mode: mode, RequireEvidence: true, HumanReviewBelowConfidence: true},
	}
	config, err := store.ConfigureQuestion(ctx, "tenant", "exam", "question", input)
	if err != nil || config.ScoringPolicy.Mode != mode {
		t.Fatalf("saved mode=%q want=%q err=%v", config.ScoringPolicy.Mode, mode, err)
	}
	snapshot, err := store.FreezeQuestionSnapshot(ctx, "tenant", "exam", "question")
	if err != nil || snapshot.ScoringPolicySnapshot.Mode != mode {
		t.Fatalf("frozen mode=%q want=%q err=%v", snapshot.ScoringPolicySnapshot.Mode, mode, err)
	}
	if got := routeScoringSegmentForMode(item.questionType, string(snapshot.ScoringPolicySnapshot.Mode), true, true, 4); got != item.routes[i] {
		t.Fatalf("route=%#v want=%#v", got, item.routes[i])
	}
	// 冻结后再改成另一模式必须被拒绝；重复冻结仍返回原模式，避免提示与历史策略漂移。
	input.ExpectedRevision = config.Revision
	input.ScoringPolicy.Mode = modes[(i+1)%len(modes)]
	if _, err = store.ConfigureQuestion(ctx, "tenant", "exam", "question", input); !errors.Is(err, assessment.ErrExamFrozen) {
		t.Fatalf("frozen mode must reject replacement: %v", err)
	}
	repeated, err := store.FreezeQuestionSnapshot(ctx, "tenant", "exam", "question")
	if err != nil || repeated.ID != snapshot.ID || repeated.ScoringPolicySnapshot.Mode != mode || repeated.ContentHash != snapshot.ContentHash {
		t.Fatalf("repeat freeze changed the accepted policy: %#v err=%v", repeated, err)
	}
}

func assertDeclaredScoringCapabilityGroup(t *testing.T, engine *Engine, group scoringCapabilityGroup) {
	t.Helper()
	if !reflect.DeepEqual(group.got, group.want) {
		t.Fatalf("capability changed; review assessmentScoringDefaults matrix: got=%v want=%v", group.got, group.want)
	}
	for _, questionType := range group.want {
		_, err := engine.Grade(Context{
			SegmentID: "segment",
			Question:  paper.Question{ID: "question", QuestionNo: "Q1", QuestionType: questionType, Score: 5},
			AnswerKey: paper.AnswerKey{ID: "key", QuestionID: "question", StandardAnswer: "A"},
			Answer:    SegmentAnswer{AnswerSegmentID: "segment", AnswerText: "A", Source: "manual_entry"},
		})
		if group.name == "rule" && err != nil {
			t.Fatalf("rule engine rejected declared type %q: %v", questionType, err)
		}
		if group.name != "rule" && !errors.Is(err, ErrUnsupportedQuestionType) {
			t.Fatalf("rule engine gained support for %q; review manual guidance: %v", questionType, err)
		}
	}
}
