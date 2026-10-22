-- 新运行从准确的确认快照绑定材料；旧运行保持 NULL，不补造历史发送事实。
ALTER TABLE subjective_grading_run ADD COLUMN reference_manifest_id UUID;
ALTER TABLE subjective_grading_run ADD COLUMN reference_manifest_hash TEXT;
ALTER TABLE subjective_grading_run ADD COLUMN reference_authorization_revision INTEGER;
ALTER TABLE subjective_grading_run ADD COLUMN material_input_profile TEXT;
ALTER TABLE subjective_grading_run ADD COLUMN math_wrapper_hash TEXT;
ALTER TABLE subjective_grading_run ADD FOREIGN KEY(tenant_id,reference_manifest_id) REFERENCES question_reference_manifest(tenant_id,id);
CREATE FUNCTION bind_subjective_reference_manifest() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE binding JSONB; material question_reference_manifest; target_exam UUID; snapshot_hash TEXT;
BEGIN
 SELECT exam_id INTO target_exam FROM question WHERE tenant_id=NEW.tenant_id AND id=NEW.question_id;
 IF NEW.exam_question_snapshot_id IS NULL THEN
  -- 旧文本流程仍允许没有快照；已确认材料的题目不能借空快照绕开冻结绑定。
  IF EXISTS(SELECT 1 FROM exam_readiness_snapshot r CROSS JOIN LATERAL jsonb_array_elements(r.import_snapshot_json->'questions') item
   WHERE r.tenant_id=NEW.tenant_id AND r.exam_id=target_exam AND r.status='passed' AND item->>'id'=NEW.question_id::text AND item->'reference_materials' IS NOT NULL AND item->'reference_materials'<>'null'::jsonb) THEN
   RAISE EXCEPTION 'reference run requires an exact assessment snapshot' USING ERRCODE='23514';
  END IF;
  IF NEW.reference_manifest_id IS NOT NULL OR NEW.reference_manifest_hash IS NOT NULL OR NEW.reference_authorization_revision IS NOT NULL OR NEW.material_input_profile IS NOT NULL OR NEW.math_wrapper_hash IS NOT NULL THEN
   RAISE EXCEPTION 'reference run binding must come from readiness' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 SELECT content_hash INTO snapshot_hash FROM exam_question_snapshot WHERE tenant_id=NEW.tenant_id AND id=NEW.exam_question_snapshot_id AND exam_id=target_exam AND question_id=NEW.question_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'assessment snapshot scope mismatch' USING ERRCODE='23514'; END IF;
 SELECT item->'reference_materials' INTO binding FROM exam_readiness_snapshot r
 CROSS JOIN LATERAL jsonb_array_elements(r.import_snapshot_json->'questions') item
 WHERE r.tenant_id=NEW.tenant_id AND r.exam_id=target_exam AND r.status='passed' AND item->>'id'=NEW.question_id::text
 AND item->>'assessment_snapshot_id'=NEW.exam_question_snapshot_id::text
 AND item->>'assessment_snapshot_hash'=snapshot_hash
 ORDER BY r.confirmed_at DESC,r.id DESC LIMIT 1;
 IF binding IS NULL OR binding='null'::jsonb THEN
  IF NEW.reference_manifest_id IS NOT NULL OR NEW.reference_manifest_hash IS NOT NULL OR NEW.reference_authorization_revision IS NOT NULL OR NEW.material_input_profile IS NOT NULL OR NEW.math_wrapper_hash IS NOT NULL THEN
   RAISE EXCEPTION 'reference run binding must come from readiness' USING ERRCODE='23514';
  END IF;
  -- 有材料 companion 却未匹配准确的哈希时，不能降级为无材料运行。
  IF EXISTS(SELECT 1 FROM exam_readiness_snapshot r CROSS JOIN LATERAL jsonb_array_elements(r.import_snapshot_json->'questions') item
   WHERE r.tenant_id=NEW.tenant_id AND r.exam_id=target_exam AND r.status='passed' AND item->>'id'=NEW.question_id::text AND item->'reference_materials' IS NOT NULL AND item->'reference_materials'<>'null'::jsonb) THEN
   RAISE EXCEPTION 'reference assessment snapshot hash mismatch' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 SELECT * INTO material FROM question_reference_manifest WHERE tenant_id=NEW.tenant_id AND id=(binding->>'manifest_id')::uuid FOR SHARE;
 IF NOT FOUND OR material.exam_id IS DISTINCT FROM target_exam OR material.question_id IS DISTINCT FROM NEW.question_id OR material.content_hash IS DISTINCT FROM binding->>'content_hash' OR material.revision::text IS DISTINCT FROM binding->>'revision' OR material.mode IS DISTINCT FROM binding->>'mode' OR material.use_state<>'active' OR
  (SELECT count(*)::text FROM question_reference_manifest_item WHERE tenant_id=NEW.tenant_id AND manifest_id=material.id) IS DISTINCT FROM binding->>'image_count' THEN
  RAISE EXCEPTION 'reference material is unavailable' USING ERRCODE='23514';
 END IF;
 IF NEW.math_wrapper_hash IS NOT NULL THEN RAISE EXCEPTION 'wrapper binding requires a prepared receipt' USING ERRCODE='23514'; END IF;
 NEW.reference_manifest_id=material.id;NEW.reference_manifest_hash=material.content_hash;
 NEW.reference_authorization_revision=material.authorization_revision;
 NEW.material_input_profile=CASE WHEN material.mode='reference_images' THEN 'material-input-v1' ELSE 'text-only-v1' END;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_bind_subjective_reference_manifest BEFORE INSERT ON subjective_grading_run FOR EACH ROW EXECUTE FUNCTION bind_subjective_reference_manifest();

