-- Immutable, release-scoped anonymous paper pages.  A student-facing share
-- can resolve only through this table; the source submission asset is never
-- returned by the sharing route.
CREATE TABLE score_release_anonymous_page (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  release_id UUID NOT NULL,
  source_submission_page_id UUID NOT NULL,
  page_no INT NOT NULL,
  file_asset_id UUID NOT NULL,
  source_sha256 TEXT NOT NULL,
  derived_sha256 TEXT NOT NULL,
  template_id UUID NOT NULL,
  template_content_hash TEXT NOT NULL,
  redaction_version TEXT NOT NULL,
  created_by UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_by UUID,
  revoked_at TIMESTAMPTZ,
  UNIQUE (tenant_id, release_id, page_no),
  UNIQUE (tenant_id, release_id, source_submission_page_id),
  UNIQUE (tenant_id, file_asset_id),
  CONSTRAINT fk_release_anonymous_page_release FOREIGN KEY (tenant_id, release_id)
    REFERENCES score_release (tenant_id, id),
  CONSTRAINT fk_release_anonymous_page_source FOREIGN KEY (tenant_id, source_submission_page_id)
    REFERENCES submission_page (tenant_id, id),
  CONSTRAINT fk_release_anonymous_page_asset FOREIGN KEY (tenant_id, file_asset_id)
    REFERENCES file_asset (tenant_id, id),
  CONSTRAINT fk_release_anonymous_page_template FOREIGN KEY (tenant_id, template_id)
    REFERENCES answer_sheet_template (tenant_id, id),
  CONSTRAINT fk_release_anonymous_page_created_by FOREIGN KEY (tenant_id, created_by)
    REFERENCES app_user (tenant_id, id),
  CONSTRAINT fk_release_anonymous_page_revoked_by FOREIGN KEY (tenant_id, revoked_by)
    REFERENCES app_user (tenant_id, id),
  CHECK (page_no > 0),
  CHECK (length(source_sha256) = 64),
  CHECK (length(derived_sha256) = 64),
  CHECK (length(template_content_hash) > 0),
  CHECK (redaction_version = 'identity-regions-v1'),
  CHECK ((revoked_at IS NULL AND revoked_by IS NULL) OR (revoked_at IS NOT NULL AND revoked_by IS NOT NULL))
);

CREATE INDEX idx_release_anonymous_page_active
ON score_release_anonymous_page (tenant_id, release_id, page_no)
WHERE revoked_at IS NULL;
