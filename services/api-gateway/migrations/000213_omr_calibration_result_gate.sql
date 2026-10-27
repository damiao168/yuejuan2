-- 校准样本冻结与实际自动确认相同的复核标志和集合质量，分层名称不再冒充资格。
-- 旧样本仅从对应已成功完成的 Runtime 精确结果补齐；不能恢复的证据保留空对象并阻断后续授权。
-- 历史批准及 evidence_hash 保留，应用层在新任务与在途回调中复验，不能静默重写旧审批证据。
ALTER TABLE omr_calibration_case
  ADD COLUMN observed_result_gate JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD CONSTRAINT chk_omr_calibration_case_result_gate_object
    CHECK (jsonb_typeof(observed_result_gate) = 'object');

UPDATE omr_calibration_case c
SET observed_result_gate = jsonb_build_object(
  'needs_human_review', task.result->'needs_human_review',
  'thresholds', task.result->'thresholds')
FROM omr_run o
JOIN agent_worker_task task ON task.tenant_id=o.tenant_id AND task.id=o.runtime_task_id
  AND task.source_type='omr_run' AND task.source_id=o.id
WHERE c.tenant_id=o.tenant_id AND c.omr_run_id=o.id
  AND task.status='succeeded'
  AND task.result->>'omr_run_id'=o.id::text
  AND task.result->>'result_version'=o.result_version
  AND jsonb_typeof(task.result->'needs_human_review')='boolean'
  AND task.result ? 'thresholds';
