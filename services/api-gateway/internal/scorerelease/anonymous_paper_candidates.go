package scorerelease

// 调用方先定义唯一 release 行（id、tenant_id、exam_id、visibility_policy）。
// 本校范围取考试冻结名册中的班级归属，不按学生当前学校筛选，避免转学改变历史候选。
// 新策略按分数名次纳入截止同分；旧版本未保存人数时仍只取原先稳定排序的第一份。
const excellentPaperCandidatesCTE = `,
eligible_papers AS (
  SELECT item.submission_id,item.total_score,item.max_score,
    RANK() OVER (ORDER BY item.total_score DESC) AS score_rank,
    ROW_NUMBER() OVER (ORDER BY item.total_score DESC,item.submission_id) AS source_order
  FROM release
  JOIN exam ON exam.tenant_id=release.tenant_id AND exam.id=release.exam_id
  JOIN score_release_item item ON item.tenant_id=release.tenant_id AND item.release_id=release.id
    AND item.student_id IS NOT NULL
  JOIN exam_candidate_snapshot candidate ON candidate.tenant_id=item.tenant_id
    AND candidate.exam_id=release.exam_id AND candidate.student_id=item.student_id
  JOIN school_class frozen_class ON frozen_class.tenant_id=candidate.tenant_id
    AND frozen_class.id=candidate.class_id_snapshot AND frozen_class.school_id=exam.school_id
), selected_papers AS (
  SELECT eligible_papers.*
  FROM eligible_papers CROSS JOIN release
  WHERE CASE
    WHEN COALESCE((release.visibility_policy->>'high_score_paper_top_n')::integer,0)=0
      THEN source_order=1
    WHEN (release.visibility_policy->>'high_score_paper_top_n')::integer BETWEEN 1 AND 100
      THEN score_rank<=(release.visibility_policy->>'high_score_paper_top_n')::integer
    ELSE false
  END
)`
