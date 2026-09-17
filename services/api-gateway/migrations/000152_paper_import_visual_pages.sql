ALTER TABLE paper_import_parse_input
  ADD COLUMN pages JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE paper_import_parse_input
  ADD CONSTRAINT chk_paper_import_parse_input_pages
  CHECK (jsonb_typeof(pages) = 'array');

COMMENT ON COLUMN paper_import_parse_input.pages IS
'Immutable page-image references loaded by the API and sent directly to the configured multimodal model.';
