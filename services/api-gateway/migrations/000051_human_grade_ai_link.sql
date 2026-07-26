-- Link each human_grade to the AI suggestion (ai_grade) that was visible to
-- the teacher at submit time, so calibration can answer "which AI suggestion
-- did this human decision override". The value is resolved server-side from
-- the tenant-scoped task context, never trusted from the client.
--
-- Foreign key shape: ai_grade has NO UNIQUE (tenant_id, id) constraint --
-- migration 000020 only registered ai_grade as a child table via
-- ensure_tenant_fk and never called ensure_tenant_identity('ai_grade', ...).
-- A composite FOREIGN KEY (tenant_id, ai_grade_id) REFERENCES
-- ai_grade(tenant_id, id) in the 000036 style is therefore not possible
-- without first adding that unique constraint to a large production table.
-- We use a plain single-column FK against the ai_grade primary key instead;
-- tenant consistency is enforced by the application, which copies
-- ai_grade_id from a context row already filtered by tenant_id.

ALTER TABLE human_grade
  ADD COLUMN IF NOT EXISTS ai_grade_id UUID;

ALTER TABLE human_grade
  DROP CONSTRAINT IF EXISTS fk_human_grade_ai_grade;

ALTER TABLE human_grade
  ADD CONSTRAINT fk_human_grade_ai_grade
  FOREIGN KEY (ai_grade_id) REFERENCES ai_grade(id);

CREATE INDEX IF NOT EXISTS idx_human_grade_ai_grade
ON human_grade (tenant_id, ai_grade_id)
WHERE ai_grade_id IS NOT NULL AND deleted_at IS NULL;