CREATE TABLE subjective_request_material_receipt (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id UUID NOT NULL REFERENCES tenant(id),run_id UUID NOT NULL,
 execution_attempt_id UUID NOT NULL,provider_attempt_no INTEGER NOT NULL DEFAULT 1 CHECK(provider_attempt_no=1),
 stage TEXT NOT NULL CHECK(stage IN ('prepared','provider_request_attempted','provider_response_received','failed_before_send','unknown_after_attempt')),
 request_id TEXT NOT NULL,request_digest TEXT NOT NULL CHECK(request_digest ~ '^[a-f0-9]{64}$'),manifest_id UUID NOT NULL,manifest_hash TEXT NOT NULL CHECK(manifest_hash ~ '^[a-f0-9]{64}$'),
 authorization_revision INTEGER NOT NULL CHECK(authorization_revision>0),wrapper_hash TEXT NOT NULL CHECK(wrapper_hash ~ '^[a-f0-9]{64}$'),
 ordered_media_json JSONB NOT NULL CHECK(jsonb_typeof(ordered_media_json)='array'),wire_payload_hash TEXT CHECK(wire_payload_hash ~ '^[a-f0-9]{64}$'),usage_json JSONB CHECK(jsonb_typeof(usage_json)='object'),usage_state TEXT NOT NULL DEFAULT 'unavailable' CHECK(usage_state IN ('unavailable','partial','reported')),
 credential_hash TEXT NOT NULL CHECK(credential_hash ~ '^[a-f0-9]{64}$'),consumed_at TIMESTAMPTZ,expires_at TIMESTAMPTZ NOT NULL,recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK((consumed_at IS NULL AND stage='prepared' AND wire_payload_hash IS NULL) OR (consumed_at IS NOT NULL AND wire_payload_hash IS NOT NULL)),
 UNIQUE(tenant_id,id),UNIQUE(tenant_id,run_id,execution_attempt_id,provider_attempt_no,stage),
 FOREIGN KEY(tenant_id,run_id) REFERENCES subjective_grading_run(tenant_id,id),
 FOREIGN KEY(tenant_id,manifest_id) REFERENCES question_reference_manifest(tenant_id,id)
);
CREATE UNIQUE INDEX idx_material_single_execution ON subjective_request_material_receipt(tenant_id,run_id) WHERE stage='prepared';
CREATE INDEX ON subjective_request_material_receipt(tenant_id,run_id,recorded_at);
ALTER TABLE subjective_request_material_receipt ENABLE ROW LEVEL SECURITY;
ALTER TABLE subjective_request_material_receipt FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subjective_request_material_receipt USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT,UPDATE ON subjective_request_material_receipt TO edugrade_tenant_runtime;
REVOKE DELETE ON subjective_request_material_receipt FROM edugrade_tenant_runtime;

