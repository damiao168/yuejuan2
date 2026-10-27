-- 189 新增的量规建议队列晚于 183 的固定保护表清单，需要显式补入归档边界。
-- 已归档考试的历史活跃任务不能再调用模型或采用结果；先收敛到 stale，再安装只读触发器。
UPDATE question_rubric_draft_job j
SET status='stale',lease_token='',lease_until=NULL,updated_at=now(),
  payload=jsonb_set(jsonb_set(payload,'{Job,result}','null'::jsonb),'{Job,error}',to_jsonb('考试已归档，旧建议任务已停止'::text))
FROM exam e LEFT JOIN exam_session parent ON parent.tenant_id=e.tenant_id AND parent.id=e.exam_session_id
WHERE e.tenant_id=j.tenant_id AND e.id=j.exam_id
  AND j.status IN ('queued','running','cancelling')
  AND (e.archived_at IS NOT NULL OR e.status='archived' OR parent.archived_at IS NOT NULL OR parent.status='archived');

INSERT INTO exam_archive_guarded_table(relation)
VALUES('question_rubric_draft_job'::regclass) ON CONFLICT DO NOTHING;

CREATE TRIGGER trg_exam_archive_readonly
BEFORE INSERT OR UPDATE OR DELETE ON question_rubric_draft_job
FOR EACH ROW EXECUTE FUNCTION reject_archived_exam_business_mutation();
