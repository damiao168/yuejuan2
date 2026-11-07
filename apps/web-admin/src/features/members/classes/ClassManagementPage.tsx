import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Form, Input, InputNumber, Modal, Select, Space, type TableColumnsType } from "antd";
import { Plus, RefreshCw, Trash2 } from "lucide-react";
import { getUserErrorMessage } from "../../../api/client";
import { createClass, createGrade, deleteClass, deleteGrade, updateClass, updateGrade, listAcademicYears, listClasses, listGrades, listSchools, listStudents, type AcademicYear, type Grade, type School, type SchoolClass, type Student } from "../../../api/org";
import type { SessionUser } from "../../../auth/session";
import { ErrorState, LoadingState } from "../../../components/PageState";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { StatusTag } from "../../../components/StatusTag";
import { classCohortLabel, classLocalName, gradeCohortGroups, gradeCohortKey, gradeCohortLabel, gradeCurrentLevelLabel, gradeCurrentLevelNumber, gradeEntryYear, gradeLevelOptions } from "../gradeCohortPresentation";
import "../../../styles/features/class-management-cohort.css";
import { currentGradeSchoolYear, expectedGradeLevel, gradeCreationIssue, gradeEditIssue, gradeSchoolYearHint } from "../gradeCreationValidation";

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

type EditTarget = { kind: "grade"; value: Grade } | { kind: "class"; value: SchoolClass };
interface GradeValues { school_id: string; education_stage: Grade["education_stage"]; entry_year: number; level_no: number }
interface EditValues { name?: string; code?: string; level_no?: number }

