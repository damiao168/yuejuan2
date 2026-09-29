package grading

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrScoringNotReady = errors.New("exam is not ready for scoring")

type ScoringReadinessError struct {
	Readiness ScoringReadiness
}

func (e *ScoringReadinessError) Error() string {
	return "exam is not ready for scoring"
}

func (e *ScoringReadinessError) Unwrap() error {
	return ErrScoringNotReady
}

type scoringReadinessQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type scoringReadinessCounts struct {
	questions         int
	snapshots         int
	segments          int
	processable       int
	missingMetadata   int
	automatic         int
	missingAutomation int
	dualQuestions     int
	eligibleGraders   int
}

func (s *PostgresStore) GetScoringReadiness(ctx context.Context, tenantID, examID string) (ScoringReadiness, error) {
	return calculateScoringReadiness(ctx, s.db, tenantID, examID)
}

func calculateScoringReadiness(ctx context.Context, q scoringReadinessQueryer, tenantID, examID string) (ScoringReadiness, error) {
	var status string
	err := q.QueryRowContext(ctx, `
SELECT status
FROM exam
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, examID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ScoringReadiness{}, ErrNotFound
	}
	if err != nil {
		return ScoringReadiness{}, fmt.Errorf("load scoring readiness exam: %w", err)
	}

	var activeRun *ScoringRun
	run, runErr := scanScoringRun(q.QueryRowContext(ctx, scoringRunSelect+`
 WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL
   AND status IN ('queued','processing','needs_review','failed','cancelling')
 ORDER BY created_at DESC
 LIMIT 1
`, tenantID, examID))
	if runErr == nil {
		activeRun = &run
	} else if !errors.Is(runErr, ErrNotFound) {
		return ScoringReadiness{}, fmt.Errorf("load active scoring run: %w", runErr)
	}

	// 自动候选数只估计配置与输入是否齐备；实际自动确认还要经过识别结果和服务端授权门槛。
	var counts scoringReadinessCounts
	err = q.QueryRowContext(ctx, `
WITH scoped_segments AS (
  SELECT
    seg.id,
    seg.processing_status,
    seg.crop_file_asset_id,
    seg.template_id,
    seg.template_content_hash,
    q.question_type,
    COALESCE(eqs.scoring_policy_snapshot_json->>'mode','') AS scoring_mode,
    q.answer_area,
    EXISTS (
      SELECT 1 FROM file_asset fa
      WHERE fa.tenant_id=seg.tenant_id AND fa.id=seg.crop_file_asset_id AND fa.deleted_at IS NULL
    ) AS crop_exists,
    EXISTS (
      SELECT 1 FROM scoring_rule sr
      WHERE sr.tenant_id=q.tenant_id AND sr.question_id=q.id
        AND sr.status='published' AND sr.deleted_at IS NULL
    ) AS has_rule,
    EXISTS (
      SELECT 1 FROM answer_segment_answer ans
      WHERE ans.tenant_id=seg.tenant_id AND ans.answer_segment_id=seg.id AND ans.deleted_at IS NULL
    ) AS has_answer,
    CASE
      WHEN jsonb_typeof(q.answer_area->'option_regions')='array'
      THEN jsonb_array_length(q.answer_area->'option_regions')
      ELSE 0
    END AS option_count
  FROM answer_segment seg
  JOIN submission sub
    ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
  JOIN question q
    ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
  LEFT JOIN exam_question_snapshot eqs
    ON eqs.tenant_id=q.tenant_id AND eqs.exam_id=q.exam_id AND eqs.question_id=q.id
  WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.deleted_at IS NULL
),
segment_facts AS (
  SELECT *,
    processing_status='completed' AND crop_file_asset_id IS NOT NULL AND crop_exists AS processable,
    template_id IS NULL OR COALESCE(template_content_hash,'')='' OR answer_area IS NULL AS missing_metadata
  FROM scoped_segments
)
SELECT
  (SELECT count(*)::int FROM question
   WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL),
  (SELECT count(*)::int FROM exam_question_snapshot
   WHERE tenant_id=$1::uuid AND exam_id=$2::uuid),
  count(*)::int,
  count(*) FILTER (WHERE processable)::int,
  count(*) FILTER (WHERE processable AND missing_metadata)::int,
  count(*) FILTER (
    WHERE processable AND NOT missing_metadata AND scoring_mode='RULE_AUTO' AND has_rule AND (
      (question_type IN ('single_choice','multiple_choice','true_false') AND option_count >= 2)
      OR (question_type IN ('fill_blank','numeric') AND has_answer)
    )
  )::int,
  count(*) FILTER (
    WHERE processable AND scoring_mode='RULE_AUTO' AND (
      (question_type IN ('single_choice','multiple_choice','true_false') AND (NOT has_rule OR option_count < 2))
      OR (question_type IN ('fill_blank','numeric') AND (NOT has_rule OR NOT has_answer))
    )
  )::int
FROM segment_facts
`, tenantID, examID).Scan(
		&counts.questions,
		&counts.snapshots,
		&counts.segments,
		&counts.processable,
		&counts.missingMetadata,
		&counts.automatic,
		&counts.missingAutomation,
	)
	if err != nil {
		return ScoringReadiness{}, fmt.Errorf("calculate scoring readiness: %w", err)
	}
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM exam_question_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND scoring_policy_snapshot_json->>'mode'='DUAL_HUMAN'`, tenantID, examID).Scan(&counts.dualQuestions); err != nil {
		return ScoringReadiness{}, fmt.Errorf("count dual-mark questions: %w", err)
	}
	if counts.dualQuestions > 0 {
		// 这里只统计同校在岗阅卷员；启动事务还会重新读取名册并分配两份任务。
		if err := q.QueryRowContext(ctx, `SELECT count(DISTINCT u.id) FROM app_user u
JOIN exam e ON e.tenant_id=u.tenant_id AND e.school_id=u.school_id
JOIN user_role ur ON ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL
JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.code='grader' AND r.deleted_at IS NULL
WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND u.status='active' AND u.deleted_at IS NULL`, tenantID, examID).Scan(&counts.eligibleGraders); err != nil {
			return ScoringReadiness{}, fmt.Errorf("count school graders: %w", err)
		}
	}
	return buildScoringReadiness(status, activeRun, counts), nil
}

