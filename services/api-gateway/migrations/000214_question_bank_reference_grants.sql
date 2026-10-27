-- 历史题目导入显式把准确冻结清单授予目标题库版本，不把受控裁图变为通用附件。
-- 授权只追加；新题库版本需复制授权记录，复制到考试保留父清单，原授权撤回即时阻断整条链。
-- 旧版本没有 reference_materials 时不补授权、不改变旧整包哈希；归档来源只读使用仍可保留。
ALTER TABLE question_reference_manifest ADD COLUMN origin_manifest_id UUID;
ALTER TABLE question_reference_manifest ADD FOREIGN KEY(tenant_id,origin_manifest_id) REFERENCES question_reference_manifest(tenant_id,id);

CREATE FUNCTION reference_authorization_active(p_tenant UUID,p_manifest UUID) RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
 WITH RECURSIVE chain AS (
  SELECT m.id,m.origin_manifest_id,m.use_state,ARRAY[m.id] path,1 depth,false cycle
  FROM question_reference_manifest m WHERE m.tenant_id=p_tenant AND m.id=p_manifest
  UNION ALL
  SELECT m.id,m.origin_manifest_id,m.use_state,c.path||m.id,c.depth+1,m.id=ANY(c.path)
  FROM chain c JOIN question_reference_manifest m ON m.tenant_id=p_tenant AND m.id=c.origin_manifest_id
  WHERE NOT c.cycle AND c.depth<128
 ) SELECT COALESCE(bool_and(use_state='active' AND NOT cycle AND (depth<128 OR origin_manifest_id IS NULL)),false) FROM chain
$$;

CREATE TABLE question_bank_reference_grant (
 tenant_id UUID NOT NULL REFERENCES tenant(id),
 version_id UUID NOT NULL,
 bank_id UUID NOT NULL,
 source_manifest_id UUID NOT NULL,
 source_exam_id UUID NOT NULL,
 source_question_id UUID NOT NULL,
 reference_materials JSONB NOT NULL CHECK((jsonb_typeof(reference_materials)='object' AND reference_materials ?& ARRAY['source_exam_id','source_question_id','manifest_id','mode','revision','content_hash','image_count','items'] AND reference_materials->>'mode'='reference_images'
  AND jsonb_typeof(reference_materials->'source_exam_id')='string' AND jsonb_typeof(reference_materials->'source_question_id')='string' AND jsonb_typeof(reference_materials->'manifest_id')='string'
  AND jsonb_typeof(reference_materials->'content_hash')='string' AND reference_materials->>'content_hash' ~ '^[a-f0-9]{64}$'
  AND jsonb_typeof(reference_materials->'revision')='number' AND jsonb_typeof(reference_materials->'image_count')='number'
  AND jsonb_typeof(reference_materials->'items')='array' AND jsonb_array_length(reference_materials->'items') BETWEEN 1 AND 4) IS TRUE),
 origin_import_id UUID NOT NULL,
 source_version_id UUID,
 purpose TEXT NOT NULL DEFAULT 'question_bank_version_reference' CHECK(purpose='question_bank_version_reference'),
 authorized_by UUID NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,version_id),
 FOREIGN KEY(tenant_id,version_id) REFERENCES question_bank_item_version(tenant_id,id),
 FOREIGN KEY(tenant_id,bank_id) REFERENCES question_bank(tenant_id,id),
 FOREIGN KEY(tenant_id,source_manifest_id) REFERENCES question_reference_manifest(tenant_id,id),
 FOREIGN KEY(tenant_id,source_exam_id,source_question_id) REFERENCES question(tenant_id,exam_id,id),
 FOREIGN KEY(tenant_id,origin_import_id) REFERENCES question_bank_import(tenant_id,id),
 FOREIGN KEY(tenant_id,source_version_id) REFERENCES question_bank_item_version(tenant_id,id),
 FOREIGN KEY(tenant_id,authorized_by) REFERENCES app_user(tenant_id,id)
);
ALTER TABLE question_bank_reference_grant ENABLE ROW LEVEL SECURITY;
ALTER TABLE question_bank_reference_grant FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON question_bank_reference_grant USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT ON question_bank_reference_grant TO edugrade_tenant_runtime;
REVOKE UPDATE,DELETE ON question_bank_reference_grant FROM edugrade_tenant_runtime;

