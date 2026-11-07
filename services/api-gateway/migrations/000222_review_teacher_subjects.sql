-- 任教学科只作为同校考试分配资格；不能从学科或历史教师班级关系获得学校管理权限。
-- 存量学科无法从班级可靠推断，保持空数组，由学校管理员在个人资料中维护后再分配新任务。
ALTER TABLE app_user ADD COLUMN teaching_subjects text[] NOT NULL DEFAULT '{}';
ALTER TABLE app_user ADD CONSTRAINT ck_app_user_teaching_subjects CHECK (
  cardinality(teaching_subjects) <= 9
  AND array_position(teaching_subjects, NULL) IS NULL
  AND teaching_subjects <@ ARRAY['chinese','mathematics','english','physics','chemistry','biology','history','geography','ethics_politics']::text[]
);

UPDATE role SET scope_type='exam_task', updated_at=now()
WHERE code='teacher' AND deleted_at IS NULL;
UPDATE user_role ur SET data_scope=jsonb_build_object('scope','exam_task'),updated_at=now()
FROM role r WHERE r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.code='teacher'
  AND r.deleted_at IS NULL AND ur.deleted_at IS NULL;

-- 管理员的组织管理关系保留；只撤销普通阅卷教师的旧班级绑定，并同步废止旧凭据。
WITH review_teachers AS (
 SELECT DISTINCT u.tenant_id,u.id FROM app_user u
 JOIN user_role ur ON ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL
 JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
 WHERE u.deleted_at IS NULL AND r.code IN ('teacher','grader','arbitrator')
 AND NOT EXISTS (
   SELECT 1 FROM user_role aur JOIN role ar ON ar.tenant_id=aur.tenant_id AND ar.id=aur.role_id AND ar.deleted_at IS NULL
   WHERE aur.tenant_id=u.tenant_id AND aur.user_id=u.id AND aur.deleted_at IS NULL AND ar.code IN ('platform_admin','tenant_admin','school_admin')
 )
), revoked_bindings AS (
 UPDATE teacher_class tc SET deleted_at=now(),updated_at=now() FROM review_teachers rt
 WHERE tc.tenant_id=rt.tenant_id AND tc.teacher_id=rt.id AND tc.deleted_at IS NULL RETURNING tc.teacher_id
), revoked_sessions AS (
 UPDATE auth_session s SET revoked_at=now(),revoke_reason='review_teacher_scope_changed',updated_at=now() FROM review_teachers rt
 WHERE s.tenant_id=rt.tenant_id AND s.user_id=rt.id AND s.revoked_at IS NULL RETURNING s.id
), revoked_devices AS (
 UPDATE auth_trusted_device d SET revoked_at=now(),updated_at=now() FROM review_teachers rt
 WHERE d.tenant_id=rt.tenant_id AND d.user_id=rt.id AND d.revoked_at IS NULL RETURNING d.id
)
UPDATE app_user u SET security_epoch=security_epoch+1,updated_at=now() FROM review_teachers rt
WHERE u.tenant_id=rt.tenant_id AND u.id=rt.id;
