-- 解除绑定也必须消耗修订号。否则删除绑定行后重新建立记录会回到 revision=1，旧页面可再次命中旧的 expected_revision。
-- 暂停旧服务的手动及自动绑定写入，回填现存绑定后切换为读写游标的新服务；旧写入口不能与新入口混用。
-- 历史上已经删除且未留记录的绑定无法追溯，只能从首次新写入开始保留游标。
CREATE TABLE exam_answer_sheet_template_binding_revision (
  tenant_id UUID NOT NULL,
  exam_id UUID NOT NULL,
  last_revision INT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, exam_id),
  CONSTRAINT fk_exam_answer_sheet_template_binding_revision_exam
    FOREIGN KEY (tenant_id, exam_id) REFERENCES exam (tenant_id, id),
  CONSTRAINT ck_exam_answer_sheet_template_binding_revision_nonnegative CHECK (last_revision >= 0)
);

INSERT INTO exam_answer_sheet_template_binding_revision (tenant_id, exam_id, last_revision)
SELECT tenant_id, exam_id, revision
FROM exam_answer_sheet_template_binding
ON CONFLICT (tenant_id, exam_id) DO UPDATE
SET last_revision = GREATEST(exam_answer_sheet_template_binding_revision.last_revision, EXCLUDED.last_revision),
    updated_at = now();
