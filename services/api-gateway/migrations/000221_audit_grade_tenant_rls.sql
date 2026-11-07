-- 后续成绩模型沿用既有租户上下文规则；存量行不改值，无上下文运行身份拒绝访问。
-- 身份认证表仍由登录前解析租户，不能套用这里的业务数据策略。
DO $$
DECLARE protected_table TEXT;
BEGIN
  FOREACH protected_table IN ARRAY ARRAY[
    'question_grade','submission_grade','score_release','score_release_item',
    'question_appeal','exam_candidate_snapshot','review_task','arbitration_task',
    'answer_candidate','ai_grade','subjective_grading_run'
  ] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', protected_table);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', protected_table);
    EXECUTE format('DROP POLICY IF EXISTS edugrade_tenant_isolation ON %I', protected_table);
    EXECUTE format('CREATE POLICY edugrade_tenant_isolation ON %I FOR ALL USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id))', protected_table);
  END LOOP;
END $$;

-- 旧标准图可能已经在校准中公开参考分，统一留在校准池；新图按内容哈希分池并永久登记。
ALTER TABLE grading_gold_paper ADD COLUMN quality_pool TEXT NOT NULL DEFAULT 'calibration'
  CHECK(quality_pool IN ('calibration','blind'));
CREATE TABLE gold_quality_image_pool (
  tenant_id UUID NOT NULL REFERENCES tenant(id),
  hash_sha256 TEXT NOT NULL CHECK(hash_sha256 ~ '^[0-9a-f]{64}$'),
  quality_pool TEXT NOT NULL CHECK(quality_pool IN ('calibration','blind')),
  PRIMARY KEY(tenant_id,hash_sha256)
);
INSERT INTO gold_quality_image_pool(tenant_id,hash_sha256,quality_pool)
SELECT DISTINCT tenant_id,frozen_crop_sha256,'calibration' FROM grading_gold_paper_version
WHERE frozen_crop_sha256 ~ '^[0-9a-f]{64}$';
ALTER TABLE gold_quality_image_pool ENABLE ROW LEVEL SECURITY;
ALTER TABLE gold_quality_image_pool FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON gold_quality_image_pool FOR ALL
USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
REVOKE UPDATE,DELETE ON gold_quality_image_pool FROM edugrade_tenant_runtime;

-- 旧盲投集合包含已曝光图像，暂停后须补齐新盲池并重新确认策略；旧资格也须按新抽样规则重新校准。
UPDATE seed_sampling_policy SET status='paused',revision=revision+1,updated_at=now() WHERE status='active';
UPDATE seed_task SET status='expired',revision=revision+1,updated_at=now() WHERE status='in_progress';
UPDATE grader_calibration_session SET status='invalidated',invalidated_at=now(),invalidation_reason='quality_pools_separated'
WHERE status='in_progress';
UPDATE grader_question_qualification SET status='revoked',updated_at=now() WHERE status='qualified';

-- 通用文件删除只接受无业务引用的临时文件。按数据库外键盘点引用，新增文件引用表无需再维护一份手写清单。
-- 巡检台账描述异常资产，不构成业务证据引用；它保留外键及租户校验，但不能永久阻止正常清理。
CREATE FUNCTION edugrade_file_asset_referenced(target_tenant UUID, target_asset UUID)
RETURNS BOOLEAN LANGUAGE plpgsql SET search_path = public AS $$
DECLARE reference RECORD; pinned BOOLEAN; aliases UUID[];
BEGIN
  SELECT array_agg(other.id) INTO aliases FROM file_asset original
  JOIN file_asset other ON other.tenant_id=original.tenant_id AND other.storage_bucket=original.storage_bucket AND other.storage_key=original.storage_key
  WHERE original.tenant_id=target_tenant AND original.id=target_asset;
  FOR reference IN
    SELECT ns.nspname AS schema_name,rel.relname AS table_name,local_att.attname AS column_name
    FROM pg_constraint con
    JOIN pg_class rel ON rel.oid=con.conrelid
    JOIN pg_namespace ns ON ns.oid=rel.relnamespace
    CROSS JOIN LATERAL unnest(con.conkey,con.confkey) AS pair(local_num,foreign_num)
    JOIN pg_attribute local_att ON local_att.attrelid=con.conrelid AND local_att.attnum=pair.local_num
    JOIN pg_attribute foreign_att ON foreign_att.attrelid=con.confrelid AND foreign_att.attnum=pair.foreign_num
    WHERE con.contype='f' AND con.confrelid='file_asset'::regclass AND foreign_att.attname='id'
      AND con.conrelid<>'file_reconciliation_finding'::regclass
  LOOP
    EXECUTE format('SELECT EXISTS(SELECT 1 FROM %I.%I WHERE %I = ANY($1))',reference.schema_name,reference.table_name,reference.column_name) INTO pinned USING aliases;
    IF pinned THEN RETURN true; END IF;
  END LOOP;
  RETURN false;
