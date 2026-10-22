-- 每次选择文件独立归组；旧文件不回填，继续使用原有文件拆页流程。
-- 先增加可空字段，再发布使用这些字段的 API；不依赖 194–199 的迁移。
ALTER TABLE capture_file
  ADD COLUMN upload_group_id UUID,
  ADD COLUMN upload_group_file_count INT,
  ADD COLUMN upload_group_file_index INT,
  ADD COLUMN pages_per_student INT,
  ADD CONSTRAINT capture_file_upload_group_check CHECK (
    (upload_group_id IS NULL AND upload_group_file_count IS NULL AND upload_group_file_index IS NULL AND pages_per_student IS NULL)
    OR (upload_group_id IS NOT NULL AND upload_group_file_count IS NOT NULL AND upload_group_file_index IS NOT NULL AND pages_per_student IS NOT NULL
      AND upload_group_file_count BETWEEN 1 AND 1000
      AND upload_group_file_index BETWEEN 1 AND upload_group_file_count
      AND pages_per_student BETWEEN 1 AND 20)
  );
CREATE UNIQUE INDEX idx_capture_file_upload_group_position
  ON capture_file (tenant_id,capture_batch_id,upload_group_id,upload_group_file_index)
  WHERE upload_group_id IS NOT NULL AND deleted_at IS NULL;
