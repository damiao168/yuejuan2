package grading

import (
	"context"
	"database/sql"
	"errors"
)

func gradingAIPlanReadinessCheck(plan GradingAIPlan, hasLargeImageAI bool) *ScoringReadinessCheck {
	if !hasLargeImageAI {
		return nil
	}
	check := &ScoringReadinessCheck{Code: "grading_ai_plan_confirmed", Label: "大题 AI 评分方案", Severity: "blocker", Passed: true, Message: "学校管理员已确认本场大题 AI 评分方案。"}
	if err := gradingAIPlanStartRequirement(plan, true); errors.Is(err, ErrGradingAIModelRequired) {
		check.Code, check.Passed, check.Message = "grading_model_required", false, "请学校管理员至少配置一个本校可用的 AI 阅卷模型"
	} else if errors.Is(err, ErrGradingAIPlanConfirmationRequired) {
		check.Code, check.Passed, check.Message = "grading_ai_plan_confirmation_required", false, "请学校管理员在阅卷前确认本场大题 AI 评分方案"
	} else if err != nil {
		check.Code, check.Passed, check.Message = "grading_ai_threshold_required", false, "请学校管理员手动填写大题主评分差和教师复核允许分差（单位：分）"
	}
	return check
}

// 新启动才校验本场确认和手填分差。原命令重放在调用前返回，不改变已启动运行的冻结方案。
// 段重处理也走同一校验，但只统计目标段，不能因其它大题阻断选择或填空的单独重处理。
func validateGradingAIPlanForStartTx(ctx context.Context, tx *sql.Tx, tenantID, examID, segmentID string) error {
	var hasLargeImageAI bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL JOIN LATERAL(SELECT scoring_policy_snapshot_json FROM exam_question_snapshot WHERE tenant_id=q.tenant_id AND exam_id=q.exam_id AND question_id=q.id ORDER BY snapshot_version DESC,id DESC LIMIT 1) snapshot ON true WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.deleted_at IS NULL AND ($3='' OR seg.id=NULLIF($3,'')::uuid) AND q.question_type IN ('short_answer','calculation','essay','discussion') AND snapshot.scoring_policy_snapshot_json->>'mode' IN ('AI_ASSIST','AI_FAST_CONFIRM'))`, tenantID, examID, segmentID).Scan(&hasLargeImageAI)
	if err != nil || !hasLargeImageAI {
		return err
	}
	detail, err := loadGradingAIPlan(ctx, tx, tenantID, examID)
	if err != nil {
		return err
	}
	return gradingAIPlanStartRequirement(detail.Plan, true)
}
