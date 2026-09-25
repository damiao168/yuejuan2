ALTER TABLE double_mark_session DROP CONSTRAINT IF EXISTS double_mark_session_status_check;
ALTER TABLE double_mark_session ADD CONSTRAINT double_mark_session_status_check
  CHECK (status IN ('pending','first_submitted','second_submitted','auto_finalized','needs_arbitration','arbitrated','cancelled'));

ALTER TABLE arbitration_task DROP CONSTRAINT IF EXISTS arbitration_task_status_check;
ALTER TABLE arbitration_task ADD CONSTRAINT arbitration_task_status_check
  CHECK (status IN ('pending','assigned','submitted','cancelled'));
