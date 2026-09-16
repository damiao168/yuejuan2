import { useCallback, useEffect, useMemo, useState } from "react";
import { App, Button, Form, Input, InputNumber, Modal, Select } from "antd";
import type { TableColumnsType } from "antd";
import { Plus, RefreshCw } from "lucide-react";
import { getUserErrorMessage } from "../../../api/client";
import { createClass, createGrade, listClasses, listGrades, listSchools, listStudents, type Grade, type School, type SchoolClass, type Student } from "../../../api/org";
import { ErrorState, LoadingState } from "../../../components/PageState";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { StatusTag } from "../../../components/StatusTag";

async function allStudents() {
  const rows: Student[] = [];
  let cursor = "";
  for (let page = 0; page < 50; page += 1) {
    const response = await listStudents({ limit: 200, cursor: cursor || undefined });
    rows.push(...response.students);
    if (!response.has_more || !response.next_cursor) break;
    cursor = response.next_cursor;
  }
  return rows;
}

function gradeBusinessLabel(grade?: Grade) {
  if (!grade) return "班级";
  const startYear = Number(grade.academic_year.split("-")[0]);
  const offset = grade.education_stage === "senior" ? Math.max(grade.level_no - 10, 0) : Math.max(grade.level_no - 7, 0);
  return Number.isFinite(startYear) ? `${grade.name}（${startYear - offset}级）` : grade.name;
}

