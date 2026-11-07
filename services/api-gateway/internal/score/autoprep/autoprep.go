// Package autoprep 保存“创建发布草稿时自动准备成绩”的共享条件。
// 写入方 score.PrepareForReleaseTx 与发布前的只读检查（名册、质量报告、发布门禁）都从这里取条件，
// 避免读取时判断“可以自动生成终评”与真正写入时用的条件逐渐不一致。
// 本包只依赖 database/sql；score 与 scorerelease 都可导入，不形成导入环。
package autoprep

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// FinalSourcesCTE 是 WITH 子句中的 CTE 列表片段（不含 WITH 关键字），参数 $1 为租户 ID、$2 为考试 ID。
// 它列出准备时会直接生成的终评：尚无未删除终评的答题区中，单评最新一次已提交的人工分；
// 当前已确认的规则分；以及尚未迁入题分模型的旧规则分（仍排除 Mock、待复核和非规则评分，模型建议不能直接变成终评）。
// 同一答题区人工分优先：准备时先插入人工终评，规则终评再按“尚无终评”跳过这些答题区。
// automatic_final 合并两类来源，每个答题区最多一行。
// 考试 ID 按文本比较，与原写入 SQL 语义相同；显式 $2::text 固定参数类型，拼接到用 $2::uuid 的语句中也不会推断冲突。
// 调用方把本片段放在自己的 CTE 之前，并用逗号连接后续 CTE。
const FinalSourcesCTE = `automatic_human_final AS (
  SELECT DISTINCT ON (rt.answer_segment_id)
    rt.tenant_id, rt.exam_id, rt.question_id, rt.question_no, rt.answer_segment_id, rt.submission_id,
    rt.anonymous_code, hg.score, hg.max_score
  FROM review_task rt
  JOIN human_grade hg ON hg.tenant_id = rt.tenant_id AND hg.review_task_id = rt.id AND hg.deleted_at IS NULL
  WHERE rt.tenant_id = $1::uuid
    AND rt.exam_id::text = $2::text
    AND rt.deleted_at IS NULL
    AND rt.grade_round = 'single'
    AND rt.status IN ('submitted', 'completed')
    AND NOT EXISTS (
      SELECT 1 FROM final_grade fg
      WHERE fg.tenant_id = rt.tenant_id AND fg.answer_segment_id = rt.answer_segment_id AND fg.deleted_at IS NULL
    )
  ORDER BY rt.answer_segment_id, hg.created_at DESC
), automatic_confirmed_rule AS (
  SELECT g.tenant_id, g.exam_id, g.question_id, seg.question_no, g.answer_segment_id,
    seg.submission_id, COALESCE(NULLIF(sub.candidate_no, ''), seg.id::text) AS anonymous_code,
    g.score, g.max_score
  FROM question_grade g
  JOIN answer_segment seg ON seg.tenant_id=g.tenant_id AND seg.id=g.answer_segment_id AND seg.deleted_at IS NULL
  JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
  WHERE g.tenant_id=$1::uuid AND g.exam_id::text=$2::text AND g.deleted_at IS NULL
    AND g.is_current AND g.status='confirmed' AND g.source='rule_confirmed'
), automatic_legacy_rule AS (
  SELECT DISTINCT ON (ag.answer_segment_id)
    ag.tenant_id, sub.exam_id, ag.question_id, ag.question_no, ag.answer_segment_id,
    seg.submission_id, COALESCE(NULLIF(sub.candidate_no, ''), seg.id::text) AS anonymous_code,
    ag.suggested_score AS score, ag.max_score
  FROM ai_grade ag
  JOIN answer_segment seg ON seg.tenant_id = ag.tenant_id AND seg.id = ag.answer_segment_id AND seg.deleted_at IS NULL
  JOIN submission sub ON sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id AND sub.deleted_at IS NULL
  WHERE ag.tenant_id = $1::uuid
    AND sub.exam_id::text = $2::text
    AND ag.deleted_at IS NULL
    AND ag.grader_type = 'rule_based_objective'
    AND ag.auto_pass = true
    AND ag.needs_human_review = false
    AND ag.mock = false
    AND ag.status = 'succeeded'
    AND NOT EXISTS (
      SELECT 1 FROM question_grade g
      WHERE g.tenant_id=ag.tenant_id AND g.answer_segment_id=ag.answer_segment_id AND g.deleted_at IS NULL
    )
  ORDER BY ag.answer_segment_id, ag.created_at DESC
), automatic_rule_final AS (
  SELECT candidate.* FROM (
    SELECT * FROM automatic_confirmed_rule
    UNION ALL
    SELECT * FROM automatic_legacy_rule
  ) candidate
  WHERE NOT EXISTS (
    SELECT 1 FROM final_grade fg
    WHERE fg.tenant_id = candidate.tenant_id AND fg.answer_segment_id = candidate.answer_segment_id AND fg.deleted_at IS NULL
  )
  AND NOT EXISTS (
    SELECT 1 FROM automatic_human_final human WHERE human.answer_segment_id = candidate.answer_segment_id
  )
), automatic_final AS (
  SELECT tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code, score, max_score
  FROM automatic_human_final
  UNION ALL
  SELECT tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code, score, max_score
  FROM automatic_rule_final
)`

