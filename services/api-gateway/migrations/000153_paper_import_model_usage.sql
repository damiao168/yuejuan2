ALTER TABLE paper_import_job
  ADD COLUMN IF NOT EXISTS model_usage JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE paper_import_job
  ADD CONSTRAINT chk_paper_import_job_model_usage
  CHECK (jsonb_typeof(model_usage) = 'object');

COMMENT ON COLUMN paper_import_job.model_usage IS
  'Provider-reported token usage for the current paper parsing result; billing remains provider-authoritative.';
