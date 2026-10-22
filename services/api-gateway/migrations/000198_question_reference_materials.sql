-- 参考材料只追加版本，历史未记录保持 NULL；所有文件引用按租户约束。
-- 裁图元数据由服务端写入，不进入通用上传请求；冻结清单固定准确版本。
ALTER TABLE file_asset ADD COLUMN reference_crop_json JSONB;
CREATE TABLE question_reference_manifest (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id UUID NOT NULL REFERENCES tenant(id),
 exam_id UUID NOT NULL REFERENCES exam(id),
 question_id UUID NOT NULL REFERENCES question(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 previous_manifest_id UUID,
 schema_version INTEGER NOT NULL DEFAULT 1 CHECK(schema_version=1),
 mode TEXT NOT NULL CHECK(mode IN ('text_only','reference_images')),
 content_hash TEXT NOT NULL CHECK(content_hash ~ '^[a-f0-9]{64}$'),
 command_id TEXT NOT NULL CHECK(length(command_id) BETWEEN 8 AND 128),
 request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
 created_by UUID NOT NULL REFERENCES app_user(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 creation_xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
 use_state TEXT NOT NULL DEFAULT 'active' CHECK(use_state IN ('active','withdrawn')),
 authorization_revision INTEGER NOT NULL DEFAULT 1 CHECK(authorization_revision>0),
 exam_use_grant_json JSONB NOT NULL DEFAULT '{}',
 withdrawn_at TIMESTAMPTZ,
 withdrawal_reason TEXT,
 UNIQUE(tenant_id,id),UNIQUE(tenant_id,question_id,revision),UNIQUE(tenant_id,created_by,command_id),
 FOREIGN KEY(tenant_id,previous_manifest_id) REFERENCES question_reference_manifest(tenant_id,id)
);
CREATE TABLE question_reference_manifest_item (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id UUID NOT NULL REFERENCES tenant(id),
 manifest_id UUID NOT NULL,
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 1 AND 4),
 role TEXT NOT NULL CHECK(role IN ('question_diagram','reference_solution')),
 required BOOLEAN NOT NULL DEFAULT true CHECK(required),
 source_file_asset_id UUID NOT NULL,
 page_file_asset_id UUID NOT NULL,
 derived_file_asset_id UUID NOT NULL,
 payload JSONB NOT NULL CHECK(jsonb_typeof(payload)='object'),
 UNIQUE(tenant_id,id),UNIQUE(tenant_id,manifest_id,ordinal),UNIQUE(tenant_id,manifest_id,derived_file_asset_id),
 FOREIGN KEY(tenant_id,manifest_id) REFERENCES question_reference_manifest(tenant_id,id),
 FOREIGN KEY(tenant_id,source_file_asset_id) REFERENCES file_asset(tenant_id,id),
 FOREIGN KEY(tenant_id,page_file_asset_id) REFERENCES file_asset(tenant_id,id),
 FOREIGN KEY(tenant_id,derived_file_asset_id) REFERENCES file_asset(tenant_id,id)
);
ALTER TABLE question_reference_manifest ADD FOREIGN KEY(tenant_id,exam_id) REFERENCES exam(tenant_id,id);
ALTER TABLE question_reference_manifest ADD FOREIGN KEY(tenant_id,question_id) REFERENCES question(tenant_id,id);
ALTER TABLE question ADD CONSTRAINT uq_reference_question_exam UNIQUE(tenant_id,exam_id,id);
ALTER TABLE question_reference_manifest ADD FOREIGN KEY(tenant_id,exam_id,question_id) REFERENCES question(tenant_id,exam_id,id);
CREATE INDEX ON question_reference_manifest(tenant_id,exam_id,question_id,revision DESC);
CREATE INDEX ON question_reference_manifest_item(tenant_id,source_file_asset_id);
CREATE INDEX ON question_reference_manifest_item(tenant_id,page_file_asset_id);
CREATE INDEX ON question_reference_manifest_item(tenant_id,derived_file_asset_id);
ALTER TABLE question_reference_manifest ENABLE ROW LEVEL SECURITY;
ALTER TABLE question_reference_manifest FORCE ROW LEVEL SECURITY;
ALTER TABLE question_reference_manifest_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE question_reference_manifest_item FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON question_reference_manifest USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
CREATE POLICY tenant_isolation ON question_reference_manifest_item USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT,UPDATE ON question_reference_manifest TO edugrade_tenant_runtime;
GRANT SELECT,INSERT ON question_reference_manifest_item TO edugrade_tenant_runtime;
REVOKE DELETE ON question_reference_manifest,question_reference_manifest_item FROM edugrade_tenant_runtime;

CREATE FUNCTION bind_reference_manifest_creation() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE exam_status TEXT;
BEGIN
 SELECT status INTO exam_status FROM exam WHERE tenant_id=NEW.tenant_id AND id=NEW.exam_id FOR SHARE;
 IF NOT FOUND OR exam_status NOT IN('draft','configured') THEN
  RAISE EXCEPTION 'reference manifest requires a mutable exam' USING ERRCODE='23514';
 END IF;
 -- 不信任显式传入的 xid；它只标记本次创建事务，不能预填后续事务的身份。
 NEW.creation_xid=pg_current_xact_id();
 RETURN NEW;
END $$;
CREATE TRIGGER trg_reference_manifest_creation BEFORE INSERT ON question_reference_manifest FOR EACH ROW EXECUTE FUNCTION bind_reference_manifest_creation();

CREATE FUNCTION protect_question_reference_manifest() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR (to_jsonb(NEW)-ARRAY['use_state','authorization_revision','withdrawn_at','withdrawal_reason']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['use_state','authorization_revision','withdrawn_at','withdrawal_reason']) THEN
  RAISE EXCEPTION 'reference manifest content is immutable' USING ERRCODE='23514';
 END IF;
 IF OLD.use_state<>'active' OR NEW.use_state<>'withdrawn' OR NEW.authorization_revision<>OLD.authorization_revision+1 OR NEW.withdrawn_at IS NULL OR length(trim(NEW.withdrawal_reason))=0 THEN
  RAISE EXCEPTION 'invalid reference authorization transition' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_reference_manifest_immutable BEFORE UPDATE OR DELETE ON question_reference_manifest FOR EACH ROW EXECUTE FUNCTION protect_question_reference_manifest();
INSERT INTO exam_archive_guarded_table VALUES('question_reference_manifest'::regclass);
CREATE TRIGGER trg_exam_archive_readonly BEFORE INSERT OR UPDATE OR DELETE ON question_reference_manifest FOR EACH ROW EXECUTE FUNCTION reject_archived_exam_business_mutation();

-- FK 仅保护登记行；软删除及相同对象键的另一资产也必须阻断，隔离仍允许。
CREATE FUNCTION protect_reference_material_object() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended(OLD.storage_bucket||':'||OLD.storage_key,81701));
 IF (TG_OP='DELETE' OR NEW.lifecycle_status IN ('pending_delete','delete_failed','deleted') OR NEW.deleted_at IS NOT NULL OR NEW.storage_bucket IS DISTINCT FROM OLD.storage_bucket OR NEW.storage_key IS DISTINCT FROM OLD.storage_key OR NEW.hash_sha256 IS DISTINCT FROM OLD.hash_sha256)
 AND EXISTS(SELECT 1 FROM question_reference_manifest_item i JOIN file_asset f ON f.tenant_id=i.tenant_id AND f.id IN (i.source_file_asset_id,i.page_file_asset_id,i.derived_file_asset_id) WHERE f.storage_bucket=OLD.storage_bucket AND f.storage_key=OLD.storage_key) THEN
  RAISE EXCEPTION 'file object is pinned by a reference manifest' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;
