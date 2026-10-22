-- 模板页匹配在质检通过后自动排队；遗漏任务类型会使同事务的质检结果一起回滚。
-- 保留现有任务类型，仅补齐 page_template_match，不改写已应用的历史迁移。
ALTER TABLE agent_worker_task DROP CONSTRAINT chk_agent_worker_task_type;
ALTER TABLE agent_worker_task ADD CONSTRAINT chk_agent_worker_task_type CHECK (task_type IN (
  'ocr', 'layout', 'preprocess', 'image_quality', 'ai_grade', 'evidence_verify',
  'report_generate', 'export', 'desktop_sync', 'capture_file_decode',
  'page_registration', 'answer_segment_crop', 'page_registration_correction_preview',
  'omr_extract', 'paper_parse', 'paper_formula', 'page_template_match'
));
