-- 回到已经识别过的裁切时，识别任务会按题块与哈希去重；因此恢复匹配的历史产物指针，不能等待旧任务再次执行。
-- 只调整数学产物的当前指针，不改变题块的文件、哈希或其它图像指针。锁顺序与产物写入一致：题块在前、产物在后。
CREATE FUNCTION restore_current_math_artifact_for_crop(t UUID, segment_id UUID)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE
  matching_artifact UUID;
BEGIN
  PERFORM 1 FROM answer_segment seg
  WHERE seg.tenant_id=t AND seg.id=segment_id AND seg.deleted_at IS NULL
  FOR UPDATE;

  SELECT artifact.id INTO matching_artifact
  FROM answer_segment seg
  JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
  JOIN LATERAL (
    SELECT snapshot.id FROM exam_question_snapshot snapshot
    WHERE snapshot.tenant_id=q.tenant_id AND snapshot.exam_id=q.exam_id AND snapshot.question_id=q.id
    ORDER BY snapshot.snapshot_version DESC,snapshot.id DESC LIMIT 1
  ) frozen ON true
  JOIN math_understanding_artifact artifact
    ON artifact.tenant_id=seg.tenant_id AND artifact.answer_segment_id=seg.id
    AND artifact.exam_question_snapshot_id=frozen.id
    AND lower(regexp_replace(btrim(artifact.input_hash),'^sha256:','','i'))=
        lower(regexp_replace(btrim(seg.crop_sha256),'^sha256:','','i'))
  WHERE seg.tenant_id=t AND seg.id=segment_id AND seg.deleted_at IS NULL
    AND seg.processing_status='completed' AND seg.crop_file_asset_id IS NOT NULL
    AND COALESCE(btrim(seg.crop_sha256),'')<>''
  ORDER BY artifact.version DESC,artifact.id DESC LIMIT 1;

  IF matching_artifact IS NULL THEN
    RETURN;
  END IF;
  -- 最新匹配版本包含已核验或人工校正结果，不能退回同哈希最初的识别草稿。
  UPDATE math_understanding_artifact SET is_current=false
  WHERE tenant_id=t AND answer_segment_id=segment_id AND is_current AND id<>matching_artifact;
  UPDATE math_understanding_artifact SET is_current=true
  WHERE tenant_id=t AND id=matching_artifact AND NOT is_current;
END
$$;

CREATE FUNCTION restore_math_artifact_after_crop_change()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM restore_current_math_artifact_for_crop(NEW.tenant_id,NEW.id);
  RETURN NEW;
END
$$;

CREATE TRIGGER trg_restore_math_artifact_after_crop_change
AFTER UPDATE OF crop_sha256,crop_file_asset_id,processing_status ON answer_segment
FOR EACH ROW EXECUTE FUNCTION restore_math_artifact_after_crop_change();

-- 恢复已发生过哈希回退、且当前指针与实体裁切不一致的存量记录；不存在匹配证据时仍由识别流程补齐。
DO $$
DECLARE affected RECORD;
BEGIN
  FOR affected IN
    SELECT DISTINCT seg.tenant_id,seg.id FROM answer_segment seg
    JOIN math_understanding_artifact artifact ON artifact.tenant_id=seg.tenant_id AND artifact.answer_segment_id=seg.id
    WHERE seg.deleted_at IS NULL AND seg.processing_status='completed'
      AND seg.crop_file_asset_id IS NOT NULL AND COALESCE(btrim(seg.crop_sha256),'')<>''
      AND lower(regexp_replace(btrim(artifact.input_hash),'^sha256:','','i'))=
          lower(regexp_replace(btrim(seg.crop_sha256),'^sha256:','','i'))
      AND NOT EXISTS (
        SELECT 1 FROM math_understanding_artifact current_artifact
        WHERE current_artifact.tenant_id=seg.tenant_id AND current_artifact.answer_segment_id=seg.id
          AND current_artifact.is_current
          AND current_artifact.exam_question_snapshot_id=(
            SELECT latest_snapshot.id FROM exam_question_snapshot latest_snapshot
            WHERE latest_snapshot.tenant_id=seg.tenant_id AND latest_snapshot.question_id=seg.question_id
            ORDER BY latest_snapshot.snapshot_version DESC,latest_snapshot.id DESC LIMIT 1
          )
          AND lower(regexp_replace(btrim(current_artifact.input_hash),'^sha256:','','i'))=
              lower(regexp_replace(btrim(seg.crop_sha256),'^sha256:','','i'))
      )
  LOOP
    PERFORM restore_current_math_artifact_for_crop(affected.tenant_id,affected.id);
  END LOOP;
END
$$;
