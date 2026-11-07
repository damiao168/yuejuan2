package scorerelease

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
	"edugrade-enterprise/services/api-gateway/internal/score/autoprep"
	"errors"
	"fmt"
	"strings"
)

// Gate 是发布前的只读门禁。已注入成绩准备器且自动准备前提成立时，按“创建草稿自动准备之后”的状态判断，
// 不报告准备能消除的阻断（见 gate_preparation.go）。发布事务内的 gateTx 始终使用严格版本。
func (s *PostgresStore) Gate(ctx context.Context, tenantID, examID string) (Gate, error) {
	dashboardIssues, err := s.dashboardIssues(ctx, tenantID, examID)
	if err != nil {
		return Gate{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Gate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	prepared := false
	if s.gradePreparer != nil {
		// 未注入准备器时创建草稿不会自动准备，门禁保持严格。
		state, err := autoprep.ReadState(ctx, tx, tenantID, examID)
		if err != nil {
			return Gate{}, err
		}
		prepared = state.Automatic()
	}
	gate, err := s.gateTx(ctx, tx, tenantID, examID, "", dashboardIssues, prepared)
	if err != nil {
		return Gate{}, err
	}
	if err := tx.Commit(); err != nil {
		return Gate{}, err
	}
	return gate, nil
}

// gateTx 的 prepared 只能由只读 Gate() 在自动准备前提成立时传 true；发布事务内必须传 false。
func (s *PostgresStore) gateTx(ctx context.Context, tx *sql.Tx, tenantID, examID, releaseID string, dashboard []GateIssue, prepared bool) (Gate, error) {
	gate := emptyGate(s.now().UTC())
	// 题目覆盖、终评缺失和总分核对只统计准备时冻结的题目；后来新增的题目不参与这些检查。
	// 已取消任务保留历史但不再参与发布阻断；有效阅卷和仲裁仍跨批次统计，不能只取最新评分批次。
	// 冻结名册无到卷者自动缺考；只检查实际收到的未知、重复、坏页或未完成评分的答卷，不读取旧人工出勤。
	checks := []struct{ code, message, route, query string }{
		{"capture_collection_incomplete", "finish collecting answer sheets and verify absentees before publishing", "capture", autoprep.CollectionIncompleteQuery},
		{"unfinished_review_tasks", "there are unfinished review tasks", "review", `SELECT COUNT(*) FROM review_task WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL AND status IN ('pending', 'assigned', 'in_progress', 'returned')`},
		{"unfinished_arbitration_tasks", "there are unresolved arbitration tasks", "arbitration", `SELECT COUNT(*) FROM arbitration_task WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL AND status IN ('pending', 'assigned')`},
		{"ocr_failed_unhandled", "there are failed OCR tasks", "processing", `SELECT COUNT(*) FROM ocr_task ot JOIN submission sub ON sub.tenant_id = ot.tenant_id AND sub.id = ot.submission_id AND sub.deleted_at IS NULL WHERE ot.tenant_id = $1 AND sub.exam_id = $2::uuid AND ot.deleted_at IS NULL AND ot.status = 'failed'`},
		{"missing_final_grades", "there are answer segments without final grades", "review", gateMissingFinalGradesQuery(prepared)},
		{"unidentified_submission", "submissions are not uniquely matched to an expected student", "capture", `WITH roster AS (SELECT student_id AS id FROM exam_candidate_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid), counts AS (SELECT student_id,COUNT(*) AS count FROM submission WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL GROUP BY student_id) SELECT COUNT(*) FROM submission sub LEFT JOIN counts c ON c.student_id=sub.student_id WHERE sub.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.deleted_at IS NULL AND (sub.student_id IS NULL OR sub.identity_status IN ('unknown','conflict') OR NOT EXISTS (SELECT 1 FROM roster r WHERE r.id=sub.student_id) OR COALESCE(c.count,0)>1)`},
		{"missing_pages_unresolved", "matched submissions have unresolved missing or rejected pages", "capture", `SELECT COUNT(*) FROM submission sub WHERE sub.tenant_id = $1 AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL AND (sub.actual_page_count < sub.expected_page_count OR sub.quality_status = 'failed' OR sub.status = 'rejected')`},
		{"no_submission_grades", "there are no submission grades to release", "grades", gateNoSubmissionGradesQuery(prepared)},
		{"grades_not_confirmed", "there are grades not confirmed for release", "grades", gateNotConfirmedQuery(prepared)},
		{"question_coverage_incomplete", "a submission does not cover the exact official question set or maximum scores", "grades", gateQuestionCoverageQuery(prepared)},
		{"score_integrity_mismatch", "submission total does not match final-grade total", "grades", gateScoreIntegrityQuery(prepared)},
	}
	for _, check := range checks {
		if check.query == "" {
			continue
		}
		count, err := scalar(ctx, tx, check.query, tenantID, examID)
		if err != nil {
			return Gate{}, err
		}
		if count > 0 {
			addIssue(&gate, GateIssue{Code: check.code, Message: check.message, Blocking: true, Count: count, ActionRoute: check.route})
		}
	}
	if releaseID != "" {
		checks := []struct{ code, message, query string }{
			{"release_snapshot_incomplete", "release contains an item without question facts", `SELECT COUNT(*) FROM score_release_item item WHERE item.tenant_id = $1 AND item.release_id = $2::uuid AND NOT EXISTS (SELECT 1 FROM score_release_question question WHERE question.tenant_id = item.tenant_id AND question.release_id = item.release_id AND question.submission_id = item.submission_id)`},
			{"release_snapshot_incomplete", "release snapshot does not reconcile to its question scores", `SELECT COUNT(*) FROM score_release_item item LEFT JOIN LATERAL (SELECT COALESCE(SUM(question.score), 0)::float8 AS total FROM score_release_question question WHERE question.tenant_id = item.tenant_id AND question.release_id = item.release_id AND question.submission_id = item.submission_id) questions ON true WHERE item.tenant_id = $1 AND item.release_id = $2::uuid AND abs(item.total_score::float8 - questions.total) > 0.000001`},
		}
		for _, check := range checks {
			count, err := scalar(ctx, tx, check.query, tenantID, releaseID)
			if err != nil {
				return Gate{}, err
			}
			if count > 0 {
				addIssue(&gate, GateIssue{Code: check.code, Message: check.message, Blocking: true, Count: count, ActionRoute: "grades"})
			}
		}
	}
	for _, issue := range dashboard {
		addIssue(&gate, issue)
	}
	gate.Passed = len(gate.Blocking) == 0
	return gate, nil
}

func (s *PostgresStore) dashboardIssues(ctx context.Context, tenantID, examID string) ([]GateIssue, error) {
	if s.dashboard == nil {
		return nil, nil
	}
	dashboard, err := s.dashboard.Get(ctx, tenantID, examID)
	// 尚未配置题目的考试没有质量看板；这是发布阻断项，不能让正常的空数据变成接口 500。
	if errors.Is(err, qualitydashboard.ErrNotFound) {
		return []GateIssue{{Code: "quality_questions_missing", Message: "考试尚未配置题目，请先完成小题与分值配置。", Blocking: true, Count: 1, ActionRoute: "questions"}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("release quality dashboard: %w", err)
	}
	issues := make([]GateIssue, 0, len(dashboard.Blocking)+len(dashboard.Warnings))
	riskByQuestion := make(map[string]string, len(dashboard.Questions))
	for _, question := range dashboard.Questions {
		riskByQuestion[question.Question.ID] = question.Question.RiskTier
	}
	for _, finding := range dashboard.Blocking {
		issues = append(issues, GateIssue{Code: finding.Code, Message: finding.Reason, Blocking: true, Count: 1, ActionRoute: "quality"})
	}
	for _, finding := range dashboard.Warnings {
		// 未完成的回评明确阻断发布。其他警告在常规低风险考试中仍保持可见。
		// R3 对 Gold、校准和 Seed 证据链采取保守拒绝；此处有警告并不表示质量状态可发布。
		blocking := finding.Code == "backmark_pending" || (riskByQuestion[finding.QuestionID] == "R3" && r3EvidenceRequired(finding.Code))
		issues = append(issues, GateIssue{Code: finding.Code, Message: finding.Reason, Blocking: blocking, Count: 1, ActionRoute: "quality"})
	}
	return issues, nil
}

func r3EvidenceRequired(code string) bool {
	return strings.HasPrefix(code, "gold_") || strings.HasPrefix(code, "calibration_") || strings.HasPrefix(code, "seed_") || code == "answer_group_sampling_pending"
}

func scalar(ctx context.Context, tx *sql.Tx, query, tenantID, id string) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, query, tenantID, id).Scan(&count)
	return count, err
}
