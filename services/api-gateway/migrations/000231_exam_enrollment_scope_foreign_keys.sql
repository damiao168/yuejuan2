-- 复合外键先核查所有存量引用，包括软删除记录。若发现归属冲突，迁移会报告约束和出错的键，
-- 由维护者核对真实归属后重试；不自动迁移学籍、修改历史名册或清空引用。
ALTER TABLE grade ADD CONSTRAINT uq_grade_tenant_school_id UNIQUE (tenant_id, school_id, id);
ALTER TABLE exam_session ADD CONSTRAINT uq_exam_session_tenant_school_id UNIQUE (tenant_id, school_id, id);
ALTER TABLE academic_year ADD CONSTRAINT uq_academic_year_tenant_school_id UNIQUE (tenant_id, school_id, id);
ALTER TABLE grade_cohort ADD CONSTRAINT uq_grade_cohort_tenant_school_id UNIQUE (tenant_id, school_id, id);
ALTER TABLE school_class ADD CONSTRAINT uq_school_class_tenant_school_id UNIQUE (tenant_id, school_id, id);
ALTER TABLE school_class ADD CONSTRAINT uq_school_class_enrollment_scope
  UNIQUE (tenant_id, school_id, academic_year_id, grade_cohort_id, id);

-- 操作者可以是同租户的管理账号，因此只绑定租户；子科和场次必须属于同一学校。
ALTER TABLE exam_session
  ADD CONSTRAINT fk_exam_session_school_tenant FOREIGN KEY (tenant_id, school_id) REFERENCES school (tenant_id, id),
  ADD CONSTRAINT fk_exam_session_grade_school FOREIGN KEY (tenant_id, school_id, grade_id) REFERENCES grade (tenant_id, school_id, id),
  ADD CONSTRAINT fk_exam_session_creator_tenant FOREIGN KEY (tenant_id, created_by) REFERENCES app_user (tenant_id, id),
  ADD CONSTRAINT fk_exam_session_archiver_tenant FOREIGN KEY (tenant_id, archived_by) REFERENCES app_user (tenant_id, id);
ALTER TABLE exam
  ADD CONSTRAINT fk_exam_parent_session_school FOREIGN KEY (tenant_id, school_id, exam_session_id) REFERENCES exam_session (tenant_id, school_id, id),
  ADD CONSTRAINT fk_exam_archiver_tenant FOREIGN KEY (tenant_id, archived_by) REFERENCES app_user (tenant_id, id);
ALTER TABLE exam_blueprint_section
  ADD CONSTRAINT fk_exam_blueprint_section_exam_tenant FOREIGN KEY (tenant_id, exam_id) REFERENCES exam (tenant_id, id);

-- 学生与考试是名册的持久身份。年级、班级和名称是冻结字段，不随学生当前学籍变更。
ALTER TABLE exam_candidate_snapshot
  ADD CONSTRAINT fk_exam_candidate_snapshot_exam_tenant FOREIGN KEY (tenant_id, exam_id) REFERENCES exam (tenant_id, id),
  ADD CONSTRAINT fk_exam_candidate_snapshot_student_tenant FOREIGN KEY (tenant_id, student_id) REFERENCES student (tenant_id, id);

ALTER TABLE academic_year
  ADD CONSTRAINT fk_academic_year_school_tenant FOREIGN KEY (tenant_id, school_id) REFERENCES school (tenant_id, id);
ALTER TABLE grade_cohort
  ADD CONSTRAINT fk_grade_cohort_school_tenant FOREIGN KEY (tenant_id, school_id) REFERENCES school (tenant_id, id);
ALTER TABLE grade
  ADD CONSTRAINT fk_grade_academic_year_school FOREIGN KEY (tenant_id, school_id, academic_year_id) REFERENCES academic_year (tenant_id, school_id, id),
  ADD CONSTRAINT fk_grade_cohort_same_school FOREIGN KEY (tenant_id, school_id, grade_cohort_id) REFERENCES grade_cohort (tenant_id, school_id, id);
