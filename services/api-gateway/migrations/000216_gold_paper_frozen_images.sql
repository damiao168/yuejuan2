-- 标准卷从提名事务开始冻结裁图身份；历史当前裁图不能证明过去审批时的图片，不能用它补填冻结字段。
ALTER TABLE grading_gold_paper_version
  ADD COLUMN frozen_answer_segment_id UUID,
  ADD COLUMN frozen_crop_file_asset_id UUID,
  ADD COLUMN frozen_crop_sha256 TEXT,
  ADD COLUMN frozen_crop_owner_type TEXT,
  ADD COLUMN frozen_crop_owner_id UUID,
  ADD COLUMN image_invalidated_at TIMESTAMPTZ,
  ADD CONSTRAINT gold_frozen_segment_fk FOREIGN KEY (tenant_id, frozen_answer_segment_id) REFERENCES answer_segment(tenant_id, id),
  ADD CONSTRAINT gold_frozen_asset_fk FOREIGN KEY (tenant_id, frozen_crop_file_asset_id) REFERENCES file_asset(tenant_id, id),
  ADD CONSTRAINT gold_frozen_image_complete CHECK (
    (frozen_answer_segment_id IS NULL AND frozen_crop_file_asset_id IS NULL AND frozen_crop_sha256 IS NULL AND frozen_crop_owner_type IS NULL AND frozen_crop_owner_id IS NULL)
    OR (frozen_answer_segment_id IS NOT NULL AND frozen_crop_file_asset_id IS NOT NULL AND frozen_crop_sha256 IS NOT NULL AND frozen_crop_sha256 ~ '^[0-9a-f]{64}$'
      AND frozen_crop_owner_type IS NOT NULL AND frozen_crop_owner_type IN ('answer_segment_crop','page_registration_correction_preview') AND frozen_crop_owner_id IS NOT NULL));

-- 保留历史 approved_by/approved_at 和版本内容，只撤回主记录的当前可用资格；既有触发器同时撤销阅卷资格。
UPDATE grading_gold_paper SET active_version=NULL,status='pending_approval',updated_at=now()
WHERE status='active';
UPDATE grader_question_qualification SET status='revoked',updated_at=now() WHERE status='qualified';
UPDATE grader_calibration_session SET status='invalidated',invalidated_at=now(),invalidation_reason='gold_image_identity_not_frozen'
WHERE status='in_progress';
UPDATE seed_task SET status='expired',revision=revision+1,updated_at=now() WHERE status='in_progress';

-- 无可靠图片身份的未审批提名保留为历史，不再占用新提名槽，也不能补一次审批后直接启用。
UPDATE grading_gold_paper_version SET image_invalidated_at=now() WHERE approved_at IS NULL;
DROP INDEX uq_grading_gold_paper_pending_version;
CREATE UNIQUE INDEX uq_grading_gold_paper_pending_version ON grading_gold_paper_version(tenant_id,gold_paper_id)
WHERE approved_at IS NULL AND image_invalidated_at IS NULL;

CREATE FUNCTION validate_gold_frozen_image() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='INSERT' AND (NEW.frozen_crop_file_asset_id IS NULL OR NEW.image_invalidated_at IS NOT NULL) THEN
    RAISE EXCEPTION 'new Gold version requires a frozen answer image' USING ERRCODE='23514';
  END IF;
  IF TG_OP='UPDATE' AND ROW(OLD.frozen_answer_segment_id,OLD.frozen_crop_file_asset_id,OLD.frozen_crop_sha256,OLD.frozen_crop_owner_type,OLD.frozen_crop_owner_id,OLD.image_invalidated_at)
    IS DISTINCT FROM ROW(NEW.frozen_answer_segment_id,NEW.frozen_crop_file_asset_id,NEW.frozen_crop_sha256,NEW.frozen_crop_owner_type,NEW.frozen_crop_owner_id,NEW.image_invalidated_at) THEN
    RAISE EXCEPTION 'Gold image identity is immutable; nominate a new version' USING ERRCODE='55000';
  END IF;
  IF NEW.approved_at IS NOT NULL AND (NEW.frozen_crop_file_asset_id IS NULL OR NEW.image_invalidated_at IS NOT NULL) THEN
    RAISE EXCEPTION 'Gold image identity is unavailable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_validate_gold_frozen_image BEFORE INSERT OR UPDATE ON grading_gold_paper_version
FOR EACH ROW EXECUTE FUNCTION validate_gold_frozen_image();

CREATE OR REPLACE FUNCTION validate_active_gold_paper_version() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status='active' AND NOT EXISTS (
    SELECT 1 FROM grading_gold_paper_version version
    WHERE version.tenant_id=NEW.tenant_id AND version.gold_paper_id=NEW.id AND version.version=NEW.active_version
      AND version.approved_at IS NOT NULL AND version.frozen_crop_file_asset_id IS NOT NULL AND version.image_invalidated_at IS NULL
  ) THEN
    RAISE EXCEPTION 'active Gold Paper requires an approved frozen-image version' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;
