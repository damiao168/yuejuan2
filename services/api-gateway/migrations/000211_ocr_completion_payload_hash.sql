-- 新完成任务保留原输入指纹，幂等判断不依赖随机结果顺序或 NUMERIC(5,4) 回读精度。
-- 历史完成任务保持 NULL，只能按已保存精度比较；不虚构已丢失的原始小数。
ALTER TABLE ocr_task ADD COLUMN completion_payload_hash TEXT;
ALTER TABLE ocr_task ADD CONSTRAINT ocr_completion_payload_hash_format
  CHECK (completion_payload_hash IS NULL OR completion_payload_hash ~ '^[0-9a-f]{64}$');