export function ClassManagementPage({ currentUser }: Readonly<{ currentUser: SessionUser }>) {
  const { message, modal } = App.useApp();
  const canChooseSchool = currentUser.roles.some(role => ["platform_admin", "tenant_admin"].includes(role));
  // 学校管理员固定使用认证后的组织范围；不能拿跨校列表的第一条代替本校。
  const fixedSchoolId = currentUser.organizationScope.resolved && currentUser.organizationScope.schoolIds.length === 1 ? currentUser.organizationScope.schoolIds[0] : "";
  const scopeKey = `${currentUser.tenant}:${currentUser.id}:${canChooseSchool}:${fixedSchoolId}`;
  const loadVersion = useRef(0);
  const currentScope = useRef(scopeKey);
  if (currentScope.current !== scopeKey) { currentScope.current = scopeKey; loadVersion.current++; }
  const [schools, setSchools] = useState<School[]>([]);
  const [grades, setGrades] = useState<Grade[]>([]);
  const [academicYears, setAcademicYears] = useState<AcademicYear[]>([]);
  const [classes, setClasses] = useState<SchoolClass[]>([]);
  const [students, setStudents] = useState<Student[]>([]);
  const [studentCountComplete, setStudentCountComplete] = useState(true);
  const [cohortKey, setCohortKey] = useState("");
  const [scopeSchoolId, setScopeSchoolId] = useState("");
  const [dialog, setDialog] = useState<"grade" | "class" | null>(null);
  const [editTarget, setEditTarget] = useState<EditTarget | null>(null);
  const [editForm] = Form.useForm<EditValues>();
  const [gradeForm] = Form.useForm<GradeValues>();
  const [classForm] = Form.useForm<Pick<SchoolClass, "school_id" | "grade_id" | "name" | "code">>();
  const gradeStage = Form.useWatch("education_stage", gradeForm) ?? "senior";
  const gradeYear = Form.useWatch("entry_year", gradeForm);
  const gradeSchool = Form.useWatch("school_id", gradeForm);
  const editedCode = Form.useWatch("code", editForm);
  const editedName = Form.useWatch("name", editForm);
  const editedLevel = Form.useWatch("level_no", editForm);
  const newClassName = Form.useWatch("name", classForm);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");

  // biome-ignore lint/correctness/useExhaustiveDependencies: 身份是读取边界，即使学校相同，换账号也必须废弃旧结果。
  const load = useCallback(async () => {
    const version = ++loadVersion.current;
    setLoading(true); setError("");
    try {
      const [schoolResult, gradeResult, classResult, studentResult, yearResult] = await Promise.all([listSchools(), listGrades(canChooseSchool ? undefined : fixedSchoolId || undefined), listClasses(), allStudents(), listAcademicYears(canChooseSchool ? undefined : fixedSchoolId || undefined)]);
      if (version !== loadVersion.current) return;
      setSchools(schoolResult.schools.filter(item => item.status === "active"));
      setGrades(gradeResult.grades);
      setAcademicYears(yearResult.academic_years);
      setClasses(classResult.classes);
      setStudents(studentResult.rows);
      setStudentCountComplete(studentResult.complete);
    } catch (loadError) { if (version === loadVersion.current) setError(getUserErrorMessage(loadError, "年级与班级加载失败")); }
    finally { if (version === loadVersion.current) setLoading(false); }
  }, [canChooseSchool, fixedSchoolId, scopeKey]);
  useEffect(() => { void load(); return () => { loadVersion.current++; }; }, [load]);

  const currentSchoolId = canChooseSchool ? (schools.some(school => school.id === scopeSchoolId) ? scopeSchoolId : schools[0]?.id || "") : fixedSchoolId;
  const creationSchoolId = canChooseSchool ? gradeSchool || currentSchoolId : currentSchoolId;
  const creationYear = currentGradeSchoolYear(academicYears, creationSchoolId);
  const calculatedLevel = expectedGradeLevel(gradeStage, gradeYear, creationYear);
  const newGradeIssue = gradeCreationIssue({ schoolId: creationSchoolId, stage: gradeStage, entryYear: gradeYear, schoolYear: creationYear, grades, ready: !loading && !error });
  const editedGrade = editTarget?.kind === "grade" ? editTarget.value : undefined;
  const editSchoolYear = currentGradeSchoolYear(academicYears, editedGrade?.school_id ?? currentSchoolId);
  const expectedEditLevel = editedGrade ? expectedGradeLevel(editedGrade.education_stage, gradeEntryYear(editedGrade) ?? 0, editSchoolYear) : undefined;
  const editIssue = editedGrade ? gradeEditIssue({ schoolId: editedGrade.school_id, stage: editedGrade.education_stage, entryYear: gradeEntryYear(editedGrade), schoolYear: editSchoolYear, grades, ready: !loading && !error, grade: editedGrade, level: editedLevel }) : undefined;
  useEffect(() => { if (dialog === "grade") gradeForm.setFieldsValue({ level_no: calculatedLevel }); }, [dialog, calculatedLevel, gradeForm]);
  const visibleGroups = useMemo(() => gradeCohortGroups(grades.filter(grade => grade.school_id === currentSchoolId)), [grades, currentSchoolId]);
  const selectedGroup = visibleGroups.find(group => group.key === cohortKey) ?? visibleGroups[0];
  const selectedGrade = selectedGroup?.grade;
  const visibleClasses = classes.filter(item => item.school_id === currentSchoolId && selectedGroup?.gradeIds.includes(item.grade_id));
  const counts = useMemo(() => students.reduce((map, student) => map.set(student.class_id, (map.get(student.class_id) ?? 0) + 1), new Map<string, number>()), [students]);
  const currentSchoolName = schools.find(school => school.id === currentSchoolId)?.name || "本校";
  const columns: TableColumnsType<SchoolClass> = [
    { title: "班级代码", dataIndex: "code", width: 140 },
    { title: "班级名称", render: (_, item) => classCohortLabel(item, selectedGrade) },
    { title: "学生数", width: 130, render: (_, item) => {
      if (studentCountComplete) return `${counts.get(item.id) ?? 0} 人`;
      if (counts.has(item.id)) return `已加载 ${counts.get(item.id)} 人`;
      return "未统计全量";
    } },
    { title: "状态", dataIndex: "status", width: 100, render: (status: string) => <StatusTag tone={status === "active" ? "success" : "neutral"}>{status === "active" ? "启用" : "停用"}</StatusTag> },
    { title: "操作", key: "actions", width: 150, render: (_, item) => <Space wrap><Button type="link" size="small" disabled={saving} onClick={() => openEdit({ kind: "class", value: item })}>编辑</Button><Button type="link" danger size="small" disabled={saving} onClick={() => confirmDelete({ kind: "class", value: item })}>删除</Button></Space> }
  ];

  function openEdit(target: EditTarget) {
    editForm.resetFields();
    editForm.setFieldsValue({ name: target.kind === "class" ? classLocalName(target.value) : undefined, code: target.kind === "class" ? target.value.code : undefined, level_no: target.kind === "grade" ? gradeCurrentLevelNumber(target.value) : undefined });
    setEditTarget(target);
  }

  async function saveEdit(values: EditValues) {
    if (!editTarget || saving) return;
    if (editTarget.kind === "grade") {
      const issue = gradeEditIssue({ schoolId: editTarget.value.school_id, stage: editTarget.value.education_stage, entryYear: gradeEntryYear(editTarget.value), schoolYear: editSchoolYear, grades, ready: !loading && !error, grade: editTarget.value, level: values.level_no });
      if (issue) { message.error(issue.message); return; }
    }
    setSaving(true); setActionError("");
    try {
      if (editTarget.kind === "grade") {
        // 当前年级只修改最新学年代表记录；固定届别及历史班级、学生和考试绑定保持原记录。
        const result = await updateGrade(editTarget.value.id, { level_no: values.level_no });
        setGrades(current => current.map(item => item.id === result.grade.id ? result.grade : item));
      } else {
        const result = await updateClass(editTarget.value.id, { name: classLocalName({ name: values.name ?? "" }), code: values.code?.trim() ?? "" });
        setClasses(current => current.map(item => item.id === result.class.id ? result.class : item));
      }
      message.success(editTarget.kind === "grade" ? "年级信息已修改" : "班级信息已修改");
      setEditTarget(null);
    } catch (reason) { message.error(getUserErrorMessage(reason, "修改失败，填写内容已保留")); if (editTarget.kind === "grade") await load(); }
    finally { setSaving(false); }
  }

  function confirmDelete(target: EditTarget) {
    const kind = target.kind === "grade" ? "年级" : "班级";
    const label = target.kind === "grade" ? gradeCohortLabel(target.value) : classCohortLabel(target.value, selectedGrade);
    const scope = scopeKey;
    modal.confirm({
      title: `删除${kind}“${label}”？`,
      content: target.kind === "grade" ? "将删除该届别的年级。仅可删除整个届别都没有班级的空年级，系统会在删除时再次核对。" : "仅可删除没有学生的空班级，停用学生也计入；请先调整学生归属。",
      okText: `删除${kind}`, okButtonProps: { danger: true }, cancelText: "取消",
      onOk: async () => {
        if (currentScope.current !== scope) throw new Error("当前账号范围已变化，请重新核对后删除");
        setSaving(true); setActionError("");
        try {
          // 列表学生数可能不全或刚发生转班；是否为空由后端事务核验，不能凭前端计数承诺可删。
          if (target.kind === "grade") await deleteGrade(target.value.id); else await deleteClass(target.value.id);
          if (currentScope.current !== scope) return;
          message.success(`${kind}已删除`);
          await load();
        } catch (reason) {
          const text = getUserErrorMessage(reason, `删除${kind}失败，请刷新后重试`);
          if (currentScope.current === scope) { setActionError(text); message.error(text); }
          throw reason;
        } finally { if (currentScope.current === scope) setSaving(false); }
      }
    });
  }

  async function submitGrade(values: GradeValues) {
    if (!currentSchoolId || saving) return;
    const schoolId = canChooseSchool ? values.school_id : currentSchoolId;
    const schoolYear = currentGradeSchoolYear(academicYears, schoolId);
    const issue = gradeCreationIssue({ schoolId, stage: values.education_stage, entryYear: values.entry_year, schoolYear, grades, ready: !loading && !error });
    if (issue) { message.error(issue.message); return; }
    const level = expectedGradeLevel(values.education_stage, values.entry_year, schoolYear)!;
    setSaving(true);
    try {
      const result = await createGrade({ ...values, level_no: level, school_id: schoolId, name: gradeCurrentLevelLabel({ level_no: level, name: "" }) });
      message.success("年级已新增"); setDialog(null); await load(); setScopeSchoolId(result.grade.school_id); setCohortKey(gradeCohortKey(result.grade));
    } catch (saveError) { message.error(getUserErrorMessage(saveError, "新增年级失败")); await load(); }
    finally { setSaving(false); }
  }
  async function submitClass(values: Pick<SchoolClass, "school_id" | "grade_id" | "name" | "code">) {
    if (!selectedGrade || saving) return;
    setSaving(true);
    try { await createClass({ ...values, name: classLocalName({ name: values.name }), school_id: selectedGrade.school_id, grade_id: selectedGrade.id }); message.success("班级已新增"); setDialog(null); await load(); }
    catch (saveError) { message.error(getUserErrorMessage(saveError, "新增班级失败")); }
    finally { setSaving(false); }
  }

  if (loading && !grades.length) return <LoadingState label="正在加载年级与班级" />;
  if (error && !grades.length) return <ErrorState message={error} onRetry={() => void load()} />;
  if (!canChooseSchool && !fixedSchoolId) return <ErrorState message="当前账号尚未确定本校管理范围，请重新登录或联系管理员核对。" onRetry={() => void load()} />;
  return <div className="member-management-page class-management-page class-cohort-page">
    <section className="member-management-heading"><div><h1>年级与班级</h1><p>按固定入学届别管理班级；当前年级按学校学年对应校正。</p></div><Space wrap><Button icon={<RefreshCw size={15} />} loading={loading} onClick={() => void load()}>刷新</Button><Button disabled={!currentSchoolId || saving || loading || Boolean(error)} onClick={() => { gradeForm.resetFields(); gradeForm.setFieldsValue({ school_id: currentSchoolId, entry_year: currentGradeSchoolYear(academicYears, currentSchoolId), education_stage: "senior" }); setDialog("grade"); }}>新增年级</Button><Button type="primary" icon={<Plus size={15} />} disabled={!selectedGrade || saving} onClick={() => { classForm.resetFields(); setDialog("class"); }}>新增班级</Button></Space></section>
    {error ? <Alert type="warning" showIcon message={error} /> : null}
    {actionError ? <Alert type="error" showIcon message={actionError} /> : null}
    <div className="class-cohort-toolbar" aria-label="班级范围">
      {canChooseSchool ? <label>学校<Select aria-label="学校" value={currentSchoolId || undefined} options={schools.map(school => ({ value: school.id, label: school.name }))} onChange={id => { setScopeSchoolId(id); setCohortKey(""); }} /></label> : null}
      <label>届别年级<Select aria-label="届别年级" value={selectedGroup?.key} placeholder="请选择届别年级" options={visibleGroups.map(group => ({ value: group.key, label: gradeCohortLabel(group.grade) }))} onChange={setCohortKey} /></label>
      <span className="muted-cell">{visibleClasses.length} 个班级</span>
    </div>
    <section className="member-table-section class-cohort-table"><div className="section-head"><div><h2>{gradeCohortLabel(selectedGrade)}</h2><p>{selectedGrade ? `${currentSchoolName} · 入学届别保持不变` : "请先新增年级"}</p></div>{selectedGrade ? <Space wrap><Button size="small" disabled={saving} onClick={() => openEdit({ kind: "grade", value: selectedGrade })}>编辑年级</Button><Button danger size="small" icon={<Trash2 size={14} />} disabled={saving} onClick={() => confirmDelete({ kind: "grade", value: selectedGrade })}>删除年级</Button></Space> : null}</div><ResponsiveTable className="dense-data-table" rowKey="id" size="small" columns={columns} dataSource={visibleClasses} pagination={{ pageSize: 20, showSizeChanger: false }} /></section>
    <Modal title={editTarget?.kind === "grade" ? "编辑年级" : "编辑班级"} open={Boolean(editTarget)} okText="保存" cancelText="取消" confirmLoading={saving} maskClosable={!saving} closable={!saving} okButtonProps={{ disabled: Boolean(editIssue) }} cancelButtonProps={{ disabled: saving }} onOk={() => editForm.submit()} onCancel={() => { if (!saving) setEditTarget(null); }}>
      <Form name="organization-edit" form={editForm} layout="vertical" disabled={saving} requiredMark={false} onFinish={values => void saveEdit(values)}>
        {editTarget?.kind === "grade" ? <Form.Item label="入学届别"><Input aria-label="入学届别" readOnly value={`${gradeEntryYear(editTarget.value) ?? "未设置"}级`} /></Form.Item> : <Form.Item label="所属届别年级"><Input aria-label="所属届别年级" readOnly value={gradeCohortLabel(selectedGrade)} /></Form.Item>}
        {editTarget?.kind === "grade" ? <><Alert type={editIssue ? "warning" : "info"} showIcon message={editIssue?.message ?? gradeSchoolYearHint(editSchoolYear, editTarget.value.education_stage)} /><Form.Item name="level_no" label="当前年级" extra="按当前学校学年校正；入学届别、班级和学生归属保持不变。" rules={[{ required: true, message: "请选择当前年级" }]}><Select options={gradeLevelOptions(editTarget.value.education_stage).map(option => ({ ...option, disabled: option.value !== expectedEditLevel }))} /></Form.Item></> : null}
        {editTarget?.kind === "class" ? <Form.Item name="name" label="班号或自定义班名" rules={[{ required: true, whitespace: true, message: "请输入班号或班名" }]}><Input maxLength={120} placeholder="例如：1班、实验班" /></Form.Item> : null}
        <Form.Item label="显示名称"><Input aria-label="显示名称" readOnly value={editTarget?.kind === "grade" ? gradeCohortLabel({ ...editTarget.value, level_no: editedLevel ?? editTarget.value.level_no }) : `${gradeCohortLabel(selectedGrade)} · ${editedName?.trim() ? classLocalName({ name: editedName }) : "待填写班名"}`} /></Form.Item>
        {editTarget?.kind === "class" ? <Form.Item name="code" label="班级代码" extra={editedCode !== undefined && editedCode.trim() !== editTarget.value.code ? "班级代码已改，后续学生导入文件请使用新代码。" : undefined} rules={[{ required: true, whitespace: true, message: "请输入班级代码" }]}><Input maxLength={64} /></Form.Item> : null}
      </Form>
    </Modal>
    <Modal title="新增年级" open={dialog === "grade"} footer={null} destroyOnHidden onCancel={() => { if (!saving) setDialog(null); }}><Form name="organization-create-grade" form={gradeForm} layout="vertical" disabled={saving} onFinish={values => void submitGrade(values)} initialValues={{ school_id: currentSchoolId, entry_year: creationYear, education_stage: "senior" }}>
      <Alert type={newGradeIssue ? "warning" : "info"} showIcon message={newGradeIssue ? `当前${creationYear}—${creationYear + 1}学年。${newGradeIssue.message}` : gradeSchoolYearHint(creationYear, gradeStage)} />
      <Form.Item name="school_id" label="学校" hidden={!canChooseSchool} rules={[{ required: true }]}><Select options={schools.map(item => ({ value: item.id, label: item.name }))} /></Form.Item>
      <Form.Item name="education_stage" label="学段" rules={[{ required: true }]}><Select options={[{ value: "junior", label: "初中" }, { value: "senior", label: "高中" }]} /></Form.Item>
      <Form.Item name="entry_year" label="入学年份（届别）" extra="例如2026级，此届别不会随年份或当前年级自动变化。" rules={[{ required: true, message: "请填写入学年份" }, { type: "integer", min: 1900, max: 2100, message: "请填写1900至2100之间的入学年份" }]}><InputNumber min={1900} max={2100} precision={0} /></Form.Item>
      <Form.Item name="level_no" label="当前年级（自动）" extra="由入学年份与当前学校学年计算，不能另选一个矛盾的年级。" rules={[{ required: true, message: "请填写当前在读范围内的入学年份" }]}><Select disabled options={gradeLevelOptions(gradeStage)} placeholder="入学年份不在当前在读范围" /></Form.Item>
      <Form.Item label="显示名称"><Input aria-label="显示名称" readOnly value={`${gradeYear ?? creationYear}级 · ${calculatedLevel ? gradeCurrentLevelLabel({ level_no: calculatedLevel, name: "" }) : "非当前在读年级"}`} /></Form.Item><Button type="primary" htmlType="submit" loading={saving} disabled={Boolean(newGradeIssue)}>新增年级</Button>
    </Form></Modal>
    <Modal title="新增班级" open={dialog === "class"} footer={null} destroyOnHidden onCancel={() => { if (!saving) setDialog(null); }}><Form name="organization-create-class" form={classForm} layout="vertical" disabled={saving} onFinish={values => void submitClass(values)} initialValues={{ school_id: selectedGrade?.school_id, grade_id: selectedGrade?.id }}>
      <Form.Item label="所属届别年级"><Input aria-label="所属届别年级" readOnly value={gradeCohortLabel(selectedGrade)} /></Form.Item><Form.Item name="school_id" hidden><Input /></Form.Item><Form.Item name="grade_id" hidden><Input /></Form.Item>
      <Form.Item name="name" label="班号或自定义班名" rules={[{ required: true, whitespace: true, message: "请输入班号或班名" }]}><Input placeholder="例如：1班、实验班" maxLength={120} /></Form.Item><Form.Item label="显示名称"><Input aria-label="显示名称" readOnly value={`${gradeCohortLabel(selectedGrade)} · ${newClassName?.trim() ? classLocalName({ name: newClassName }) : "待填写班名"}`} /></Form.Item><Form.Item name="code" label="班级代码" extra="用于匹配学生导入文件中的班级代码。" rules={[{ required: true, message: "请输入班级代码" }]}><Input placeholder="例如：2026-01" /></Form.Item><Button type="primary" htmlType="submit" loading={saving}>新增班级</Button>
    </Form></Modal>
  </div>;
}
