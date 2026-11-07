package scorerelease

import "edugrade-enterprise/services/api-gateway/internal/score/autoprep"

// 本文件生成门禁里与“自动准备成绩”相关的检查。
// prepared=false 是严格版本：发布事务内的 gateTx 和未注入成绩准备器时使用，只看当前事实。
// prepared=true 只用于只读 Gate()：已注入准备器且 autoprep.State.Automatic() 成立时，创建草稿会在同一事务中
// 补齐终评、重算可恢复的总分并确认，所以按准备之后的状态判断。自动终评只从 autoprep.FinalSourcesCTE 取，
// 与 score 写入终评的条件相同；准备做不到的事实（缺评分、满分不一致、已确认总分与终评不符）仍报告。

// gateMissingFinalGradesQuery 统计冻结题集中没有终评的答题区；准备后版本再排除会自动生成终评的答题区。
func gateMissingFinalGradesQuery(prepared bool) string {
	const strict = `SELECT COUNT(*) FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM exam_question_snapshot eqs
    WHERE eqs.tenant_id=seg.tenant_id AND eqs.exam_id=sub.exam_id AND eqs.question_id=seg.question_id)
  AND NOT EXISTS (SELECT 1 FROM final_grade fg
    WHERE fg.tenant_id=seg.tenant_id AND fg.answer_segment_id=seg.id AND fg.deleted_at IS NULL)`
	if !prepared {
		return strict
	}
	return `WITH ` + autoprep.FinalSourcesCTE + `
` + strict + `
  AND NOT EXISTS (SELECT 1 FROM automatic_final af WHERE af.answer_segment_id=seg.id)`
}

// gateNoSubmissionGradesQuery 在没有可发布总分时返回 1；准备后版本把准备会新建的总分也算上。
func gateNoSubmissionGradesQuery(prepared bool) string {
	if !prepared {
		return `SELECT CASE WHEN EXISTS (SELECT 1 FROM submission_grade WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL) THEN 0 ELSE 1 END`
	}
	return `WITH ` + autoprep.FinalSourcesCTE + `
SELECT CASE WHEN EXISTS (SELECT 1 FROM submission_grade WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL)
  OR EXISTS (SELECT 1 FROM submission sub WHERE sub.tenant_id = $1::uuid AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL
    AND ` + autoprep.NewTotalPredicate("sub") + `) THEN 0 ELSE 1 END`
}

// gateNotConfirmedQuery 返回空字符串表示不检查：准备成功后会确认全部待确认总分，准备失败的原因由其它检查报告。
func gateNotConfirmedQuery(prepared bool) string {
	if prepared {
		return ""
	}
	return `SELECT COUNT(*) FROM submission_grade WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL AND status NOT IN ('confirmed', 'published', 'locked')`
}

// gateQuestionCoverageQuery 统计答题区、终评与冻结题集或考试满分不一致的答卷。
// 两个版本共用同一模板，只在终评来源中并入自动终评。终评条件先按答卷汇总再连接，
// 避免对 CTE 做逐份答卷的关联子查询。
func gateQuestionCoverageQuery(prepared bool) string {
	prefix := `WITH `
	finals := `(SELECT DISTINCT ON (fg.answer_segment_id) fg.submission_id, fg.question_id, fg.max_score FROM final_grade fg
  JOIN submission owner ON owner.tenant_id=fg.tenant_id AND owner.id=fg.submission_id
  WHERE fg.tenant_id=$1::uuid AND owner.exam_id=$2::uuid AND fg.deleted_at IS NULL
  ORDER BY fg.answer_segment_id,fg.created_at DESC,fg.id DESC)`
	if prepared {
		prefix = `WITH ` + autoprep.FinalSourcesCTE + `, `
		finals += `
  UNION ALL
  SELECT af.submission_id, af.question_id, af.max_score FROM automatic_final af`
	}
	return prefix + `official AS (
  SELECT q.id,q.score::float8 FROM question q
  WHERE q.tenant_id=$1::uuid AND q.exam_id=$2::uuid AND q.deleted_at IS NULL
    AND EXISTS (SELECT 1 FROM exam_question_snapshot eqs
      WHERE eqs.tenant_id=q.tenant_id AND eqs.exam_id=q.exam_id AND eqs.question_id=q.id)
), official_summary AS (
  SELECT COUNT(*)::int AS question_count,COALESCE(SUM(score),0)::float8 AS total FROM official
), coverage_final AS (
  ` + finals + `
), final_summary AS (
  SELECT cf.submission_id, COUNT(DISTINCT cf.question_id)::int AS question_count,
    COALESCE(SUM(cf.max_score),0)::float8 AS max_total,
    BOOL_OR(ABS(cf.max_score::float8-q.score)>0.000001) AS max_mismatch
  FROM coverage_final cf JOIN official q ON q.id=cf.question_id
  GROUP BY cf.submission_id
)
SELECT COUNT(*) FROM submission sub CROSS JOIN official_summary summary JOIN exam e ON e.tenant_id=sub.tenant_id AND e.id=sub.exam_id
LEFT JOIN final_summary finals ON finals.submission_id=sub.id
WHERE sub.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.deleted_at IS NULL AND (
  summary.question_count<>(SELECT COUNT(DISTINCT seg.question_id) FROM answer_segment seg JOIN official q ON q.id=seg.question_id WHERE seg.tenant_id=sub.tenant_id AND seg.submission_id=sub.id AND seg.deleted_at IS NULL)
  OR EXISTS (SELECT 1 FROM official q WHERE NOT EXISTS (SELECT 1 FROM answer_segment seg WHERE seg.tenant_id=sub.tenant_id AND seg.submission_id=sub.id AND seg.question_id=q.id AND seg.deleted_at IS NULL))
  OR ABS(summary.total-e.total_score::float8)>0.000001
  OR summary.question_count<>COALESCE(finals.question_count,0)
  OR ABS(summary.total-COALESCE(finals.max_total,0))>0.000001
  OR COALESCE(finals.max_mismatch,false)
)`
}