ALTER TABLE school_class
  ADD CONSTRAINT fk_school_class_grade_school FOREIGN KEY (tenant_id, school_id, grade_id) REFERENCES grade (tenant_id, school_id, id),
  ADD CONSTRAINT fk_school_class_academic_year_school FOREIGN KEY (tenant_id, school_id, academic_year_id) REFERENCES academic_year (tenant_id, school_id, id),
  ADD CONSTRAINT fk_school_class_cohort_school FOREIGN KEY (tenant_id, school_id, grade_cohort_id) REFERENCES grade_cohort (tenant_id, school_id, id);
ALTER TABLE student
  ADD CONSTRAINT fk_student_class_school FOREIGN KEY (tenant_id, school_id, class_id) REFERENCES school_class (tenant_id, school_id, id);

-- 学籍保留当时的班级、学年和届别，转班新增关系，不能把旧记录指向同校另一学年的班级。
ALTER TABLE student_enrollment
  ADD CONSTRAINT fk_student_enrollment_school_tenant FOREIGN KEY (tenant_id, school_id) REFERENCES school (tenant_id, id),
  ADD CONSTRAINT fk_student_enrollment_student_school FOREIGN KEY (tenant_id, school_id, student_id) REFERENCES student (tenant_id, school_id, id),
  ADD CONSTRAINT fk_student_enrollment_year_school FOREIGN KEY (tenant_id, school_id, academic_year_id) REFERENCES academic_year (tenant_id, school_id, id),
  ADD CONSTRAINT fk_student_enrollment_cohort_school FOREIGN KEY (tenant_id, school_id, grade_cohort_id) REFERENCES grade_cohort (tenant_id, school_id, id),
  ADD CONSTRAINT fk_student_enrollment_class_scope FOREIGN KEY (tenant_id, school_id, academic_year_id, grade_cohort_id, class_id)
    REFERENCES school_class (tenant_id, school_id, academic_year_id, grade_cohort_id, id);

-- 新约束验证后移除被覆盖的旧外键。保留原来的 NO ACTION 和允许空值的可选关系。
ALTER TABLE exam_session DROP CONSTRAINT exam_session_school_id_fkey, DROP CONSTRAINT exam_session_grade_id_fkey,
  DROP CONSTRAINT exam_session_created_by_fkey, DROP CONSTRAINT exam_session_archived_by_fkey;
ALTER TABLE exam DROP CONSTRAINT exam_exam_session_id_fkey, DROP CONSTRAINT exam_archived_by_fkey;
ALTER TABLE exam_blueprint_section DROP CONSTRAINT exam_blueprint_section_exam_id_fkey;
ALTER TABLE exam_candidate_snapshot DROP CONSTRAINT exam_candidate_snapshot_exam_id_fkey, DROP CONSTRAINT exam_candidate_snapshot_student_id_fkey;
ALTER TABLE academic_year DROP CONSTRAINT academic_year_school_id_fkey;
ALTER TABLE grade_cohort DROP CONSTRAINT grade_cohort_school_id_fkey;
ALTER TABLE grade DROP CONSTRAINT grade_academic_year_id_fkey, DROP CONSTRAINT grade_grade_cohort_id_fkey;
ALTER TABLE school_class DROP CONSTRAINT school_class_grade_id_fkey, DROP CONSTRAINT fk_school_class_grade_tenant,
  DROP CONSTRAINT school_class_academic_year_id_fkey, DROP CONSTRAINT school_class_grade_cohort_id_fkey;
ALTER TABLE student DROP CONSTRAINT student_class_id_fkey, DROP CONSTRAINT fk_student_school_class_tenant;
ALTER TABLE student_enrollment DROP CONSTRAINT student_enrollment_school_id_fkey, DROP CONSTRAINT student_enrollment_student_id_fkey,
  DROP CONSTRAINT student_enrollment_academic_year_id_fkey, DROP CONSTRAINT student_enrollment_grade_cohort_id_fkey,
  DROP CONSTRAINT student_enrollment_class_id_fkey;
