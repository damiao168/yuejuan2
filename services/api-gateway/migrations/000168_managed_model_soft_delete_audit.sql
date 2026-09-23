ALTER TABLE managed_model_api_config
  ADD COLUMN deleted_by UUID,
  ADD COLUMN deletion_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE managed_model_api_config
  ADD CONSTRAINT fk_managed_model_deleted_by
  FOREIGN KEY (deleted_by) REFERENCES app_user(id);