// State 是自动准备的前提，与 score.PrepareForReleaseTx 的判断一致。
type State struct {
	// Published 表示已有正式发布；之后的改分走复评更正，不再自动汇总，避免覆盖已发布事实。
	Published bool
	// Locked 表示有旧版公开后锁定的总分；准备只做只读核对，不生成终评、不确认。
	Locked bool
	// Invalid 表示有状态不在 pending_confirmation/confirmed/published/locked 中的总分；准备直接判为未就绪。
	Invalid bool
}

// Automatic 为 true 时，创建草稿会补齐终评、重算可恢复的总分，并在检查通过后确认。
func (s State) Automatic() bool { return !s.Published && !s.Locked && !s.Invalid }

// RecalculableTotalPredicate 限定首发前可自动重算的总分。确认会把已有终评一并改为 confirmed；
// 后到的 pending_confirmation 终评是总分尚未包含它的明确事实，不能用事务 now() 推断提交顺序。
// 无待确认终评的总分篡改仍被门禁拒绝；已正式发布后的成绩必须通过复评更正。
func RecalculableTotalPredicate(alias string) string {
	return strings.NewReplacer("{grade}", alias).Replace(`({grade}.locked=false AND (
  {grade}.status='pending_confirmation' OR (
    {grade}.status='confirmed'
    AND NOT EXISTS (SELECT 1 FROM score_release_current published
      WHERE published.tenant_id={grade}.tenant_id AND published.exam_id={grade}.exam_id)
    AND EXISTS (SELECT 1 FROM final_grade newer
      WHERE newer.tenant_id={grade}.tenant_id AND newer.exam_id={grade}.exam_id
        AND newer.submission_id={grade}.submission_id AND newer.deleted_at IS NULL
        AND newer.status='pending_confirmation'
        AND NOT EXISTS (SELECT 1 FROM final_grade latest
          WHERE latest.tenant_id=newer.tenant_id AND latest.answer_segment_id=newer.answer_segment_id
            AND latest.deleted_at IS NULL AND ROW(latest.created_at,latest.id)>ROW(newer.created_at,newer.id)))
  )
))`)
}

// RecalculableAfterPreparationPredicate 供含 FinalSourcesCTE 的只读预测使用；准备会新建的终评同样处于待确认状态。
func RecalculableAfterPreparationPredicate(alias string) string {
	return "(" + RecalculableTotalPredicate(alias) + " OR " + strings.NewReplacer("{grade}", alias).Replace(`(
  {grade}.locked=false AND {grade}.status='confirmed'
  AND NOT EXISTS (SELECT 1 FROM score_release_current published
    WHERE published.tenant_id={grade}.tenant_id AND published.exam_id={grade}.exam_id)
  AND EXISTS (SELECT 1 FROM automatic_final upcoming WHERE upcoming.submission_id={grade}.submission_id)
))`)
}