func buildScoringReadiness(status string, activeRun *ScoringRun, counts scoringReadinessCounts) ScoringReadiness {
	eligibleStatus := status == "collecting" || status == "processing" || status == "grading"
	incomplete := counts.segments - counts.processable
	readySegments := counts.processable - counts.missingMetadata
	if readySegments < 0 {
		readySegments = 0
	}
	manual := readySegments - counts.automatic
	if manual < 0 {
		manual = 0
	}
	noActiveRun := activeRun == nil
	checks := []ScoringReadinessCheck{
		{
			Code:     "exam_status",
			Label:    "考试状态",
			Passed:   eligibleStatus,
			Severity: "blocker",
			Message: map[bool]string{
				true:  "考试状态允许启动阅卷。",
				false: "考试必须处于采集中、处理中或阅卷中。",
			}[eligibleStatus],
		},
		{
			Code:     "questions_configured",
			Label:    "题目配置",
			Passed:   counts.questions > 0,
			Severity: "blocker",
			Message: map[bool]string{
				true:  fmt.Sprintf("已配置 %d 道题。", counts.questions),
				false: "尚未配置题目，不能启动阅卷。",
			}[counts.questions > 0],
			Count: counts.questions,
		},
		{
			Code:     "assessment_snapshots_complete",
			Label:    "Assessment snapshots",
			Passed:   counts.questions > 0 && counts.snapshots == counts.questions,
			Severity: "blocker",
			Message: map[bool]string{
				true:  "Every configured question has an immutable assessment snapshot.",
				false: fmt.Sprintf("%d configured questions are missing assessment snapshots.", max(counts.questions-counts.snapshots, 0)),
			}[counts.questions > 0 && counts.snapshots == counts.questions],
			Count: max(counts.questions-counts.snapshots, 0),
		},
		{
			Code:     "answer_segments_present",
			Label:    "答案题块",
			Passed:   counts.segments > 0,
			Severity: "blocker",
			Message: map[bool]string{
				true:  fmt.Sprintf("已发现 %d 个答案题块。", counts.segments),
				false: "没有可阅卷的答案题块，请先完成采集、配准和切题。",
			}[counts.segments > 0],
			Count: counts.segments,
		},
		{
			Code:     "answer_segments_processed",
			Label:    "题块处理",
			Passed:   incomplete == 0,
			Severity: "blocker",
			Message: map[bool]string{
				true:  "全部题块均已完成处理并具备裁剪影像。",
				false: fmt.Sprintf("仍有 %d 个题块未处理完成或裁剪影像不可用。", incomplete),
			}[incomplete == 0],
			Count: max(incomplete, 0),
		},
		{
			Code:     "segment_metadata_complete",
			Label:    "模板与坐标",
			Passed:   counts.missingMetadata == 0,
			Severity: "blocker",
			Message: map[bool]string{
				true:  "题块模板、版本和答题区域信息完整。",
				false: fmt.Sprintf("有 %d 个题块缺少模板版本或答题区域信息。", counts.missingMetadata),
			}[counts.missingMetadata == 0],
			Count: counts.missingMetadata,
		},
		{
			Code:     "active_run_clear",
			Label:    "当前评分任务",
			Passed:   noActiveRun,
			Severity: "blocker",
			Message:  scoringRunAvailabilityMessage(activeRun),
		},
		{
			Code: "dual_mark_graders", Label: "双评阅卷员", Passed: counts.dualQuestions == 0 || counts.eligibleGraders >= 2,
			Severity: "blocker", Count: counts.eligibleGraders,
			Message: map[bool]string{true: "双评题具备同校两名在岗阅卷员。", false: "双评题至少需要同校两名在岗阅卷员，不能退化为单评。"}[counts.dualQuestions == 0 || counts.eligibleGraders >= 2],
		},
		{
			Code:     "automation_coverage",
			Label:    "自动判分覆盖",
			Passed:   counts.missingAutomation == 0,
			Severity: "warning",
			Message:  automationCoverageMessage(counts.automatic, counts.missingAutomation, manual),
			Count:    counts.missingAutomation,
		},
	}
	// 自动化配置不足可以转人工，因此 warning 不阻止启动；只有 blocker 决定 Ready。
	ready := true
	for _, check := range checks {
		if check.Severity == "blocker" && !check.Passed {
			ready = false
			break
		}
	}
	return ScoringReadiness{
		Ready:                  ready,
		ExamStatus:             status,
		TotalQuestions:         counts.questions,
		TotalSegments:          counts.segments,
		ReadySegments:          readySegments,
		AutomaticCandidates:    counts.automatic,
		ManualReviewCandidates: manual,
		ActiveRun:              activeRun,
		Checks:                 checks,
	}
}

func scoringRunAvailabilityMessage(run *ScoringRun) string {
	if run == nil {
		return "当前没有未结束的评分任务。"
	}
	switch run.Status {
	case "queued", "processing":
		return "已有评分任务正在处理，请进入阅卷监控查看进度。"
	case "needs_review":
		return "已有评分任务等待人工复核；完成或取消该任务后才能重新评分。"
	case "failed":
		return "已有评分任务失败；请重试失败项或取消该任务后再重新评分。"
	case "cancelling":
		return "评分任务正在取消，请稍后刷新。"
	default:
		return "已有未结束的评分任务，暂不能重复启动。"
	}
}

func automationCoverageMessage(automatic, missingAutomation, manual int) string {
	if missingAutomation > 0 {
		return fmt.Sprintf("预计 %d 个题块自动处理、%d 个客观/规则题块因配置不足转人工，另有 %d 个题块进入人工复核。", automatic, missingAutomation, max(manual-missingAutomation, 0))
	}
	return fmt.Sprintf("预计 %d 个题块自动处理、%d 个题块进入人工复核。", automatic, manual)
}