// gateScoreIntegrityQuery 统计总分与冻结题集终评合计不一致的答卷。两个版本共用同一比较。
// 准备后版本模拟 score.recalculateSubmissionGradesTx：每个答题区取最新终评（含自动终评）按答卷合计；
// 待确认总分，以及首发前确有新待确认终评的已确认总分，采用重算值；其余已确认或已锁定记录仍严格核对。
func gateScoreIntegrityQuery(prepared bool) string {
	prefix := `WITH `
	finals := `(SELECT DISTINCT ON (fg.answer_segment_id) fg.submission_id, fg.question_id, fg.score FROM final_grade fg
  WHERE fg.tenant_id=$1::uuid AND fg.exam_id=$2::uuid AND fg.deleted_at IS NULL
  ORDER BY fg.answer_segment_id,fg.created_at DESC,fg.id DESC)`
	grades := `SELECT sg.submission_id, sg.total_score::float8 AS total_score FROM submission_grade sg
  WHERE sg.tenant_id=$1::uuid AND sg.exam_id=$2::uuid AND sg.deleted_at IS NULL`
	if prepared {
		prefix = `WITH ` + autoprep.FinalSourcesCTE + `, latest_final AS (
  SELECT DISTINCT ON (fg.answer_segment_id) fg.submission_id, fg.score
  FROM final_grade fg WHERE fg.tenant_id=$1::uuid AND fg.exam_id=$2::uuid AND fg.deleted_at IS NULL
  ORDER BY fg.answer_segment_id, fg.created_at DESC, fg.id DESC
), recalculated AS (
  SELECT merged.submission_id, SUM(merged.score)::float8 AS total_score FROM (
    SELECT submission_id, score FROM latest_final
    UNION ALL
    SELECT submission_id, score FROM automatic_final
  ) merged
  JOIN submission owner ON owner.tenant_id=$1::uuid AND owner.id=merged.submission_id AND owner.deleted_at IS NULL
  GROUP BY merged.submission_id
), `
		finals += `
  UNION ALL
  SELECT af.submission_id, af.question_id, af.score FROM automatic_final af`
		grades = `SELECT sg.submission_id,
    CASE WHEN ` + autoprep.RecalculableAfterPreparationPredicate("sg") + ` AND recalculated.submission_id IS NOT NULL
      THEN recalculated.total_score ELSE sg.total_score::float8 END AS total_score
  FROM submission_grade sg LEFT JOIN recalculated ON recalculated.submission_id=sg.submission_id
  WHERE sg.tenant_id=$1::uuid AND sg.exam_id=$2::uuid AND sg.deleted_at IS NULL
  UNION ALL
  SELECT sub.id, recalculated.total_score FROM submission sub JOIN recalculated ON recalculated.submission_id=sub.id
  WHERE sub.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.deleted_at IS NULL AND ` + autoprep.NewTotalPredicate("sub")
	}
	return prefix + `gate_final AS (
  ` + finals + `
), gate_grade AS (
  ` + grades + `
), gate_total AS (
  SELECT gf.submission_id, SUM(gf.score)::float8 AS total FROM gate_final gf
  WHERE EXISTS (SELECT 1 FROM exam_question_snapshot eqs
    WHERE eqs.tenant_id=$1::uuid AND eqs.exam_id=$2::uuid AND eqs.question_id=gf.question_id)
  GROUP BY gf.submission_id
)
SELECT COUNT(*) FROM gate_grade sg LEFT JOIN gate_total total ON total.submission_id=sg.submission_id
WHERE ABS(sg.total_score-COALESCE(total.total,0))>0.000001`
}
