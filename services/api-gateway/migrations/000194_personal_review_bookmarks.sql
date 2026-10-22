-- 个人复查元数据独立于阅卷状态。取消保留墓碑与版本，旧请求不能重新创建已取消的记录。
CREATE TABLE review_bookmark (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenant(id),
  owner_id UUID NOT NULL,
  review_task_id UUID,
  seed_task_id UUID,
  task_id UUID GENERATED ALWAYS AS (COALESCE(review_task_id,seed_task_id)) STORED,
  reason TEXT NOT NULL DEFAULT '',
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  marked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ,
  observed_crop_sha256 TEXT NOT NULL DEFAULT '',
  observed_snapshot_id TEXT NOT NULL DEFAULT '',
  UNIQUE (tenant_id,id),
  UNIQUE (tenant_id,owner_id,task_id),
  FOREIGN KEY (tenant_id,owner_id) REFERENCES app_user(tenant_id,id),
  FOREIGN KEY (tenant_id,review_task_id) REFERENCES review_task(tenant_id,id),
  FOREIGN KEY (tenant_id,seed_task_id) REFERENCES seed_task(tenant_id,id),
  CHECK ((review_task_id IS NULL) <> (seed_task_id IS NULL)),
  CHECK (char_length(reason) <= 200),
  CHECK (deleted_at IS NULL OR reason='')
);
CREATE INDEX idx_review_bookmark_personal_list ON review_bookmark(tenant_id,owner_id,marked_at DESC,id DESC) WHERE deleted_at IS NULL;
ALTER TABLE review_bookmark ENABLE ROW LEVEL SECURITY;
ALTER TABLE review_bookmark FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON review_bookmark
USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT,UPDATE ON review_bookmark TO edugrade_tenant_runtime;
REVOKE DELETE ON review_bookmark FROM edugrade_tenant_runtime;

-- 通用归档触发器会连个人取消也拒绝；此表只用专门触发器，例外不得夹带任何其他字段。
CREATE FUNCTION protect_review_bookmark_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE target_exam UUID;
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'review_bookmark_hard_delete_forbidden' USING ERRCODE='55000';
  END IF;
  IF TG_OP='UPDATE' THEN
    -- 归档后仍允许本人清理；只放行同一行的墓碑转换，避免借此修改来源或版本事实。
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL AND NEW.reason=''
      AND NEW.revision=OLD.revision+1
      AND NEW.id=OLD.id AND NEW.tenant_id=OLD.tenant_id AND NEW.owner_id=OLD.owner_id
      AND NEW.review_task_id IS NOT DISTINCT FROM OLD.review_task_id
      AND NEW.seed_task_id IS NOT DISTINCT FROM OLD.seed_task_id
      AND NEW.created_at=OLD.created_at AND NEW.marked_at=OLD.marked_at
      AND NEW.observed_crop_sha256=OLD.observed_crop_sha256
      AND NEW.observed_snapshot_id=OLD.observed_snapshot_id THEN
      RETURN NEW;
    END IF;
    IF ROW(NEW.id,NEW.tenant_id,NEW.owner_id,NEW.review_task_id,NEW.seed_task_id,NEW.created_at)
      IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.owner_id,OLD.review_task_id,OLD.seed_task_id,OLD.created_at) THEN
      RAISE EXCEPTION 'review_bookmark_identity_immutable' USING ERRCODE='55000';
    END IF;
  END IF;
  IF NEW.review_task_id IS NOT NULL THEN
    SELECT exam_id INTO target_exam FROM review_task WHERE tenant_id=NEW.tenant_id AND id=NEW.review_task_id;
  ELSE
    SELECT exam_id INTO target_exam FROM seed_task WHERE tenant_id=NEW.tenant_id AND id=NEW.seed_task_id;
  END IF;
  PERFORM exam_archive_require_mutable(NEW.tenant_id,target_exam);
  RETURN NEW;
END $$;
CREATE TRIGGER trg_review_bookmark_mutation BEFORE INSERT OR UPDATE OR DELETE ON review_bookmark
FOR EACH ROW EXECUTE FUNCTION protect_review_bookmark_mutation();