END $$;

CREATE FUNCTION edugrade_guard_file_asset_delete()
RETURNS TRIGGER LANGUAGE plpgsql SET search_path = public AS $$
BEGIN
  IF NEW.lifecycle_status IN ('pending_delete','deleted') AND OLD.lifecycle_status IS DISTINCT FROM NEW.lifecycle_status THEN
    PERFORM pg_advisory_xact_lock(hashtextextended(OLD.storage_bucket||':'||OLD.storage_key,81701));
    IF OLD.owner_type<>'generic' OR OLD.exam_id IS NOT NULL OR OLD.submission_id IS NOT NULL
      OR edugrade_file_asset_referenced(OLD.tenant_id,OLD.id) THEN
      RAISE EXCEPTION 'referenced answer or business assets cannot be deleted' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER guard_business_file_delete BEFORE UPDATE ON file_asset
FOR EACH ROW EXECUTE FUNCTION edugrade_guard_file_asset_delete();

-- 对象键锁覆盖同一对象的多个资产别名；KEY SHARE 固定当前资产状态，阻断删除与首次引用交错。
CREATE FUNCTION edugrade_guard_file_reference()
RETURNS TRIGGER LANGUAGE plpgsql SET search_path = public AS $$
DECLARE column_name TEXT; target UUID; asset_state TEXT; object_bucket TEXT; object_key TEXT;
BEGIN
  FOREACH column_name IN ARRAY TG_ARGV LOOP
    IF TG_OP='UPDATE' AND to_jsonb(NEW)->column_name IS NOT DISTINCT FROM to_jsonb(OLD)->column_name
      AND NEW.tenant_id IS NOT DISTINCT FROM OLD.tenant_id THEN CONTINUE; END IF;
    target := NULLIF(to_jsonb(NEW)->>column_name,'')::uuid;
    IF target IS NULL THEN CONTINUE; END IF;
    SELECT storage_bucket,storage_key INTO object_bucket,object_key FROM file_asset
    WHERE tenant_id=NEW.tenant_id AND id=target;
    IF NOT FOUND THEN RAISE EXCEPTION 'file reference requires active evidence' USING ERRCODE='23514'; END IF;
    -- 巡检必须能够记录未上传、隔离或待删状态；前面的身份检查仍拒绝跨租户伪造台账。
    IF TG_RELID='file_reconciliation_finding'::regclass THEN CONTINUE; END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(object_bucket||':'||object_key,81701));
    SELECT lifecycle_status INTO asset_state FROM file_asset
    WHERE tenant_id=NEW.tenant_id AND id=target AND deleted_at IS NULL FOR KEY SHARE;
    IF NOT FOUND OR asset_state NOT IN ('active','orphan_recovered') OR EXISTS(
      SELECT 1 FROM file_asset WHERE tenant_id=NEW.tenant_id AND storage_bucket=object_bucket AND storage_key=object_key
        AND (lifecycle_status IN ('pending_delete','delete_failed','deleted') OR deleted_at IS NOT NULL)
    ) THEN
      RAISE EXCEPTION 'file reference requires active evidence' USING ERRCODE='23514';
    END IF;
  END LOOP;
  RETURN NEW;
END $$;
DO $$
DECLARE reference RECORD;
BEGIN
  FOR reference IN
    SELECT ns.nspname AS schema_name,rel.relname AS table_name,
      string_agg(DISTINCT quote_literal(local_att.attname),',') AS column_args
    FROM pg_constraint con
    JOIN pg_class rel ON rel.oid=con.conrelid
    JOIN pg_namespace ns ON ns.oid=rel.relnamespace
    CROSS JOIN LATERAL unnest(con.conkey,con.confkey) AS pair(local_num,foreign_num)
    JOIN pg_attribute local_att ON local_att.attrelid=con.conrelid AND local_att.attnum=pair.local_num
    JOIN pg_attribute foreign_att ON foreign_att.attrelid=con.confrelid AND foreign_att.attnum=pair.foreign_num
    WHERE con.contype='f' AND con.confrelid='file_asset'::regclass AND foreign_att.attname='id'
    GROUP BY ns.nspname,rel.relname
  LOOP
    EXECUTE format('CREATE TRIGGER guard_active_file_reference BEFORE INSERT OR UPDATE ON %I.%I FOR EACH ROW EXECUTE FUNCTION edugrade_guard_file_reference(%s)',reference.schema_name,reference.table_name,reference.column_args);
  END LOOP;
END $$;