export function ClassManagementPage() {
  const { message } = App.useApp();
  const [schools, setSchools] = useState<School[]>([]);
  const [grades, setGrades] = useState<Grade[]>([]);
  const [classes, setClasses] = useState<SchoolClass[]>([]);
  const [students, setStudents] = useState<Student[]>([]);
  const [gradeId, setGradeId] = useState("");
  const [dialog, setDialog] = useState<"grade" | "class" | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true); setError("");
    try {
      const [schoolResult, gradeResult, classResult, studentResult] = await Promise.all([listSchools(), listGrades(), listClasses(), allStudents()]);
      setSchools(schoolResult.schools.filter((item) => item.status === "active"));
      setGrades(gradeResult.grades);
      setClasses(classResult.classes);
      setStudents(studentResult);
      setGradeId((current) => current || gradeResult.grades.find((item) => item.status === "active")?.id || "");
    } catch (loadError) { setError(getUserErrorMessage(loadError, "年级与班级加载失败")); }
    finally { setLoading(false); }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const selectedGrade = grades.find((item) => item.id === gradeId);
  const visibleClasses = classes.filter((item) => item.grade_id === gradeId);
  const counts = useMemo(() => students.reduce((map, student) => map.set(student.class_id, (map.get(student.class_id) ?? 0) + 1), new Map<string, number>()), [students]);
  const columns: TableColumnsType<SchoolClass> = [
    { title: "班级代码", dataIndex: "code", width: 140 },
    { title: "班级名称", dataIndex: "name" },
    { title: "学生数", width: 110, render: (_, item) => counts.has(item.id) ? `${counts.get(item.id)} 人` : "-" },
    { title: "状态", dataIndex: "status", width: 100, render: (status: string) => <StatusTag tone={status === "active" ? "success" : "neutral"}>{status === "active" ? "启用" : "停用"}</StatusTag> }
  ];

  async function submitGrade(values: Pick<Grade, "school_id" | "name" | "level_no" | "academic_year" | "education_stage">) {
    setSaving(true);
    try { const result = await createGrade(values); message.success("年级已新增"); setDialog(null); await load(); setGradeId(result.grade.id); }
    catch (saveError) { message.error(getUserErrorMessage(saveError, "新增年级失败")); }
    finally { setSaving(false); }
  }
  async function submitClass(values: Pick<SchoolClass, "school_id" | "grade_id" | "name" | "code">) {
    setSaving(true);
    try { await createClass(values); message.success("班级已新增"); setDialog(null); await load(); }
    catch (saveError) { message.error(getUserErrorMessage(saveError, "新增班级失败")); }
    finally { setSaving(false); }
  }

  if (loading && !grades.length) return <LoadingState label="正在加载年级与班级" />;
  if (error && !grades.length) return <ErrorState message={error} onRetry={() => void load()} />;
  return <div className="member-management-page">
    <section className="member-management-heading"><div><h1>年级与班级</h1><p>维护考试学生范围所依赖的年级、班级和班级代码。</p></div><div><Button icon={<RefreshCw size={15} />} loading={loading} onClick={() => void load()}>刷新</Button><Button onClick={() => setDialog("grade")}>新增年级</Button><Button type="primary" icon={<Plus size={15} />} disabled={!grades.length} onClick={() => setDialog("class")}>新增班级</Button></div></section>
    <div className="class-management-layout"><aside><h2>年级 / 届别</h2>{grades.map((grade) => <button type="button" key={grade.id} className={grade.id === gradeId ? "active" : ""} onClick={() => setGradeId(grade.id)}><strong>{gradeBusinessLabel(grade)}</strong><span>{grade.academic_year}学年</span></button>)}</aside><section className="member-table-section"><div className="section-head"><div><h2>{gradeBusinessLabel(selectedGrade)}</h2><p>{selectedGrade ? `${selectedGrade.academic_year}学年` : "请选择年级"}</p></div><span>{visibleClasses.length} 个班级</span></div><ResponsiveTable className="dense-data-table" rowKey="id" size="small" columns={columns} dataSource={visibleClasses} pagination={false} /></section></div>
    <Modal title="新增年级" open={dialog === "grade"} footer={null} destroyOnHidden onCancel={() => setDialog(null)}><Form layout="vertical" onFinish={(values) => void submitGrade(values)} initialValues={{ school_id: schools[0]?.id, academic_year: `${new Date().getFullYear()}-${new Date().getFullYear() + 1}`, education_stage: "senior", level_no: 10 }}><Form.Item name="school_id" label="学校" rules={[{ required: true }]}><Select options={schools.map((item) => ({ value: item.id, label: item.name }))} /></Form.Item><Form.Item name="education_stage" label="学段" rules={[{ required: true }]}><Select options={[{ value: "junior", label: "初中" }, { value: "senior", label: "高中" }]} /></Form.Item><Form.Item name="academic_year" label="学年" rules={[{ required: true, message: "请输入学年" }]}><Input /></Form.Item><Form.Item name="name" label="年级名称" rules={[{ required: true, message: "请输入年级名称" }]}><Input placeholder="例如：高二" /></Form.Item><Form.Item name="level_no" label="年级序号" rules={[{ required: true, message: "请输入年级序号" }]}><InputNumber min={1} max={20} /></Form.Item><Button type="primary" htmlType="submit" loading={saving}>新增年级</Button></Form></Modal>
    <Modal title="新增班级" open={dialog === "class"} footer={null} destroyOnHidden onCancel={() => setDialog(null)}><Form layout="vertical" onFinish={(values) => void submitClass(values)} initialValues={{ school_id: selectedGrade?.school_id, grade_id: gradeId }}><Form.Item name="school_id" hidden><Input /></Form.Item><Form.Item name="grade_id" label="所属年级" rules={[{ required: true }]}><Select options={grades.map((item) => ({ value: item.id, label: `${item.name} · ${item.academic_year}` }))} /></Form.Item><Form.Item name="name" label="班级名称" rules={[{ required: true, message: "请输入班级名称" }]}><Input placeholder="例如：高二（1）班" /></Form.Item><Form.Item name="code" label="班级代码" extra="学生 CSV 导入会使用班级代码进行匹配。" rules={[{ required: true, message: "请输入班级代码" }]}><Input placeholder="例如：G11-01" /></Form.Item><Button type="primary" htmlType="submit" loading={saving}>新增班级</Button></Form></Modal>
  </div>;
}