CREATE TRIGGER trg_reference_material_object_pin BEFORE UPDATE OR DELETE ON file_asset FOR EACH ROW EXECUTE FUNCTION protect_reference_material_object();

CREATE FUNCTION validate_reference_material_item() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE asset RECORD; manifest question_reference_manifest;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'reference material items are immutable' USING ERRCODE='23514'; END IF;
 SELECT * INTO manifest FROM question_reference_manifest WHERE tenant_id=NEW.tenant_id AND id=NEW.manifest_id;
 -- 清单及全部图片必须同一事务创建，提交后不能通过追加 item 改变旧清单内容。
 IF NOT FOUND OR manifest.creation_xid<>pg_current_xact_id() OR manifest.mode<>'reference_images' THEN
  RAISE EXCEPTION 'reference manifest items are sealed' USING ERRCODE='23514';
 END IF;
 IF (SELECT count(*) FROM question_reference_manifest_item WHERE tenant_id=NEW.tenant_id AND manifest_id=NEW.manifest_id AND role=NEW.role)>=2
 OR EXISTS(SELECT 1 FROM question_reference_manifest_item i WHERE i.tenant_id=NEW.tenant_id AND i.manifest_id=NEW.manifest_id AND
  ((NEW.role='question_diagram' AND i.role='reference_solution' AND i.ordinal<NEW.ordinal) OR (NEW.role='reference_solution' AND i.role='question_diagram' AND i.ordinal>NEW.ordinal))) THEN
  RAISE EXCEPTION 'invalid reference role count or order' USING ERRCODE='23514';
 END IF;
 -- 对象键锁同时覆盖多个资产共享同一对象的情况，防止别名清理与首次绑定交错。
 FOR asset IN SELECT * FROM file_asset WHERE tenant_id=NEW.tenant_id AND id IN(NEW.source_file_asset_id,NEW.page_file_asset_id,NEW.derived_file_asset_id) ORDER BY storage_bucket,storage_key LOOP
  PERFORM pg_advisory_xact_lock(hashtextextended(asset.storage_bucket||':'||asset.storage_key,81701));
  IF asset.lifecycle_status<>'active' OR asset.deleted_at IS NOT NULL OR EXISTS(SELECT 1 FROM file_asset f WHERE f.storage_bucket=asset.storage_bucket AND f.storage_key=asset.storage_key AND f.lifecycle_status IN('pending_delete','delete_failed','deleted')) THEN
   RAISE EXCEPTION 'reference object is unavailable or being removed' USING ERRCODE='23514';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_reference_item_immutable BEFORE INSERT OR UPDATE OR DELETE ON question_reference_manifest_item FOR EACH ROW EXECUTE FUNCTION validate_reference_material_item();

ALTER TABLE exam_readiness_snapshot DROP CONSTRAINT exam_readiness_snapshot_import_shape;
ALTER TABLE exam_readiness_snapshot ADD CONSTRAINT exam_readiness_snapshot_import_shape CHECK(
 (import_snapshot_schema_version IS NULL AND import_snapshot_hash IS NULL AND import_snapshot_json IS NULL)
 OR (import_snapshot_schema_version IN(1,2) AND import_snapshot_hash ~ '^[0-9a-f]{64}$' AND jsonb_typeof(import_snapshot_json)='object' AND jsonb_typeof(import_snapshot_json->'questions')='array')
);
