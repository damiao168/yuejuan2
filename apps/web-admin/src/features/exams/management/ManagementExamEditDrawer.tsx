import { useEffect, useRef, useState } from "react";
import { Alert, App, Button, Descriptions, Drawer, Form, Input, Select } from "antd";
import { Save } from "lucide-react";
import { getUserErrorMessage } from "../../../api/client";
import { updateManagementExam, type ManagementExam, type ManagementExamUpdate } from "../../../api/exams";
import type { SchoolClass } from "../../../api/org";
import { examSubjectLabel } from "../../../constants/examStatus";
import { examTypeOptions } from "../../../constants/examCatalog";
import { canEditManagementDefaultClasses, canEditManagementScope, managementDefaultClassesHelp, managementEditPayload, type ManagementEditValues } from "./managementPresentation";
import { isManagementCommandUncertain } from "./managementCommand";

export function ManagementExamEditDrawer({ exam, classes, schoolName, gradeName, onClose, onSaved }: Readonly<{
  exam: ManagementExam | null;
  classes: SchoolClass[];
  schoolName?: string;
  gradeName?: string;
  onClose: () => void;
  onSaved: () => Promise<void>;
}>) {
  const { message } = App.useApp();
  const [form] = Form.useForm<ManagementEditValues>();
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [uncertain, setUncertain] = useState(false);
  const command = useRef<{ id: string; payload: ManagementExamUpdate } | null>(null);

  useEffect(() => {
    command.current = null;
    setSaveError(null);
    setUncertain(false);
    if (exam) form.setFieldsValue({ name: exam.name, exam_type: exam.exam_type, class_ids: exam.default_class_ids ?? exam.class_ids });
  }, [exam, form]);

  const save = async () => {
    if (!exam || !exam.can_edit || saving) return;
    let values: ManagementEditValues;
    try { values = await form.validateFields(); } catch { return; }
    // 响应丢失时保留同一请求和幂等键；重试期间冻结表单，避免同一命令被换成另一份设置。
    const pending = command.current ?? { id: crypto.randomUUID(), payload: managementEditPayload(exam, values) };
    if (pending.payload.name === undefined && pending.payload.exam_type === undefined && pending.payload.class_ids === undefined) {
      onClose();
      return;
    }
    command.current = pending;
    setSaving(true);
    setSaveError(null);
    try {
      await updateManagementExam(exam, pending.payload, pending.id);
      message.success("整场考试已更新");
      onClose();
      await onSaved();
    } catch (error) {
      const ambiguous = isManagementCommandUncertain(error);
      setUncertain(ambiguous);
      if (!ambiguous) command.current = null;
      setSaveError(getUserErrorMessage(error, "考试保存失败，请稍后重试") + (ambiguous ? "。结果尚未确认，请使用“重试保存”继续同一操作。" : "。请刷新考试后检查最新设置。"));
    } finally {
      setSaving(false);
    }
  };

  const affected = exam?.exams.filter((subject) => exam.inherited_subject_ids?.includes(subject.id) || exam.kind === "standalone") ?? [];
  const independent = exam?.exams.filter((subject) => !affected.some((item) => item.id === subject.id)) ?? [];
  const scopeEditable = exam ? canEditManagementScope(exam) : false;
  const classesEditable = exam ? canEditManagementDefaultClasses(exam) : false;
  const allowedClasses = classes.filter((item) => item.school_id === exam?.school_id && (!exam.grade_id || item.grade_id === exam.grade_id));
  return <Drawer title="编辑考试" open={Boolean(exam)} onClose={() => { if (!saving) onClose(); }} width={680} destroyOnHidden
    extra={<Button type="primary" icon={<Save size={16} />} loading={saving} disabled={!exam?.can_edit} onClick={() => void save()}>{uncertain ? "重试保存" : "保存"}</Button>}>
    {exam ? <>
      {saveError ? <Alert className="drawer-alert" type="error" showIcon message={saveError} /> : null}
      <Descriptions column={1} size="small"><Descriptions.Item label="学校">{schoolName || "本校"}</Descriptions.Item><Descriptions.Item label="年级">{gradeName || "未关联年级"}</Descriptions.Item></Descriptions>
      <Form form={form} layout="vertical" disabled={!exam.can_edit || saving || uncertain} preserve={false}>
        <Form.Item label="考试名称" name="name" rules={[{ required: true, whitespace: true, message: "请输入考试名称" }, { max: 200, message: "考试名称最多 200 个字符" }]}><Input /></Form.Item>
        <Form.Item label="考试类型" name="exam_type" rules={[{ required: true, message: "请选择考试类型" }]} extra={!scopeEditable ? "考试进入后续阶段后，考试类型不能修改。" : undefined}><Select options={examTypeOptions} disabled={!scopeEditable || saving || uncertain} /></Form.Item>
        <Form.Item label="默认参考班级" name="class_ids" rules={affected.length && (exam.default_class_ids ?? exam.class_ids).length ? [{ required: true, type: "array", min: 1, message: "请选择至少一个班级" }] : []} extra={managementDefaultClassesHelp(affected.length > 0, classesEditable)}>
          <Select mode="multiple" optionFilterProp="label" disabled={!classesEditable || saving || uncertain} options={allowedClasses.map((item) => ({ label: `${item.name} (${item.code})`, value: item.id }))} />
        </Form.Item>
        <Alert type="info" showIcon message="默认班级变更范围" description={<>
          <p>将影响：{affected.map((subject) => examSubjectLabel(subject.subject)).join("、") || "暂无继承默认范围的学科"}。</p>
          {independent.length ? <p>{independent.map((subject) => examSubjectLabel(subject.subject)).join("、")}使用独立参考范围，保留各自班级。</p> : null}
          <p>满分、试卷、答案、评分标准和阅卷发布设置，请进入对应学科配置。</p>
        </>} />
      </Form>
    </> : null}
  </Drawer>;
}
