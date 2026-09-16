import { useCallback, useEffect, useMemo, useState } from "react";
import { App, Button, Form, Input, Modal, Select, Upload, type TableColumnsType } from "antd";
import { Download, Plus, RefreshCw, Search, Upload as UploadIcon } from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import {
  createStudent,
  importStudentsCSV,
  listClasses,
  listGrades,
  listStudents,
  updateStudentStatus,
  type Grade,
  type SchoolClass,
  type Student
} from "../api/org";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { prepareStudentImport, remapStudentImportError } from "../features/members/students/studentImport";

interface StudentFormValues {
  class_id: string;
  student_no: string;
  name: string;
  gender?: string;
}

function errorText(error: unknown) {
  return getUserErrorMessage(error, "操作失败");
}

function gradeBusinessLabel(grade?: Grade) {
  if (!grade) return "-";
  const startYear = Number(grade.academic_year.split("-")[0]);
  const offset = grade.education_stage === "senior" ? Math.max(grade.level_no - 10, 0) : Math.max(grade.level_no - 7, 0);
  return Number.isFinite(startYear) ? `${grade.name}（${startYear - offset}级）` : grade.name;
}

function downloadTemplate() {
  const content = "student_no,name,class_code\r\n20260001,张同学,G10-01\r\n";
  const url = URL.createObjectURL(new Blob(["\ufeff", content], { type: "text/csv;charset=utf-8" }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = "学生导入模板.csv";
  anchor.click();
  URL.revokeObjectURL(url);
}

async function loadAllStudents() {
  const result: Student[] = [];
  let cursor = "";
  do {
    const page = await listStudents({ limit: 200, cursor: cursor || undefined });
    result.push(...page.students);
    cursor = page.has_more ? page.next_cursor : "";
  } while (cursor);
  return result;
}

export function StudentManagementPage() {
  const { message, modal } = App.useApp();
  const [form] = Form.useForm<StudentFormValues>();
  const [students, setStudents] = useState<Student[]>([]);
  const [grades, setGrades] = useState<Grade[]>([]);
  const [classes, setClasses] = useState<SchoolClass[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [keyword, setKeyword] = useState("");
  const [gradeId, setGradeId] = useState("all");
  const [academicYear, setAcademicYear] = useState("all");
  const [classId, setClassId] = useState("all");
  const [status, setStatus] = useState("active");
  const [createOpen, setCreateOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [csv, setCSV] = useState("");
  const [saving, setSaving] = useState(false);
  const [actioning, setActioning] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [studentResult, gradeResult, classResult] = await Promise.all([
        loadAllStudents(),
        listGrades(),
        listClasses()
      ]);
      setStudents(studentResult);
      setGrades(gradeResult.grades);
      setClasses(classResult.classes);
    } catch (failure) {
      setError(errorText(failure));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const classById = useMemo(() => new Map(classes.map((item) => [item.id, item])), [classes]);
  const gradeById = useMemo(() => new Map(grades.map((item) => [item.id, item])), [grades]);
  const academicYears = useMemo(() => [...new Set(grades.map((item) => item.academic_year))].sort().reverse(), [grades]);
  const visibleGrades = useMemo(() => grades.filter((item) => academicYear === "all" || item.academic_year === academicYear), [academicYear, grades]);
  const visibleClasses = useMemo(() => classes.filter((item) => gradeId === "all" || item.grade_id === gradeId), [classes, gradeId]);
  const filtered = useMemo(() => students.filter((student) => {
    const schoolClass = classById.get(student.class_id);
    const grade = gradeById.get(schoolClass?.grade_id ?? "");
    if (academicYear !== "all" && grade?.academic_year !== academicYear) return false;
    if (gradeId !== "all" && schoolClass?.grade_id !== gradeId) return false;
    if (classId !== "all" && student.class_id !== classId) return false;
    if (status !== "all" && student.status !== status) return false;
    const query = keyword.trim().toLowerCase();
    return !query || student.name.toLowerCase().includes(query) || student.student_no.toLowerCase().includes(query);
  }), [academicYear, classById, classId, gradeById, gradeId, keyword, status, students]);

  const columns: TableColumnsType<Student> = [
    { title: "学号", dataIndex: "student_no", width: 150, ellipsis: true },
    { title: "姓名", dataIndex: "name", width: 130, ellipsis: true },
    { title: "年级 / 届别", width: 180, render: (_, item) => gradeBusinessLabel(gradeById.get(classById.get(item.class_id)?.grade_id ?? "")) },
    { title: "班级", width: 150, render: (_, item) => classById.get(item.class_id)?.name ?? "-" },
    { title: "状态", dataIndex: "status", width: 100, render: (value: string) => <StatusTag tone={value === "active" ? "success" : "neutral"}>{value === "active" ? "在籍" : "停用"}</StatusTag> },
    {
      title: "操作", key: "actions", width: 110, fixed: "right", render: (_, item) => (
        <Button
          type="link"
          size="small"
          loading={actioning === item.id}
          onClick={() => modal.confirm({
            title: item.status === "active" ? "停用学生" : "恢复学生",
            content: item.status === "active" ? `停用后，${item.name} 不会进入新考试的在籍学生范围。` : `恢复 ${item.name} 的在籍状态？`,
            okText: "确认",
            cancelText: "取消",
            onOk: async () => {
              setActioning(item.id);
              try {
                await updateStudentStatus(item.id, item.status === "active" ? "inactive" : "active");
                await load();
              } catch (failure) {
                message.error(errorText(failure));
              } finally {
                setActioning("");
              }
            }
          })}
        >{item.status === "active" ? "停用" : "恢复"}</Button>
      )
    }
  ];

  const submitStudent = async () => {
    const values = await form.validateFields();
    const schoolClass = classById.get(values.class_id);
    if (!schoolClass) return;
    setSaving(true);
    try {
      await createStudent({ ...values, school_id: schoolClass.school_id });
      message.success("学生已添加");
      setCreateOpen(false);
      form.resetFields();
      await load();
    } catch (failure) {
      message.error(errorText(failure));
    } finally {
      setSaving(false);
    }
  };

  const submitImport = async () => {
    if (!csv.trim()) return;
    const prepared = prepareStudentImport(csv, classes);
    if (!prepared.csv) {
      const firstError = prepared.errors[0];
      message.warning(firstError ? `没有可导入的学生：第 ${firstError.row} 行，${firstError.message}` : "没有可导入的学生记录");
      return;
    }
    setSaving(true);
    try {
      const result = await importStudentsCSV(prepared.csv);
      const errors = [
        ...prepared.errors,
        ...result.result.errors.map((item) => remapStudentImportError(item, prepared.sourceRows))
      ].sort((left, right) => left.row - right.row);
      if (errors.length) {
        const firstError = errors[0];
        message.warning(`已导入 ${result.result.created} 名，${errors.length} 行未导入：第 ${firstError.row} 行，${firstError.message}`);
      } else {
        message.success(`已导入 ${result.result.created} 名学生`);
        setImportOpen(false);
        setCSV("");
      }
      await load();
    } catch (failure) {
      message.error(errorText(failure));
    } finally {
      setSaving(false);
    }
  };

  if (loading && !students.length) return <LoadingState label="正在读取学生名册" />;
  if (error && !students.length) return <ErrorState message={error} onRetry={() => void load()} />;

  return (
    <div className="member-management-page student-management-page">
      <section className="member-management-heading">
        <div><h1>学生管理</h1><p>维护日常学生名册；考试创建时直接选择班级范围。</p></div>
        <div>
          <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button>
          <Button icon={<UploadIcon size={16} />} onClick={() => setImportOpen(true)}>导入学生</Button>
          <Button type="primary" icon={<Plus size={16} />} onClick={() => setCreateOpen(true)}>新增学生</Button>
        </div>
      </section>

      <section className="student-management-filters" aria-label="学生筛选">
        <label className="student-filter-field student-filter-year">
          <span>学年</span>
          <Select size="small" value={academicYear} options={[{ value: "all", label: "全部学年" }, ...academicYears.map((value) => ({ value, label: `${value}学年` }))]} onChange={(value) => { setAcademicYear(value); setGradeId("all"); setClassId("all"); }} />
        </label>
        <label className="student-filter-field student-filter-grade">
          <span>年级</span>
          <Select size="small" value={gradeId} options={[{ value: "all", label: "全部年级" }, ...visibleGrades.map((item) => ({ value: item.id, label: gradeBusinessLabel(item) }))]} onChange={(value) => { setGradeId(value); setClassId("all"); }} />
        </label>
        <label className="student-filter-field student-filter-class">
          <span>班级</span>
          <Select size="small" value={classId} options={[{ value: "all", label: "全部班级" }, ...visibleClasses.map((item) => ({ value: item.id, label: item.name }))]} onChange={setClassId} />
        </label>
        <label className="student-filter-field student-filter-status">
          <span>状态</span>
          <Select size="small" value={status} options={[{ value: "active", label: "在籍" }, { value: "inactive", label: "停用" }, { value: "all", label: "全部状态" }]} onChange={setStatus} />
        </label>
        <label className="student-filter-field student-filter-search">
          <span>搜索</span>
          <Input size="small" allowClear prefix={<Search size={15} />} value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder="姓名或学号" />
        </label>
      </section>

      <section className="student-management-table">
        <div className="section-head"><div><h2>学生名册</h2><p>{filtered.length} 名学生</p></div></div>
        <ResponsiveTable className="dense-data-table" rowKey="id" size="small" columns={columns} dataSource={filtered} scroll={{ x: 760 }} pagination={{ size: "small", pageSize: 20, showSizeChanger: true }} locale={{ emptyText: <EmptyState title="暂无学生" description="可新增学生，或下载模板后批量导入。" /> }} />
      </section>

      <Modal title="新增学生" open={createOpen} okText="保存" cancelText="取消" confirmLoading={saving} onOk={() => void submitStudent()} onCancel={() => { setCreateOpen(false); form.resetFields(); }}>
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item name="class_id" label="班级" rules={[{ required: true, message: "请选择班级" }]}><Select showSearch optionFilterProp="label" options={classes.map((item) => ({ value: item.id, label: `${gradeBusinessLabel(gradeById.get(item.grade_id))} · ${item.name}` }))} /></Form.Item>
          <Form.Item name="student_no" label="学号" rules={[{ required: true, message: "请输入学号" }]}><Input maxLength={50} /></Form.Item>
          <Form.Item name="name" label="姓名" rules={[{ required: true, message: "请输入姓名" }]}><Input maxLength={80} /></Form.Item>
          <Form.Item name="gender" label="性别（可选）"><Select allowClear options={[{ value: "male", label: "男" }, { value: "female", label: "女" }, { value: "unknown", label: "未填写" }]} /></Form.Item>
        </Form>
      </Modal>

      <Modal title="批量导入学生" open={importOpen} okText="开始导入" cancelText="取消" okButtonProps={{ disabled: !csv.trim() }} confirmLoading={saving} onOk={() => void submitImport()} onCancel={() => { setImportOpen(false); setCSV(""); }}>
        <div className="student-import-copy"><p>使用 CSV 模板填写学号、姓名和班级代码；班级代码需唯一。导入不会修改已有学生。</p><Button size="small" icon={<Download size={15} />} onClick={downloadTemplate}>下载模板</Button></div>
        <Upload.Dragger accept=".csv,text/csv" maxCount={1} beforeUpload={async (file) => { setCSV(await file.text()); return false; }} onRemove={() => { setCSV(""); return true; }}><UploadIcon size={22} /><p>选择或拖入 CSV 文件</p></Upload.Dragger>
      </Modal>
    </div>
  );
}