CREATE FUNCTION validate_bank_reference_grant() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE target RECORD; source_manifest question_reference_manifest; parent RECORD;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'bank reference grants are immutable' USING ERRCODE='23514'; END IF;
 SELECT v.item_id,v.scoring,i.bank_id INTO target FROM question_bank_item_version v JOIN question_bank_item i ON i.tenant_id=v.tenant_id AND i.id=v.item_id WHERE v.tenant_id=NEW.tenant_id AND v.id=NEW.version_id;
 IF NOT FOUND OR target.bank_id<>NEW.bank_id OR target.scoring->'reference_materials' IS DISTINCT FROM NEW.reference_materials THEN
  RAISE EXCEPTION 'bank reference grant does not match target version' USING ERRCODE='23514';
 END IF;
 SELECT * INTO source_manifest FROM question_reference_manifest WHERE tenant_id=NEW.tenant_id AND id=NEW.source_manifest_id FOR SHARE;
 IF NOT FOUND OR source_manifest.exam_id<>NEW.source_exam_id OR source_manifest.question_id<>NEW.source_question_id
 OR source_manifest.mode<>'reference_images' OR NOT reference_authorization_active(NEW.tenant_id,source_manifest.id)
 OR NEW.reference_materials->>'manifest_id'<>source_manifest.id::text
 OR NEW.reference_materials->>'source_exam_id'<>source_manifest.exam_id::text
 OR NEW.reference_materials->>'source_question_id'<>source_manifest.question_id::text
 OR NEW.reference_materials->>'content_hash'<>source_manifest.content_hash
 OR (NEW.reference_materials->>'revision')::int<>source_manifest.revision
 OR (NEW.reference_materials->>'image_count')::int<>(SELECT count(*) FROM question_reference_manifest_item WHERE tenant_id=NEW.tenant_id AND manifest_id=source_manifest.id) THEN
  RAISE EXCEPTION 'bank reference source is not the authorized frozen manifest' USING ERRCODE='23514';
 END IF;
 IF NEW.source_version_id IS NULL THEN
  IF NOT EXISTS(SELECT 1 FROM question_bank_import i WHERE i.tenant_id=NEW.tenant_id AND i.id=NEW.origin_import_id AND i.target_bank_id=NEW.bank_id AND i.target_version_id=NEW.version_id AND i.source_exam_id=NEW.source_exam_id AND i.source_question_id=NEW.source_question_id AND i.created_by=NEW.authorized_by) THEN
   RAISE EXCEPTION 'bank reference requires its accepted import command' USING ERRCODE='23514';
  END IF;
 ELSE
  SELECT g.*,v.item_id INTO parent FROM question_bank_reference_grant g JOIN question_bank_item_version v ON v.tenant_id=g.tenant_id AND v.id=g.version_id WHERE g.tenant_id=NEW.tenant_id AND g.version_id=NEW.source_version_id;
  IF NOT FOUND OR parent.item_id<>target.item_id OR parent.bank_id<>NEW.bank_id OR parent.origin_import_id<>NEW.origin_import_id OR parent.reference_materials IS DISTINCT FROM NEW.reference_materials THEN
   RAISE EXCEPTION 'bank reference cannot be delegated to another item' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_bank_reference_grant BEFORE INSERT OR UPDATE OR DELETE ON question_bank_reference_grant FOR EACH ROW EXECUTE FUNCTION validate_bank_reference_grant();

CREATE FUNCTION require_bank_version_reference_grant() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.scoring->'reference_materials' IS DISTINCT FROM OLD.scoring->'reference_materials' THEN
  RAISE EXCEPTION 'imported reference binding cannot be edited by scoring requests' USING ERRCODE='23514';
 END IF;
 IF NEW.scoring->'reference_materials' IS NOT NULL AND NEW.scoring->'reference_materials'<>'null'::jsonb
 AND NOT EXISTS(SELECT 1 FROM question_bank_reference_grant WHERE tenant_id=NEW.tenant_id AND version_id=NEW.id AND reference_materials=NEW.scoring->'reference_materials') THEN
  RAISE EXCEPTION 'bank reference requires a version-scoped grant' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_bank_version_reference_grant AFTER INSERT OR UPDATE ON question_bank_item_version DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_bank_version_reference_grant();

CREATE FUNCTION bind_reference_manifest_origin() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE parent question_reference_manifest;
BEGIN
 IF NEW.origin_manifest_id IS NULL THEN RETURN NEW; END IF;
 SELECT * INTO parent FROM question_reference_manifest WHERE tenant_id=NEW.tenant_id AND id=NEW.origin_manifest_id FOR SHARE;
 IF NOT FOUND OR parent.id=NEW.id OR NOT reference_authorization_active(NEW.tenant_id,parent.id) THEN
  RAISE EXCEPTION 'reference parent authorization is unavailable' USING ERRCODE='23514';
 END IF;
 IF (parent.exam_id,parent.question_id) IS DISTINCT FROM (NEW.exam_id,NEW.question_id)
 AND NOT EXISTS(SELECT 1 FROM question_bank_reference_grant g JOIN question_bank_item_version v ON v.tenant_id=g.tenant_id AND v.id=g.version_id JOIN question_bank_item i ON i.tenant_id=v.tenant_id AND i.id=v.item_id JOIN question_bank b ON b.tenant_id=i.tenant_id AND b.id=i.bank_id
 WHERE g.tenant_id=NEW.tenant_id AND g.version_id::text=NEW.exam_use_grant_json->>'bank_version_id' AND g.source_manifest_id=parent.id AND v.workflow_status='published' AND i.status='active' AND b.status='active') THEN
  RAISE EXCEPTION 'cross-exam reference requires the exact bank version grant' USING ERRCODE='23514';
 END IF;
 IF NEW.exam_use_grant_json->>'parent_manifest_id' IS DISTINCT FROM parent.id::text THEN
  RAISE EXCEPTION 'reference parent does not match grant' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_reference_manifest_origin BEFORE INSERT ON question_reference_manifest FOR EACH ROW EXECUTE FUNCTION bind_reference_manifest_origin();