-- 收据只追加；prepared 唯一可变部分是一次性消费时刻和同事务冻结的线上请求摘要。
CREATE FUNCTION protect_subjective_material_receipt() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE prepared subjective_request_material_receipt; run subjective_grading_run; expected_media JSONB;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'material receipt must be retained' USING ERRCODE='23514'; END IF;
 IF TG_OP='UPDATE' THEN
  IF OLD.stage<>'prepared' OR OLD.consumed_at IS NOT NULL OR OLD.expires_at<=now() OR NEW.consumed_at IS NULL OR NEW.wire_payload_hash IS NULL OR
   (to_jsonb(NEW)-ARRAY['consumed_at','wire_payload_hash']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['consumed_at','wire_payload_hash']) THEN
   RAISE EXCEPTION 'material receipt is append only' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.stage='prepared' THEN
  SELECT * INTO run FROM subjective_grading_run WHERE tenant_id=NEW.tenant_id AND id=NEW.run_id;
  IF NOT FOUND OR run.status<>'processing' OR run.request_id IS DISTINCT FROM NEW.request_id OR run.reference_manifest_id IS DISTINCT FROM NEW.manifest_id OR run.reference_manifest_hash IS DISTINCT FROM NEW.manifest_hash OR run.reference_authorization_revision IS DISTINCT FROM NEW.authorization_revision OR run.material_input_profile IS DISTINCT FROM 'material-input-v1' OR NEW.consumed_at IS NOT NULL OR NEW.wire_payload_hash IS NOT NULL THEN
   RAISE EXCEPTION 'material receipt run binding mismatch' USING ERRCODE='23514';
  END IF;
  SELECT COALESCE(jsonb_agg(jsonb_build_object('media_id',i.id::text,'role',i.role,'ordinal',i.ordinal,'sha256',i.payload->'sha256','width_pixels',i.payload->'width','height_pixels',i.payload->'height','byte_size',i.payload->'size_bytes') ORDER BY i.ordinal),'[]'::jsonb) INTO expected_media FROM question_reference_manifest_item i WHERE i.tenant_id=NEW.tenant_id AND i.manifest_id=NEW.manifest_id;
  IF jsonb_array_length(expected_media) NOT BETWEEN 1 AND 4 OR NEW.ordered_media_json IS DISTINCT FROM expected_media OR (run.math_wrapper_hash IS NOT NULL AND run.math_wrapper_hash IS DISTINCT FROM NEW.wrapper_hash) THEN
   RAISE EXCEPTION 'material receipt ordered media mismatch' USING ERRCODE='23514';
  END IF;
 ELSE
  SELECT * INTO prepared FROM subjective_request_material_receipt WHERE tenant_id=NEW.tenant_id AND run_id=NEW.run_id AND stage='prepared';
  IF NOT FOUND OR prepared.consumed_at IS NULL OR
   (NEW.execution_attempt_id,NEW.provider_attempt_no,NEW.request_id,NEW.request_digest,NEW.manifest_id,NEW.manifest_hash,NEW.authorization_revision,NEW.wrapper_hash,NEW.ordered_media_json,NEW.credential_hash,NEW.expires_at,NEW.consumed_at,NEW.wire_payload_hash)
   IS DISTINCT FROM
   (prepared.execution_attempt_id,prepared.provider_attempt_no,prepared.request_id,prepared.request_digest,prepared.manifest_id,prepared.manifest_hash,prepared.authorization_revision,prepared.wrapper_hash,prepared.ordered_media_json,prepared.credential_hash,prepared.expires_at,prepared.consumed_at,prepared.wire_payload_hash) THEN
   RAISE EXCEPTION 'material receipt execution binding mismatch' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_material_receipt_append_only BEFORE INSERT OR UPDATE OR DELETE ON subjective_request_material_receipt FOR EACH ROW EXECUTE FUNCTION protect_subjective_material_receipt();

-- 撤回之后的建议不得结算；先锁 run/runtime，再在这里短锁材料和所有资产。
CREATE FUNCTION validate_subjective_reference_settlement() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE material question_reference_manifest; asset RECORD;
BEGIN
 IF NEW.reference_manifest_id IS DISTINCT FROM OLD.reference_manifest_id OR NEW.reference_manifest_hash IS DISTINCT FROM OLD.reference_manifest_hash OR NEW.reference_authorization_revision IS DISTINCT FROM OLD.reference_authorization_revision OR NEW.material_input_profile IS DISTINCT FROM OLD.material_input_profile OR
  (OLD.reference_manifest_id IS NOT NULL AND (NEW.tenant_id,NEW.question_id,NEW.answer_segment_id,NEW.exam_question_snapshot_id,NEW.request_id) IS DISTINCT FROM (OLD.tenant_id,OLD.question_id,OLD.answer_segment_id,OLD.exam_question_snapshot_id,OLD.request_id)) OR
  (OLD.math_wrapper_hash IS NOT NULL AND NEW.math_wrapper_hash IS DISTINCT FROM OLD.math_wrapper_hash) OR
  (OLD.math_wrapper_hash IS NULL AND NEW.math_wrapper_hash IS NOT NULL AND (OLD.status<>'processing' OR NOT EXISTS(SELECT 1 FROM subjective_request_material_receipt p WHERE p.tenant_id=NEW.tenant_id AND p.run_id=NEW.id AND p.stage='prepared' AND p.wrapper_hash=NEW.math_wrapper_hash))) THEN
  RAISE EXCEPTION 'reference run binding is immutable' USING ERRCODE='23514';
 END IF;
 IF NEW.status='succeeded' AND OLD.status<>'succeeded' AND NEW.reference_manifest_id IS NOT NULL THEN
  SELECT * INTO material FROM question_reference_manifest WHERE tenant_id=NEW.tenant_id AND id=NEW.reference_manifest_id FOR SHARE;
  IF NOT FOUND OR material.use_state<>'active' OR material.authorization_revision<>NEW.reference_authorization_revision OR material.content_hash<>NEW.reference_manifest_hash THEN
   RAISE EXCEPTION 'reference authorization changed' USING ERRCODE='23514';
  END IF;
  FOR asset IN SELECT f.* FROM file_asset f WHERE f.tenant_id=NEW.tenant_id AND f.id IN (SELECT unnest(ARRAY[i.source_file_asset_id,i.page_file_asset_id,i.derived_file_asset_id]) FROM question_reference_manifest_item i WHERE i.tenant_id=NEW.tenant_id AND i.manifest_id=NEW.reference_manifest_id) ORDER BY f.id FOR SHARE LOOP
   IF asset.lifecycle_status<>'active' OR asset.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'reference asset unavailable' USING ERRCODE='23514'; END IF;
  END LOOP;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_reference_settlement BEFORE UPDATE ON subjective_grading_run FOR EACH ROW EXECUTE FUNCTION validate_subjective_reference_settlement();
