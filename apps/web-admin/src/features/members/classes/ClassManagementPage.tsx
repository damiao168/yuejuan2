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
    if (!response.has_more || !response.next_cursor) return { rows, complete: true };
    cursor = response.next_cursor;
  }
  // 现有前端最多读取一万名学生；达到上限后不能把聚合数展示为全量。
  return { rows, complete: false };
}

function gradeBusinessLabel(grade?: Grade) {
  if (!grade) return "班级";
  const startYear = Number(grade.academic_year.split("-")[0]);
  // 年级序号从初一 7、高一 10 起算，用当前学年倒推入学届别，仅用于显示。
  const offset = grade.education_stage === "senior" ? Math.max(grade.level_no - 10, 0) : Math.max(grade.level_no - 7, 0);
  return Number.isFinite(startYear) ? `${grade.name}（${startYear - offset}级）` : grade.name;
}

export function ClassManagementPage() {
  const { message } = App.useApp();
  const [schools, setSchools] = useState<School[]>([]);
  const [grades, setGrades] = useState<Grade[]>([]);
  const [classes, setClasses] = useState<SchoolClass[]>([]);
  const [students, setStudents] = useState<Student[]>([]);
  const [studentCountComplete, setStudentCountComplete] = useState(true);
  const [gradeId, setGradeId] = useState("");
  const [scopeSchoolId, setScopeSchoolId] = useState("");
  const [scopeYear, setScopeYear] = useState("");
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
      setStudents(studentResult.rows);
      setStudentCountComplete(studentResult.complete);
      setGradeId((current) => current || gradeResult.grades.find((item) => item.status === "active")?.id || "");
    } catch (loadError) { setError(getUserErrorMessage(loadError, "年级与班级加载失败")); }
    finally { setLoading(false); }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const selectedGrade = grades.find((item) => item.id === gradeId);
  // 学校范围独立于年级，尚未建年级的学校也要能保持选中并新增年级。
  const currentSchoolId = scopeSchoolId || selectedGrade?.school_id || schools[0]?.id || "";
  const currentYear = scopeYear || selectedGrade?.academic_year || "";
  const visibleGrades = grades.filter((grade) => grade.school_id === currentSchoolId && grade.academic_year === currentYear);
  const visibleClasses = classes.filter((item) => item.grade_id === gradeId);
  const counts = useMemo(() => students.reduce((map, student) => map.set(student.class_id, (map.get(student.class_id) ?? 0) + 1), new Map<string, number>()), [students]);
  const columns: TableColumnsType<SchoolClass> = [
    { title: "班级代码", dataIndex: "code", width: 140 },
    { title: "班级名称", dataIndex: "name" },
    { title: "学生数", width: 130, render: (_, item) => {
      if (studentCountComplete) return `${counts.get(item.id) ?? 0} 人`;
      if (counts.has(item.id)) return `已加载 ${counts.get(item.id)} 人`;
      // 分页统计未完成时，尚未出现的班级不能按零人展示。
      return "未统计全量";
    } },
    { title: "状态", dataIndex: "status", width: 100, render: (status: string) => <StatusTag tone={status === "active" ? "success" : "neutral"}>{status === "active" ? "启用" : "停用"}</StatusTag> }
  ];

  async function submitGrade(values: Pick<Grade, "school_id" | "name" | "level_no" | "academic_year" | "education_stage">) {
    setSaving(true);
    try { const result = await createGrade(values); message.success("年级已新增"); setDialog(null); await load(); setScopeSchoolId(result.grade.school_id); setScopeYear(result.grade.academic_year); setGradeId(result.grade.id); }
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
  return <div className="member-management-page class-management-page">
    <section className="member-management-heading"><div><h1>年级与班级</h1><p>维护考试学生范围所依赖的年级、班级和班级代码。</p></div><div><Button icon={<RefreshCw size={15} />} loading={loading} onClick={() => void load()}>刷新</Button><Button onClick={() => setDialog("grade")}>新增年级</Button><Button type="primary" icon={<Plus size={15} />} disabled={!selectedGrade} onClick={() => setDialog("class")}>新增班级</Button></div></section>
    <div className="class-management-scope" aria-label="班级范围"><label>学校<Select value={currentSchoolId || undefined} options={schools.map((school) => ({ value: school.id, label: school.name }))} onChange={(id) => { setScopeSchoolId(id); setScopeYear(""); setGradeId(grades.find((grade) => grade.school_id === id && grade.status === "active")?.id ?? grades.find((grade) => grade.school_id === id)?.id ?? ""); }} /></label><label>学年<Select value={currentYear || undefined} options={[...new Set(grades.filter((grade) => grade.school_id === currentSchoolId).map((grade) => grade.academic_year))].sort((left, right) => right.localeCompare(left)).map((year) => ({ value: year, label: `${year}学年` }))} onChange={(year) => { setScopeYear(year); setGradeId(grades.find((grade) => grade.school_id === currentSchoolId && grade.academic_year === year)?.id ?? ""); }} /></label><label>年级<Select value={gradeId || undefined} options={visibleGrades.map((grade) => ({ value: grade.id, label: gradeBusinessLabel(grade) }))} onChange={setGradeId} /></label></div>
    <div className="class-management-layout"><aside><h2>当前学年年级</h2>{visibleGrades.map((grade) => <button type="button" key={grade.id} className={grade.id === gradeId ? "active" : ""} onClick={() => setGradeId(grade.id)}><strong>{gradeBusinessLabel(grade)}</strong><span>{grade.academic_year}学年</span></button>)}</aside><section className="member-table-section"><div className="section-head"><div><h2>{gradeBusinessLabel(selectedGrade)}</h2><p>{selectedGrade ? `${schools.find((school) => school.id === selectedGrade.school_id)?.name ?? "当前学校"} · ${selectedGrade.academic_year}学年` : "请选择年级"}</p></div><span>{visibleClasses.length} 个班级</span></div><ResponsiveTable className="dense-data-table" rowKey="id" size="small" columns={columns} dataSource={visibleClasses} pagination={{ pageSize: 20, showSizeChanger: false }} /></section></div>
    <Modal title="新增年级" open={dialog === "grade"} footer={null} destroyOnHidden onCancel={() => setDialog(null)}><Form layout="vertical" onFinish={(values) => void submitGrade(values)} initialValues={{ school_id: currentSchoolId || schools[0]?.id, academic_year: `${new Date().getFullYear()}-${new Date().getFullYear() + 1}`, education_stage: "senior", level_no: 10 }}><Form.Item name="school_id" label="学校" rules={[{ required: true }]}><Select options={schools.map((item) => ({ value: item.id, label: item.name }))} /></Form.Item><Form.Item name="education_stage" label="学段" rules={[{ required: true }]}><Select options={[{ value: "junior", label: "初中" }, { value: "senior", label: "高中" }]} /></Form.Item><Form.Item name="academic_year" label="学年" rules={[{ required: true, message: "请输入学年" }]}><Input /></Form.Item><Form.Item name="name" label="年级名称" rules={[{ required: true, message: "请输入年级名称" }]}><Input placeholder="例如：高二" /></Form.Item><Form.Item name="level_no" label="年级序号" rules={[{ required: true, message: "请输入年级序号" }]}><InputNumber min={1} max={20} /></Form.Item><Button type="primary" htmlType="submit" loading={saving}>新增年级</Button></Form></Modal>
    <Modal title="新增班级" open={dialog === "class"} footer={null} destroyOnHidden onCancel={() => setDialog(null)}><Form layout="vertical" onFinish={(values) => void submitClass(values)} initialValues={{ school_id: selectedGrade?.school_id, grade_id: gradeId }}><Form.Item name="school_id" hidden><Input /></Form.Item><Form.Item name="grade_id" label="所属年级" rules={[{ required: true }]}><Select options={visibleGrades.map((item) => ({ value: item.id, label: `${item.name} · ${item.academic_year}` }))} /></Form.Item><Form.Item name="name" label="班级名称" rules={[{ required: true, message: "请输入班级名称" }]}><Input placeholder="例如：高二（1）班" /></Form.Item><Form.Item name="code" label="班级代码" extra="学生 CSV 导入会使用班级代码进行匹配。" rules={[{ required: true, message: "请输入班级代码" }]}><Input placeholder="例如：G11-01" /></Form.Item><Button type="primary" htmlType="submit" loading={saving}>新增班级</Button></Form></Modal>
  </div>;
}
