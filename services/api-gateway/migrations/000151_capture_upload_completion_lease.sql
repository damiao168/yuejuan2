-- A durable lease lets a later request recover an interrupted finalization.
-- A token fences all terminal/reset writes from older finalizers.
ALTER TABLE capture_upload_session
  ADD COLUMN completion_token UUID,
  ADD COLUMN completion_lease_until TIMESTAMPTZ;
