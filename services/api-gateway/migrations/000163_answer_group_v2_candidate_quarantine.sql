-- The v1 text representation removed decimal points and other meaningful
-- punctuation. Previously confirmed group-score proposals require human
-- recheck before they can be considered for grading.
--
-- Install the write guard before quarantining existing rows. CREATE TRIGGER
-- takes a table lock through transaction commit, so an old application
-- instance racing this migration cannot insert another active v1 candidate
-- after the backfill has passed it.
CREATE OR REPLACE FUNCTION guard_answer_group_active_candidate_version()
RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  grouping_algorithm_version TEXT;
  grouping_representation_version TEXT;
BEGIN
  IF NEW.candidate_kind <> 'group_score' OR NEW.status <> 'active' THEN
    RETURN NEW;
  END IF;

  SELECT algorithm_version, representation_version
  INTO grouping_algorithm_version, grouping_representation_version
  FROM answer_group
  WHERE tenant_id = NEW.tenant_id
    AND id = NEW.group_id;

  IF NOT FOUND
    OR NEW.algorithm_version <> 'deterministic-complete-link-v2'
    OR grouping_algorithm_version <> 'deterministic-complete-link-v2'
    OR grouping_representation_version <> 'normalized-char-bigram-v2' THEN
    RAISE EXCEPTION 'active answer-group score candidates require the v2 grouping representation'
      USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_answer_group_active_candidate_version
  ON answer_group_automation_candidate;
CREATE TRIGGER trg_answer_group_active_candidate_version
  BEFORE INSERT OR UPDATE OF tenant_id, group_id, candidate_kind, status, algorithm_version
  ON answer_group_automation_candidate
  FOR EACH ROW EXECUTE FUNCTION guard_answer_group_active_candidate_version();

UPDATE answer_group_automation_candidate AS candidate
SET status = 'manual_required'
FROM answer_group AS grouping
WHERE candidate.tenant_id = grouping.tenant_id
  AND candidate.group_id = grouping.id
  AND candidate.candidate_kind = 'group_score'
  AND candidate.status = 'active'
  AND (
    candidate.algorithm_version <> 'deterministic-complete-link-v2'
    OR grouping.algorithm_version <> 'deterministic-complete-link-v2'
    OR grouping.representation_version <> 'normalized-char-bigram-v2'
  );
