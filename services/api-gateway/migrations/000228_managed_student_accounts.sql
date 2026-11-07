-- 学生身份采用独立外键；同一学校可用学号登录，内部用户名不承载可变学号。
-- 存量仅关联已明确绑定同校学生且双方唯一的学生角色，不生成或重置任何密码。
ALTER TABLE student ADD CONSTRAINT uq_student_tenant_school_id UNIQUE (tenant_id, school_id, id);
ALTER TABLE app_user ADD COLUMN student_id UUID;
ALTER TABLE app_user ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE app_user ADD CONSTRAINT fk_app_user_student_school
  FOREIGN KEY (tenant_id, school_id, student_id) REFERENCES student (tenant_id, school_id, id);
ALTER TABLE app_user ADD CONSTRAINT ck_app_user_student_school
  CHECK (student_id IS NULL OR school_id IS NOT NULL);
CREATE UNIQUE INDEX uq_app_user_active_student ON app_user (tenant_id, student_id)
  WHERE student_id IS NOT NULL AND deleted_at IS NULL;

-- 唯一性先统计全部历史本人声明；不能先排除混合职责，再把同学生的另一账号误判为唯一。
WITH candidates AS (
  SELECT DISTINCT u.id AS user_id, u.tenant_id, s.school_id, s.id AS student_id,
    (u.school_id IS NULL OR s.school_id=u.school_id) AND NOT EXISTS (
      SELECT 1 FROM user_role other_binding
      JOIN role other_role ON other_role.tenant_id=other_binding.tenant_id AND other_role.id=other_binding.role_id AND other_role.deleted_at IS NULL
      WHERE other_binding.tenant_id=u.tenant_id AND other_binding.user_id=u.id AND other_binding.deleted_at IS NULL AND other_role.code<>'student'
    ) AS eligible
  FROM app_user u
  JOIN user_role ur ON ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL
  JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.code='student' AND r.deleted_at IS NULL
  JOIN student s ON s.tenant_id=u.tenant_id AND s.id::text=ur.data_scope->>'student_id'
    AND s.deleted_at IS NULL
  WHERE u.deleted_at IS NULL AND ur.data_scope->>'scope'='self'
), unique_candidates AS (
  SELECT *, count(*) OVER (PARTITION BY tenant_id,user_id) AS user_count,
    count(*) OVER (PARTITION BY tenant_id,student_id) AS student_count
  FROM candidates
)
UPDATE app_user u SET student_id=c.student_id, school_id=c.school_id
FROM unique_candidates c
WHERE u.id=c.user_id AND u.tenant_id=c.tenant_id AND c.user_count=1 AND c.student_count=1 AND c.eligible;
