import { useEffect, useMemo, useState } from "react";
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  InputNumber,
  Result,
  Select,
  Space,
  Steps,
  Tag,
  Upload
} from "antd";
import type { TableColumnsType } from "antd";
import { ArrowRight, CheckCircle2, Download, FileUp, RefreshCw } from "lucide-react";
import Papa from "papaparse";
import { getUserErrorMessage } from "../api/client";
import { validateNewPassword } from "../auth/loginSecurity";
import { createExam, listExams } from "../api/exams";
import {
  createClass,
  createGrade,
  createSchool,
  importStudentsCSV,
  listClasses,
  listGrades,
  listSchools,
  listStudents,
  type Grade,
  type School,
  type SchoolClass,
  type Student,
  type StudentImportError
} from "../api/org";
import { createManagedUser, listAssignableRoles, listManagedUsers, type AssignableRole, type ManagedUser } from "../api/users";
import { ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";

interface SetupData {
  schools: School[];
  grades: Grade[];
  classes: SchoolClass[];
  students: Student[];
  users: ManagedUser[];
  roles: AssignableRole[];
  examCount: number;
}

interface ImportRow {
  key: number;
  sourceRow: number;
  studentNo: string;
  name: string;
  classCode: string;
  schoolId?: string;
  classId?: string;
  error?: string;
}

const setupSteps = ["机构信息", "学年与年级", "班级", "学生", "人员账号", "第一场考试", "完成"];

function messageOf(error: unknown) {
  return getUserErrorMessage(error, "请求失败");
}

function downloadCSV(filename: string, rows: Array<Record<string, string | number>>) {
  const blob = new Blob(["\ufeff", Papa.unparse(rows)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

export function OrganizationSetupPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const { message } = App.useApp();
  const [data, setData] = useState<SetupData>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  const [step, setStep] = useState(0);
  const [stepInitialized, setStepInitialized] = useState(false);
  const [csvHeaders, setCsvHeaders] = useState<string[]>([]);
  const [csvRecords, setCsvRecords] = useState<Record<string, string>[]>([]);
  const [mapping, setMapping] = useState({ studentNo: "", name: "", classCode: "" });
  const [importErrors, setImportErrors] = useState<StudentImportError[]>([]);

  const load = async () => {
    setLoading(true);
    setError(undefined);
    try {
      const [schools, grades, classes, students, users, roles, exams] = await Promise.all([
        listSchools(),
        listGrades(),
        listClasses(),
        listStudents(),
        listManagedUsers({ limit: 200 }),
        listAssignableRoles(),
        listExams()
      ]);
      setData({
        schools: schools.schools,
        grades: grades.grades,
        classes: classes.classes,
        students: students.students,
        users: users.users,
        roles: roles.roles,
        examCount: exams.exams.length
      });
    } catch (loadError) {
      setError(messageOf(loadError));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const completed = useMemo(
    () => [
      Boolean(data?.schools.length),
      Boolean(data?.grades.length),
      Boolean(data?.classes.length),
      Boolean(data?.students.length),
      Boolean(data && data.users.length > 1),
      Boolean(data?.examCount),
      Boolean(data?.schools.length && data.grades.length && data.classes.length && data.students.length && data.users.length > 1 && data.examCount)
    ],
    [data]
  );

  useEffect(() => {
    if (data && !stepInitialized) {
      const firstIncomplete = completed.slice(0, 6).findIndex((value) => !value);
      setStep(firstIncomplete === -1 ? 6 : firstIncomplete);
      setStepInitialized(true);
    }
  }, [completed, data, stepInitialized]);

  const runSave = async (action: () => Promise<unknown>, success: string) => {
    setSaving(true);
    try {
      await action();
      message.success(success);
      await load();
      setStep((value) => Math.min(6, value + 1));
    } catch (saveError) {
      message.error(messageOf(saveError));
    } finally {
      setSaving(false);
    }
  };

  const importRows = useMemo<ImportRow[]>(() => {
    const existing = new Set(data?.students.map((student) => student.student_no.toLowerCase()) ?? []);
    const seen = new Set<string>();
    const classByCode = new Map(data?.classes.map((item) => [item.code.toLowerCase(), item]) ?? []);
    return csvRecords.map((record, index) => {
      const studentNo = (record[mapping.studentNo] ?? "").trim();
      const name = (record[mapping.name] ?? "").trim();
      const classCode = (record[mapping.classCode] ?? "").trim();
      const normalizedNo = studentNo.toLowerCase();
      const targetClass = classByCode.get(classCode.toLowerCase());
      let rowError = "";
      if (!studentNo || !name || !classCode) rowError = "学号、姓名和班级代码不能为空";
      else if (existing.has(normalizedNo)) rowError = "学号已存在";
      else if (seen.has(normalizedNo)) rowError = "文件内学号重复";
      else if (!targetClass) rowError = "班级代码不存在";
      seen.add(normalizedNo);
      return {
        key: index,
        sourceRow: index + 2,
        studentNo,
        name,
        classCode,
        schoolId: targetClass?.school_id,
        classId: targetClass?.id,
        error: rowError || undefined
      };
    });
  }, [csvRecords, data?.classes, data?.students, mapping]);

  const validImportRows = importRows.filter((row) => !row.error);

  const readCSV = async (file: File) => {
    const text = await file.text();
    const parsed = Papa.parse<Record<string, string>>(text, { header: true, skipEmptyLines: "greedy", transformHeader: (value) => value.trim() });
    const headers = parsed.meta.fields ?? [];
    setCsvHeaders(headers);
    setCsvRecords(parsed.data);
    const choose = (...names: string[]) => headers.find((header) => names.includes(header.toLowerCase())) ?? "";
    setMapping({
      studentNo: choose("student_no", "学号", "考号"),
      name: choose("name", "姓名"),
      classCode: choose("class_code", "班级代码", "班级")
    });
    setImportErrors(parsed.errors.map((item) => ({ row: (item.row ?? 0) + 2, message: "该行 CSV 格式有误，请检查列数和引号" })));
    return false;
  };

  const submitStudents = async () => {
    if (!validImportRows.length) {
      message.warning("没有可导入的有效学生记录");
      return;
    }
    const csv = Papa.unparse([
      ["student_no", "name", "school_id", "class_id"],
      ...validImportRows.map((row) => [row.studentNo, row.name, row.schoolId, row.classId])
    ]);
    setSaving(true);
    try {
      const result = await importStudentsCSV(csv);
      setImportErrors(result.result.errors);
      if (result.result.created > 0) {
        message.success(`已导入 ${result.result.created} 名学生`);
        await load();
        if (!result.result.errors.length) setStep(4);
      }
    } catch (importError) {
      message.error(messageOf(importError));
    } finally {
      setSaving(false);
    }
  };

  const importColumns: TableColumnsType<ImportRow> = [
    { title: "原始行", dataIndex: "sourceRow", width: 80 },
    { title: "学号", dataIndex: "studentNo" },
    { title: "姓名", dataIndex: "name" },
    { title: "班级代码", dataIndex: "classCode" },
    { title: "校验", dataIndex: "error", render: (value?: string) => value ? <Tag color="error">{value}</Tag> : <Tag color="success">可导入</Tag> }
  ];

  if (!data && loading) return <LoadingState label="正在读取机构启用进度" />;
  if (!data && error) return <ErrorState message={error} onRetry={() => void load()} />;
  if (!data) return null;

  const stepContent = (() => {
    switch (step) {
      case 0:
        return (
          <Form key="school" layout="vertical" preserve={false} onFinish={(values) => void runSave(() => createSchool(values), "机构信息已保存")}>
            <h2>机构信息</h2>
            <p className="section-copy">填写学校或考试机构的正式名称和内部代码。</p>
            {data.schools.length ? <Alert type="success" showIcon message={`已创建：${data.schools.map((item) => item.name).join("、")}`} /> : null}
            <div className="form-grid compact-form-grid">
              <Form.Item name="name" label="机构名称" rules={[{ required: true, message: "请输入机构名称" }]}><Input placeholder="示例中学" /></Form.Item>
              <Form.Item name="code" label="机构代码" rules={[{ required: true, message: "请输入机构代码" }]}><Input placeholder="SCHOOL-001" /></Form.Item>
            </div>
            <Button type="primary" htmlType="submit" loading={saving}>保存并继续</Button>
          </Form>
        );
      case 1:
        return (
          <Form key="grade" layout="vertical" preserve={false} onFinish={(values) => void runSave(() => createGrade(values), "学年与年级已保存")} initialValues={{ academic_year: `${new Date().getFullYear()}-${new Date().getFullYear() + 1}`, education_stage: "senior", level_no: 10 }}>
            <h2>学年与年级</h2>
            <p className="section-copy">年级会用于班级和考试学生范围。</p>
            {data.grades.length ? <Alert type="success" showIcon message={`已有 ${data.grades.length} 个年级`} /> : null}
            <div className="form-grid compact-form-grid">
              <Form.Item name="school_id" label="所属机构" rules={[{ required: true }]}><Select options={data.schools.map((item) => ({ value: item.id, label: item.name }))} /></Form.Item>
              <Form.Item name="education_stage" label="学段" rules={[{ required: true }]}><Select options={[{ value: "junior", label: "初中" }, { value: "senior", label: "高中" }]} /></Form.Item>
              <Form.Item name="academic_year" label="学年" rules={[{ required: true }]}><Input /></Form.Item>
              <Form.Item name="name" label="年级名称" rules={[{ required: true }]}><Input placeholder="高一年级" /></Form.Item>
              <Form.Item name="level_no" label="年级序号" rules={[{ required: true }]}><InputNumber min={1} max={20} /></Form.Item>
            </div>
            <Button type="primary" htmlType="submit" loading={saving}>保存并继续</Button>
          </Form>
        );
      case 2:
        return (
          <Form key="class" layout="vertical" preserve={false} onFinish={(values) => void runSave(() => createClass(values), "班级已保存")}>
            <h2>班级</h2>
            <p className="section-copy">班级代码用于学生导入匹配，请保持唯一且易识别。</p>
            {data.classes.length ? <Alert type="success" showIcon message={`已有 ${data.classes.length} 个班级`} /> : null}
            <div className="form-grid compact-form-grid">
              <Form.Item name="school_id" label="所属机构" rules={[{ required: true }]}><Select options={data.schools.map((item) => ({ value: item.id, label: item.name }))} /></Form.Item>
              <Form.Item name="grade_id" label="所属年级" rules={[{ required: true }]}><Select options={data.grades.map((item) => ({ value: item.id, label: `${item.name} · ${item.academic_year}` }))} /></Form.Item>
              <Form.Item name="name" label="班级名称" rules={[{ required: true }]}><Input placeholder="高一（1）班" /></Form.Item>
              <Form.Item name="code" label="班级代码" rules={[{ required: true }]}><Input placeholder="G10-01" /></Form.Item>
            </div>
            <Button type="primary" htmlType="submit" loading={saving}>保存并继续</Button>
          </Form>
        );
      case 3:
        return (
          <div>
            <h2>导入学生</h2>
            <p className="section-copy">先下载模板填写，或直接上传现有名单，然后指定学号、姓名、班级对应哪一列。只有校验通过的行会被导入。</p>
            <Space wrap>
              <Button icon={<Download size={16} />} onClick={() => downloadCSV("学生导入模板.csv", [{ student_no: "20260001", name: "张同学", class_code: data.classes[0]?.code ?? "G10-01" }])}>下载模板</Button>
              <Upload accept=".csv,text/csv" maxCount={1} showUploadList={false} beforeUpload={(file) => { void readCSV(file); return false; }}>
                <Button type="primary" icon={<FileUp size={16} />}>选择 CSV</Button>
              </Upload>
            </Space>
            {csvHeaders.length ? (
              <>
                <div className="mapping-grid">
                  <label>学号字段<Select value={mapping.studentNo || undefined} options={csvHeaders.map((value) => ({ value }))} onChange={(value) => setMapping((current) => ({ ...current, studentNo: value }))} /></label>
                  <label>姓名字段<Select value={mapping.name || undefined} options={csvHeaders.map((value) => ({ value }))} onChange={(value) => setMapping((current) => ({ ...current, name: value }))} /></label>
                  <label>班级代码字段<Select value={mapping.classCode || undefined} options={csvHeaders.map((value) => ({ value }))} onChange={(value) => setMapping((current) => ({ ...current, classCode: value }))} /></label>
                </div>
                <ResponsiveTable rowKey="key" size="small" columns={importColumns} dataSource={importRows} pagination={{ pageSize: 8 }} className="setup-preview-table dense-data-table" />
                <Space wrap>
                  <Button type="primary" icon={<FileUp size={16} />} loading={saving} disabled={!validImportRows.length} onClick={() => void submitStudents()}>导入 {validImportRows.length} 行</Button>
                  {importRows.some((row) => row.error) ? <Button icon={<Download size={16} />} onClick={() => downloadCSV("学生导入错误.csv", importRows.filter((row) => row.error).map((row) => ({ row: row.sourceRow, student_no: row.studentNo, name: row.name, class_code: row.classCode, error: row.error ?? "" })))}>下载错误行</Button> : null}
                </Space>
              </>
            ) : data.students.length ? <Alert type="success" showIcon message={`已导入 ${data.students.length} 名学生，可继续或追加导入。`} /> : null}
            {importErrors.length ? <Alert type="warning" showIcon message={`${importErrors.length} 行未导入：${importErrors[0]?.message ?? "请检查名单内容"}`} description={<Button type="link" onClick={() => downloadCSV("导入错误行.csv", importErrors.map((item) => ({ row: item.row, error: item.message })))}>下载错误行</Button>} /> : null}
            {data.students.length ? <Button className="step-next" onClick={() => setStep(4)}>继续 <ArrowRight size={16} /></Button> : null}
          </div>
        );
      case 4:
        return (
          <Form key="user" layout="vertical" preserve={false} onFinish={(values) => void runSave(() => createManagedUser(values), "人员账号已创建")}>
            <h2>人员账号</h2>
            <p className="section-copy">创建第一位教务或阅卷教师账号。请当面告知或通过安全渠道发送初始密码，并提醒对方首次登录后立即修改。</p>
            {data.users.length > 1 ? <Alert type="success" showIcon message={`当前机构已有 ${data.users.length} 个用户`} /> : null}
            <div className="form-grid compact-form-grid">
              <Form.Item name="username" label="登录账号" rules={[{ required: true }]}><Input autoComplete="off" /></Form.Item>
              <Form.Item name="display_name" label="姓名" rules={[{ required: true }]}><Input /></Form.Item>
              <Form.Item name="role_code" label="业务角色" rules={[{ required: true }]}><Select options={data.roles.filter((role) => !["platform_admin", "tenant_admin"].includes(role.code)).map((role) => ({ value: role.code, label: role.description || role.name }))} /></Form.Item>
              <Form.Item name="password" label="初始密码" extra="建议使用至少 15 个字符的长密码短语" rules={[{ required: true }, { validator: validateNewPassword }]}><Input.Password autoComplete="new-password" /></Form.Item>
            </div>
            <Button type="primary" htmlType="submit" loading={saving}>创建并继续</Button>
          </Form>
        );
      case 5:
        return (
          <Form key="exam" layout="vertical" preserve={false} initialValues={{ total_score: 100, grading_mode: "human_review_required", exam_type: "school_exam", publish_policy: "manual", appeal_enabled: true }} onFinish={(values) => void runSave(() => createExam(values), "第一场考试已创建")}>
            <h2>创建第一场考试</h2>
            <p className="section-copy">先建立考试草稿，试卷、题目和准备检查将在考试工作区继续完成。</p>
            {data.examCount ? <Alert type="success" showIcon message={`已有 ${data.examCount} 场考试`} /> : null}
            <div className="form-grid compact-form-grid">
              <Form.Item name="school_id" label="机构" rules={[{ required: true }]}><Select options={data.schools.map((item) => ({ value: item.id, label: item.name }))} /></Form.Item>
              <Form.Item name="name" label="考试名称" rules={[{ required: true }]}><Input placeholder="高一期末考试" /></Form.Item>
              <Form.Item name="subject" label="学科" rules={[{ required: true }]}><Input placeholder="数学" /></Form.Item>
              <Form.Item name="total_score" label="总分" rules={[{ required: true }]}><InputNumber min={1} max={1000} /></Form.Item>
              <Form.Item name="class_ids" label="学生范围" rules={[{ required: true }]}><Select mode="multiple" options={data.classes.map((item) => ({ value: item.id, label: item.name }))} /></Form.Item>
            </div>
            <Form.Item name="exam_type" hidden><Input /></Form.Item><Form.Item name="grading_mode" hidden><Input /></Form.Item><Form.Item name="publish_policy" hidden><Input /></Form.Item><Form.Item name="appeal_enabled" hidden><Input /></Form.Item>
            <Button type="primary" htmlType="submit" loading={saving}>创建考试</Button>
          </Form>
        );
      default:
        return <Result status="success" title="机构启用已完成" subTitle="基础组织、人员和第一场考试已经建立。接下来进入考试工作区完成试卷与阅卷配置。" extra={<Button type="primary" icon={<ArrowRight size={16} />} onClick={() => onNavigate("/exams")}>进入考试</Button>} />;
    }
  })();

  return (
    <div className="page-stack setup-page">
      <section className="page-heading">
        <div><h1>机构启用</h1><p>建立机构基础数据，并从上次完成的位置继续。</p></div>
        <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新进度</Button>
      </section>
      {error ? <Alert type="warning" showIcon message="部分数据刷新失败" description={error} /> : null}
      <div className="setup-progress-line">
        <span>{completed.slice(0, 6).filter(Boolean).length}/6 已完成</span>
        {completed[6] ? <strong><CheckCircle2 size={16} /> 可进入考试配置</strong> : <span>每步提交后进度自动记录，下次可从此处继续</span>}
      </div>
      <div className="onboarding-shell">
        <aside className="onboarding-rail">
          <Steps direction="vertical" current={step} items={setupSteps.map((title, index) => ({ title, status: completed[index] ? "finish" : index === step ? "process" : "wait", disabled: index > 0 && !completed[index - 1] && !completed[index] }))} onChange={setStep} />
        </aside>
        <main className="onboarding-panel">{stepContent}</main>
      </div>
    </div>
  );
}
