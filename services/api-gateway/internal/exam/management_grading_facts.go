package exam

// managementGradingCompleteSQL 嵌入现有学科读取查询，和名册、任务及终分共用同一事务快照。
// 无答卷或仅人工切换到 finalized 不算结束；缺考不参与评分，但已收到的坏卷不能绕过完整性检查。
// 完整性口径与成绩确认一致：未完任务、身份页数、冻结题集覆盖和终分求和都必须通过；等待人工确认不影响阅卷完成。
const managementGradingCompleteSQL = `(
 EXISTS (SELECT 1 FROM submission present
   WHERE present.tenant_id=e.tenant_id AND present.exam_id=e.id AND present.deleted_at IS NULL)
 AND EXISTS (SELECT 1 FROM exam_question_snapshot question
   WHERE question.tenant_id=e.tenant_id AND question.exam_id=e.id)
 AND NOT EXISTS (SELECT 1 FROM review_task task
   WHERE task.tenant_id=e.tenant_id AND task.exam_id=e.id AND task.deleted_at IS NULL
     AND task.status NOT IN ('submitted','completed','cancelled'))
 AND NOT EXISTS (SELECT 1 FROM arbitration_task task
   WHERE task.tenant_id=e.tenant_id AND task.exam_id=e.id AND task.deleted_at IS NULL
     AND task.status NOT IN ('submitted','cancelled'))
 AND NOT EXISTS (SELECT 1 FROM ocr_task task
   JOIN submission sub ON sub.tenant_id=task.tenant_id AND sub.id=task.submission_id AND sub.deleted_at IS NULL
   WHERE task.tenant_id=e.tenant_id AND sub.exam_id=e.id AND task.deleted_at IS NULL AND task.status='failed')
 AND NOT EXISTS (
   SELECT 1 FROM submission sub
   WHERE sub.tenant_id=e.tenant_id AND sub.exam_id=e.id AND sub.deleted_at IS NULL AND (
     sub.student_id IS NULL OR sub.identity_status IN ('unknown','conflict')
     OR sub.actual_page_count<sub.expected_page_count OR sub.quality_status='failed' OR sub.status='rejected'
     OR NOT EXISTS (SELECT 1 FROM exam_candidate_snapshot candidate
       WHERE candidate.tenant_id=sub.tenant_id AND candidate.exam_id=sub.exam_id AND candidate.student_id=sub.student_id)
     OR (SELECT COUNT(*) FROM submission duplicate
       WHERE duplicate.tenant_id=sub.tenant_id AND duplicate.exam_id=sub.exam_id
         AND duplicate.student_id=sub.student_id AND duplicate.deleted_at IS NULL)<>1
     OR NOT EXISTS (SELECT 1 FROM submission_grade grade
       WHERE grade.tenant_id=sub.tenant_id AND grade.exam_id=sub.exam_id AND grade.submission_id=sub.id
         AND grade.student_id=sub.student_id AND grade.deleted_at IS NULL
         AND grade.status IN ('pending_confirmation','confirmed','pending_publish','published','locked')
         AND grade.max_score=e.total_score
         AND EXISTS (
           SELECT 1 FROM (
             SELECT SUM(latest.score) AS score,SUM(latest.max_score) AS max_score FROM (
               SELECT DISTINCT ON (final.answer_segment_id) final.score,final.max_score
               FROM final_grade final
               WHERE final.tenant_id=sub.tenant_id AND final.exam_id=sub.exam_id
                 AND final.submission_id=sub.id AND final.deleted_at IS NULL
               ORDER BY final.answer_segment_id,final.created_at DESC,final.id DESC
             ) latest
           ) totals WHERE grade.total_score=totals.score AND grade.max_score=totals.max_score
         ))
     OR EXISTS (SELECT 1 FROM exam_question_snapshot question
       WHERE question.tenant_id=sub.tenant_id AND question.exam_id=sub.exam_id AND NOT EXISTS (
         SELECT 1 FROM answer_segment segment
         JOIN final_grade grade ON grade.tenant_id=segment.tenant_id AND grade.answer_segment_id=segment.id AND grade.deleted_at IS NULL
         WHERE segment.tenant_id=sub.tenant_id AND segment.submission_id=sub.id
           AND segment.question_id=question.question_id AND segment.deleted_at IS NULL))
     OR EXISTS (SELECT 1 FROM answer_segment segment
       WHERE segment.tenant_id=sub.tenant_id AND segment.submission_id=sub.id AND segment.deleted_at IS NULL
         AND NOT EXISTS (SELECT 1 FROM final_grade grade
           WHERE grade.tenant_id=segment.tenant_id AND grade.answer_segment_id=segment.id AND grade.deleted_at IS NULL))
   )
 )
)`
