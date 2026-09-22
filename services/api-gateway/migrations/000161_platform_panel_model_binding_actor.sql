-- Platform administrators configure a school's panel roles across tenant
-- boundaries. Keep the actor attributable to a real user without requiring
-- that platform user to be a member of the target school.
ALTER TABLE model_role_binding
  DROP CONSTRAINT model_role_binding_tenant_id_created_by_fkey;

ALTER TABLE model_role_binding
  ADD CONSTRAINT model_role_binding_created_by_fkey
  FOREIGN KEY (created_by) REFERENCES app_user(id);