// CollectionIncompleteQuery 只在还有未到卷学生时要求结束导入，不能在扫描尚未完成时把漏扫者静默当作缺考。
// 结束导入已经由收卷工作区核对名单；这里不恢复逐人标记缺考，也不把已收到的身份异常答卷排除在其它检查外。
const CollectionIncompleteQuery = `SELECT CASE WHEN EXISTS (
  SELECT 1 FROM exam_candidate_snapshot candidate
  WHERE candidate.tenant_id=$1::uuid AND candidate.exam_id=$2::uuid
    AND NOT EXISTS (SELECT 1 FROM submission sub
      WHERE sub.tenant_id=candidate.tenant_id AND sub.exam_id=candidate.exam_id
        AND sub.student_id=candidate.student_id AND sub.deleted_at IS NULL)
) AND NOT EXISTS (
  SELECT 1 FROM capture_collection collection
  WHERE collection.tenant_id=$1::uuid AND collection.exam_id=$2::uuid AND collection.completed_at IS NOT NULL
) THEN 1 ELSE 0 END`

// LockCollection 让实际发布与重新开放补传串行。只读门禁不调用它；调用方负责同一事务的提交或回滚。
// 尚无台账时不伪造“已结束”；CollectionIncompleteQuery 会继续阻止未到卷学生被静默算缺考。
func LockCollection(ctx context.Context, tx *sql.Tx, tenantID, examID string) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT exam_id::text FROM capture_collection WHERE tenant_id=$1::uuid AND exam_id=$2::uuid FOR SHARE`, tenantID, examID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ReadState 在一条语句里读取三项前提。写入方须在持有考试锁的事务中调用；
// 只读检查不加锁，读到的是语句开始时的快照，只用于展示，不作为发布依据。
func ReadState(ctx context.Context, q rowQueryer, tenantID, examID string) (State, error) {
	var state State
	err := q.QueryRowContext(ctx, `SELECT
  EXISTS (SELECT 1 FROM score_release_current WHERE tenant_id=$1::uuid AND exam_id=$2::uuid),
  EXISTS (SELECT 1 FROM submission_grade WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL
    AND (locked = true OR status IN ('published', 'locked'))),
  EXISTS (SELECT 1 FROM submission_grade WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL
    AND status NOT IN ('pending_confirmation','confirmed','published','locked'))`, tenantID, examID).Scan(&state.Published, &state.Locked, &state.Invalid)
	return state, err
}

// NewTotalPredicate 返回一个 SQL 条件：alias 指向的答卷（本考试未删除的 submission 行）在准备后会新建总分。
// 重算按 (tenant_id, exam_id, submission_id) 冲突更新；已有任何总分行（含已删除）时不会新建可见行，
// 所以要求完全没有总分行，并且已有终评或 automatic_final 中有该答卷。
// 语句须已包含 FinalSourcesCTE。IN 子查询排除 NULL，取反时也不会因三值逻辑漏判。
func NewTotalPredicate(alias string) string {
	return strings.NewReplacer("{sub}", alias).Replace(`(NOT EXISTS (
    SELECT 1 FROM submission_grade existing
    WHERE existing.tenant_id={sub}.tenant_id AND existing.exam_id={sub}.exam_id AND existing.submission_id={sub}.id
  ) AND (
    EXISTS (
      SELECT 1 FROM final_grade fg
      WHERE fg.tenant_id={sub}.tenant_id AND fg.exam_id={sub}.exam_id AND fg.submission_id={sub}.id AND fg.deleted_at IS NULL
    )
    OR {sub}.id IN (SELECT af.submission_id FROM automatic_final af WHERE af.submission_id IS NOT NULL)
  ))`)
}
