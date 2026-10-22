-- 学校库只收录已锁定、人工声明为空白参考的全套模板。旧模板不回填审核事实。
-- 库题区保存 question_no 槽位；本场实例另表绑定，跨考试不得复用 question_id 或 OMR 校准。
CREATE TABLE answer_sheet_template_library (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id UUID NOT NULL REFERENCES tenant(id),
 school_id UUID NOT NULL REFERENCES school(id),
 source_template_id UUID NOT NULL REFERENCES answer_sheet_template(id),
 source_revision INT NOT NULL CHECK (source_revision > 0),
 reference_file_asset_id UUID NOT NULL REFERENCES file_asset(id),
 name TEXT NOT NULL,
 page_count INT NOT NULL CHECK (page_count BETWEEN 1 AND 100),
 layout JSONB NOT NULL,
 source_sha256 TEXT NOT NULL CHECK (length(source_sha256)=64),
 content_hash TEXT NOT NULL CHECK (length(content_hash)=64),
 reviewed_by UUID NOT NULL REFERENCES app_user(id),
 reviewed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE (tenant_id, source_template_id, source_revision),
 UNIQUE (tenant_id, school_id, source_sha256, content_hash)
);
CREATE INDEX answer_sheet_template_library_school ON answer_sheet_template_library(tenant_id,school_id,reviewed_at DESC);
CREATE INDEX answer_sheet_template_library_exact ON answer_sheet_template_library(tenant_id,school_id,source_sha256);

CREATE TABLE answer_sheet_template_library_instance (
 tenant_id UUID NOT NULL REFERENCES tenant(id),
 exam_id UUID NOT NULL REFERENCES exam(id),
 library_id UUID NOT NULL REFERENCES answer_sheet_template_library(id),
 template_id UUID NOT NULL REFERENCES answer_sheet_template(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,exam_id,library_id),
 UNIQUE(tenant_id,template_id)
);

CREATE FUNCTION reject_answer_sheet_library_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'published answer sheet library versions are immutable';
END;
$$;
CREATE TRIGGER answer_sheet_library_immutable BEFORE UPDATE OR DELETE ON answer_sheet_template_library
 FOR EACH ROW EXECUTE FUNCTION reject_answer_sheet_library_mutation();

-- 多个元数据资产可以引用同一个对象；固定对象后，源文件和实例文件也不能先删对象再报告失败。
CREATE FUNCTION protect_answer_sheet_library_reference() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.deleted_at IS NOT NULL OR NEW.lifecycle_status IN ('pending_delete','deleted')
    OR NEW.storage_bucket IS DISTINCT FROM OLD.storage_bucket OR NEW.storage_key IS DISTINCT FROM OLD.storage_key
    OR NEW.hash_sha256 IS DISTINCT FROM OLD.hash_sha256 THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('answer-sheet-reference/'||OLD.tenant_id::text||'/'||OLD.storage_bucket||'/'||OLD.storage_key,0));
  IF EXISTS(SELECT 1 FROM answer_sheet_template_library l JOIN file_asset f
    ON f.tenant_id=l.tenant_id AND f.id=l.reference_file_asset_id
    WHERE l.tenant_id=OLD.tenant_id AND f.storage_bucket=OLD.storage_bucket AND f.storage_key=OLD.storage_key) THEN
   RAISE EXCEPTION 'answer sheet library reference object is pinned';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER answer_sheet_library_reference_pinned BEFORE UPDATE OR DELETE ON file_asset
 FOR EACH ROW EXECUTE FUNCTION protect_answer_sheet_library_reference();

ALTER TABLE answer_sheet_template_library ENABLE ROW LEVEL SECURITY;
ALTER TABLE answer_sheet_template_library FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON answer_sheet_template_library FOR ALL
 USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
ALTER TABLE answer_sheet_template_library_instance ENABLE ROW LEVEL SECURITY;
ALTER TABLE answer_sheet_template_library_instance FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON answer_sheet_template_library_instance FOR ALL
 USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
