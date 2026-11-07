import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Form, Input, Modal, Segmented, Select, Space, Upload, type TableColumnsType } from "antd";
import { Download, Plus, RefreshCw, Search, Upload as UploadIcon } from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import {
  createStudent,
  deleteStudent,
  importStudentsCSV,
  listClasses,
  listGrades,
  listStudents,
  openStudentAccount,
  updateStudentStatus,
  updateStudent,
  transferStudent,
  type Grade,
  type SchoolClass,
  type Student,
  type StudentPasswordInput,
  type StudentImportError
} from "../api/org";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { isStepUpCancelledError, useStepUp } from "../auth/stepUpContext";
import type { SessionUser } from "../auth/session";
import { classCohortLabel, gradeCohortGroups, gradeCohortKey, gradeCohortLabel } from "../features/members/gradeCohortPresentation";
import { prepareStudentImport, remapStudentImportError, studentImportColumns, studentImportErrorsCSV, type StudentImportMapping } from "../features/members/students/studentImport";
import { readStudentImportWorkbook, type StudentImportSheet } from "../features/members/students/studentImportWorkbook";
import "../styles/features/student-management-cohort.css";

interface StudentFormValues {
  class_id: string;
  student_no: string;
  name: string;
  gender?: string;
  password?: string;
  password_confirm?: string;
}

function StudentPasswordFields() {
  return <>
    <Form.Item name="password" label="初始密码" extra="至少 8 个字符，由管理员安全交付给学生。首次登录必须修改。" rules={[
      { required: true, message: "请输入初始密码" },
      { validator: async (_, value: string | undefined) => {
        if (value && (Array.from(value).length < 8 || !value.trim() || new TextEncoder().encode(value).length > 1024)) throw new Error("密码至少需要 8 个字符，且不能全部为空格");
      } }
    ]}><Input.Password autoComplete="new-password" maxLength={1024} /></Form.Item>
    <Form.Item name="password_confirm" label="确认密码" dependencies={["password"]} rules={[
      { required: true, message: "请再次输入初始密码" },
      ({ getFieldValue }) => ({ validator: async (_, value: string | undefined) => {
        if (value && value !== getFieldValue("password")) throw new Error("两次输入的密码不一致");
      } })
    ]}><Input.Password autoComplete="new-password" maxLength={1024} /></Form.Item>
  </>;
}

function errorText(error: unknown) {
  return getUserErrorMessage(error, "操作失败");
}

function downloadTemplate() {
  const content = "学号,姓名,班级代码\r\n00123,张同学,G10-01\r\n";
  const url = URL.createObjectURL(new Blob(["\ufeff", content], { type: "text/csv;charset=utf-8" }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = "学生导入模板.csv";
  anchor.click();
  URL.revokeObjectURL(url);
}

function downloadImportErrors(errors: StudentImportError[]) {
  const content = studentImportErrorsCSV(errors);
  const url = URL.createObjectURL(new Blob(["\ufeff", content], { type: "text/csv;charset=utf-8" }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = "学生导入错误.csv";
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

export function StudentManagementPage({ currentUser }: { currentUser?: SessionUser }) {
  const { message, modal } = App.useApp();
  const { runWithStepUp } = useStepUp();
  const [form] = Form.useForm<StudentFormValues>();
  const [accountForm] = Form.useForm<StudentPasswordInput>();
  const [students, setStudents] = useState<Student[]>([]);
  const [grades, setGrades] = useState<Grade[]>([]);
  const [classes, setClasses] = useState<SchoolClass[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [keyword, setKeyword] = useState("");
  const [cohortKey, setCohortKey] = useState("all");
  const [classId, setClassId] = useState("all");
  const [status, setStatus] = useState("all");
  const [createOpen, setCreateOpen] = useState(false);
  const [editingStudent, setEditingStudent] = useState<Student | null>(null);
  const [accountStudent, setAccountStudent] = useState<Student | null>(null);
  const [transferringStudent, setTransferringStudent] = useState<Student | null>(null);
  const [transferClassId, setTransferClassId] = useState<string>();
  const [importOpen, setImportOpen] = useState(false);
  const [csv, setCSV] = useState("");
  const [importReading, setImportReading] = useState(false);
  const [importSheets, setImportSheets] = useState<StudentImportSheet[]>([]);
  const [importSheetIndex, setImportSheetIndex] = useState(0);
  const importRead = useRef<AbortController | null>(null);
  const importMutation = useRef(false);
  const importAttempt = useRef<{ csv: string; commandId: string } | null>(null);
  const importGeneration = useRef(0);
  useEffect(() => () => { importGeneration.current += 1; importRead.current?.abort(); }, []);
  function resetImportFile() {
    if (importMutation.current) return false;
    importGeneration.current += 1;
    importRead.current?.abort(); importRead.current = null;
    importAttempt.current = null;
    setImportReading(false); setImportSheets([]); setImportSheetIndex(0); setCSV(""); setImportResult(null);
    return true;
  }
  function applyImportSheet(sheet: StudentImportSheet, index: number) {
    if (importMutation.current) return;
    importGeneration.current += 1;
    setCSV(sheet.csv); setImportMapping(studentImportColumns(sheet.csv).mapping);
    setImportSheetIndex(index); setImportClassID(""); setImportResult(null);
  }
  async function readImportFile(file: File) {
    if (!resetImportFile()) return false;
    const controller = new AbortController(); importRead.current = controller; setImportReading(true);
    try {
      if (/\.xlsx$/i.test(file.name)) {
        const sheets = await readStudentImportWorkbook(file, controller.signal);
        if (controller.signal.aborted) return false;
        if (!sheets.length) throw new Error("Excel文件中没有可读取的工作表");
        setImportSheets(sheets); applyImportSheet(sheets[0], 0);
      } else {
        if (!/\.csv$/i.test(file.name)) throw new Error("请选择 .xlsx 或 CSV 文件；旧版 .xls 请另存为 .xlsx");
        if (file.size > 4 * 1024 * 1024) throw new Error("CSV名单不能超过4MB，请分批导入");
        const text = await file.text();
        if (!controller.signal.aborted) applyImportSheet({ name: file.name, csv: text }, 0);
      }
    } catch (cause) {
      if (!controller.signal.aborted) message.error(getUserErrorMessage(cause, "名单读取失败，请检查文件后重试"));
    } finally { if (importRead.current === controller) setImportReading(false); }
    return false;
  }
  const [importMapping, setImportMapping] = useState<StudentImportMapping>({ studentNo: "", name: "", classCode: "", schoolID: "", classID: "" });
  const [importClassID, setImportClassID] = useState("");
  const importHeaders = useMemo(() => csv.trim() ? studentImportColumns(csv).headers : [], [csv]);
  const [importResult, setImportResult] = useState<{ created: number; skipped: number; committed: boolean; errors: StudentImportError[] } | null>(null);
  const [saving, setSaving] = useState(false);
  const studentMutation = useRef(false);
  const [actioning, setActioning] = useState("");
  const loadGeneration = useRef(0);
  const restrictToSchools = Boolean(currentUser && !currentUser.organizationScope.tenantWide && !currentUser.roles.includes("platform_admin") && !currentUser.roles.includes("tenant_admin"));
  const schoolScopeKey = currentUser?.organizationScope.resolved ? [...currentUser.organizationScope.schoolIds].sort().join(",") : "";

  const load = useCallback(async () => {
    const generation = ++loadGeneration.current;
    setLoading(true);
    setError("");
    try {
      const [studentResult, gradeResult, classResult] = await Promise.all([
        loadAllStudents(),
        listGrades(),
        listClasses()
      ]);
      if (generation !== loadGeneration.current) return;
      // 账户范围来自已认证会话；学校管理员的表格、导入和转班候选共用同一批本校资料。
      const schools = new Set(schoolScopeKey.split(",").filter(Boolean));
      const inScope = (item: { school_id: string }) => !restrictToSchools || schools.has(item.school_id);
      setStudents(studentResult.filter(inScope));
      setGrades(gradeResult.grades.filter(inScope));
      setClasses(classResult.classes.filter(inScope));
    } catch (error_) {
      if (generation === loadGeneration.current) setError(errorText(error_));
    } finally {
      if (generation === loadGeneration.current) setLoading(false);
    }
  }, [restrictToSchools, schoolScopeKey]);

  useEffect(() => { void load(); }, [load]);

  const classById = useMemo(() => new Map(classes.map((item) => [item.id, item])), [classes]);
  const gradeById = useMemo(() => new Map(grades.map((item) => [item.id, item])), [grades]);
  const cohortGrades = useMemo(() => gradeCohortGroups(grades), [grades]);
  const currentGradeForClass = (schoolClass?: SchoolClass) => {
    const grade = gradeById.get(schoolClass?.grade_id ?? "");
    return grade ? cohortGrades.find((group) => group.key === gradeCohortKey(grade))?.grade ?? grade : undefined;
  };
  const visibleClasses = useMemo(() => classes.filter((item) => {
    const grade = gradeById.get(item.grade_id);
    return cohortKey === "all" || Boolean(grade && gradeCohortKey(grade) === cohortKey);
  }), [classes, cohortKey, gradeById]);
  const classOptions = visibleClasses.map((item) => {
    const grade = currentGradeForClass(item);
    const label = classCohortLabel(item, grade);
    // 不同届别可能同读高二；全部届别下重名班级要带固定届别，避免选中同名的另一个班。
    const repeated = visibleClasses.some(other => other.id !== item.id && classCohortLabel(other, currentGradeForClass(other)) === label);
    return { value: item.id, label: repeated ? `${gradeCohortLabel(grade)} · ${label}` : label };
  });
  useEffect(() => {
    // 刷新或换届别后不能保留已隐藏的班级条件，否则看似“全部班级”仍可能筛成空表。
    if (classId !== "all" && !visibleClasses.some((item) => item.id === classId)) setClassId("all");
    if (cohortKey !== "all" && !cohortGrades.some((group) => group.key === cohortKey)) setCohortKey("all");
  }, [classId, cohortGrades, cohortKey, visibleClasses]);
  const importPreview = useMemo(() => csv.trim() ? prepareStudentImport(csv, classes, importMapping, importClassID) : null, [csv, classes, importMapping, importClassID]);
  const filtered = useMemo(() => students.filter((student) => {
    const schoolClass = classById.get(student.class_id);
    const grade = gradeById.get(schoolClass?.grade_id ?? "");
    if (cohortKey !== "all" && (!grade || gradeCohortKey(grade) !== cohortKey)) return false;
    if (classId !== "all" && student.class_id !== classId) return false;
    if (status !== "all" && student.status !== status) return false;
    const query = keyword.trim().toLowerCase();
    return !query || student.name.toLowerCase().includes(query) || student.student_no.toLowerCase().includes(query);
  }), [classById, classId, cohortKey, gradeById, keyword, status, students]);

  const columns: TableColumnsType<Student> = [
    { title: "学号", dataIndex: "student_no", width: 150, ellipsis: true },
    { title: "姓名", dataIndex: "name", width: 130, ellipsis: true },
    { title: "届别年级", width: 180, render: (_, item) => gradeCohortLabel(currentGradeForClass(classById.get(item.class_id))) },
    { title: "班级", width: 150, render: (_, item) => { const schoolClass = classById.get(item.class_id); return schoolClass ? classCohortLabel(schoolClass, currentGradeForClass(schoolClass)) : "-"; } },
    { title: "状态", dataIndex: "status", width: 100, render: (value: string) => <StatusTag tone={value === "active" ? "success" : "neutral"}>{value === "active" ? "在籍" : "已停用"}</StatusTag> },
    { title: "账号状态", width: 200, render: (_, item) => {
      const accountStatus = item.account_status ?? "not_provisioned";
      return <div className="student-account-state">
        <StatusTag tone={accountStatus === "active" ? "success" : accountStatus === "disabled" ? "neutral" : "warning"}>{accountStatus === "active" ? "已启用" : accountStatus === "disabled" ? "已停用" : "未开通"}</StatusTag>
        {item.login_school_code ? <span className="student-account-meta">学校代码 {item.login_school_code}{item.must_change_password ? " · 首次登录需改密" : ""}</span> : null}
      </div>;
    } },
    {
      title: "操作", key: "actions", width: 330, fixed: "right", render: (_, item) => {
        const busy = loading || saving || Boolean(actioning);
        return (
        <Space size={0} wrap className="student-row-actions">
        <Button type="link" size="small" disabled={busy} onClick={() => {
          setEditingStudent(item);
          form.resetFields();
          form.setFieldsValue({ class_id: item.class_id, student_no: item.student_no, name: item.name, gender: item.gender || undefined });
          setCreateOpen(true);
        }}>编辑</Button>
        {(!item.account_status || item.account_status === "not_provisioned") ? <Button type="link" size="small" disabled={busy || item.status !== "active"} title={item.status !== "active" ? "请先恢复学生，再开通账号" : undefined} onClick={() => { accountForm.resetFields(); setAccountStudent(item); }}>开通账号</Button> : null}
        <Button type="link" size="small" disabled={busy} onClick={() => { setTransferringStudent(item); setTransferClassId(item.class_id); }}>调整班级</Button>
        <Button type="link" size="small" disabled={busy} onClick={() => confirmStatusChange(item)}>{item.status === "active" ? "停用" : "恢复"}</Button>
        {item.status === "inactive" ? <Button type="link" size="small" danger disabled={busy} onClick={() => confirmDelete(item)}>彻底删除</Button> : null}
        </Space>
      ); }
    }
  ];

  function confirmStatusChange(item: Student) {
    modal.confirm({
      title: item.status === "active" ? "停用学生" : "恢复学生",
      content: item.status === "active" ? `停用后，${item.name} 不会进入新考试的在籍学生范围；已开通的登录账号也将停用，已登录设备需重新登录。` : `恢复 ${item.name} 的在籍状态？已开通的账号会同步恢复登录。`,
      okText: "确认",
      cancelText: "取消",
      onOk: async () => {
        setActioning(item.id);
        try {
          const result = await updateStudentStatus(item.id, item.status === "active" ? "inactive" : "active");
          setStudents((current) => current.map((student) => student.id === result.student.id ? result.student : student));
          message.success(item.status === "active" ? "学生已停用，可在“已停用”中恢复或删除" : "学生已恢复，可在“在籍”中查看");
        } catch (error_) {
          message.error(errorText(error_));
          throw error_;
        } finally {
          setActioning("");
        }
      }
    });
  }

  function confirmDelete(student: Student) {
    modal.confirm({
      title: `彻底删除学生“${student.name}”？`,
      content: <><p>学号：{student.student_no}</p><p>删除后无法恢复。系统只会删除没有考试、答卷、成绩等业务记录的停用学生；未使用的学生账号会一并关闭。历史账号或兼有其他职责的账号需要先核对。</p></>,
      okText: "彻底删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      maskClosable: false,
      onOk: async () => {
        setActioning(student.id);
        try {
          await runWithStepUp({
            reason: `彻底删除学生“${student.name}”`,
            description: "此操作永久删除学生名册和学籍，并关闭可安全处理的学生账号。已有业务记录或历史账号冲突时会保留学生。",
            action: () => deleteStudent(student.id)
          });
          // 仅在远端确认删除后移除本地记录，失败时继续保留恢复入口。
          setStudents((current) => current.filter((item) => item.id !== student.id));
          message.success("学生已彻底删除");
        } catch (reason) {
          if (!isStepUpCancelledError(reason)) message.error(errorText(reason));
          throw reason;
        } finally { setActioning(""); }
      }
    });
  }

  const submitStudent = async (values: StudentFormValues) => {
    if (studentMutation.current) return;
    const schoolClass = classById.get(values.class_id);
    if (!editingStudent && !schoolClass) {
      message.error("所选班级已不可用，请刷新后重新选择");
      return;
    }
    studentMutation.current = true;
    setSaving(true);
    try {
      // 基本资料编辑不直接覆盖班级；转班单独走学籍接口，保留原班记录。
      const result = editingStudent
        ? await updateStudent(editingStudent.id, { student_no: values.student_no.trim(), name: values.name.trim(), gender: values.gender ?? "" })
        : await createStudent({ class_id: values.class_id, gender: values.gender, student_no: values.student_no.trim(), name: values.name.trim(), school_id: schoolClass!.school_id, password: values.password ?? "", password_confirm: values.password_confirm ?? "" });
      // 只用服务端已提交的学生及账号投影更新名册，避免成功后一次刷新失败让界面仍保留旧学号或账号状态。
      setStudents(current => editingStudent ? current.map(student => student.id === result.student.id ? result.student : student) : [...current.filter(student => student.id !== result.student.id), result.student]);
      if (!editingStudent) { setKeyword(""); setCohortKey("all"); setClassId("all"); setStatus("all"); }
      message.success(editingStudent ? "学生信息已修改" : "学生和账号已创建，首次登录需修改密码");
      setCreateOpen(false);
      setEditingStudent(null);
      form.resetFields();
    } catch (error_) {
      message.error(errorText(error_));
    } finally {
      studentMutation.current = false;
      setSaving(false);
    }
  };

  const submitAccount = async (values: StudentPasswordInput) => {
    if (!accountStudent || studentMutation.current) return;
    studentMutation.current = true;
    setSaving(true);
    try {
      const result = await openStudentAccount(accountStudent.id, values);
      setStudents(current => current.map(student => student.id === result.student.id ? result.student : student));
      message.success("学生账号已开通，首次登录需修改密码");
      setAccountStudent(null);
      accountForm.resetFields();
    } catch (reason) {
      message.error(errorText(reason));
    } finally { studentMutation.current = false; setSaving(false); }
  };

  const submitTransfer = async () => {
    if (!transferringStudent || !transferClassId || transferClassId === transferringStudent.class_id) return;
    const targetClass = classById.get(transferClassId);
    if (!targetClass || targetClass.school_id !== transferringStudent.school_id || targetClass.status !== "active") {
      message.error("目标班级已不可用，请重新选择本校班级");
      return;
    }
    setSaving(true);
    try {
      await transferStudent(transferringStudent.id, transferClassId);
      message.success("学生班级已调整");
      setTransferringStudent(null);
      await load();
    } catch (reason) {
      message.error(errorText(reason));
    } finally { setSaving(false); }
  };

  const submitImport = async () => {
    if (!csv.trim() || importReading || saving || importMutation.current) return;
    const prepared = importPreview ?? prepareStudentImport(csv, classes, importMapping, importClassID);
    // 整批导入不能先剔除坏行再提交其余行；本地发现问题时，不向服务端写入任何学生。
    if (prepared.errors.length || !prepared.csv) {
      const errors = prepared.errors.length ? prepared.errors : [{ row: 1, message: "名单没有学生记录，请检查文件内容" }];
      setImportResult({ created: 0, skipped: 0, committed: false, errors });
      message.warning("名单存在问题，本次整批未导入，请修正后重试。");
      return;
    }
    const generation = importGeneration.current;
    // 在React更新按钮前同步上锁；导入回包只属于本次已核对的文件和工作表。
    importMutation.current = true;
    setSaving(true);
    try {
      // 网络结果不明时重试同一份标准化名单，沿用原请求键；确定结果或修改内容后再建立新操作。
      if (importAttempt.current?.csv !== prepared.csv) importAttempt.current = { csv: prepared.csv, commandId: crypto.randomUUID() };
      const result = await importStudentsCSV(prepared.csv, importAttempt.current.commandId);
      if (generation !== importGeneration.current) return;
      const errors = result.result.errors.map((item) => remapStudentImportError(item, prepared.sourceRows)).sort((left, right) => left.row - right.row);
      const committed = result.result.committed ?? errors.length === 0;
      const skipped = result.result.skipped ?? 0;
      setImportResult({ created: result.result.created, skipped, committed, errors });
      importAttempt.current = null;
      if (!committed || errors.length) {
        message.warning("名单存在冲突，本次整批未导入，请修正后重试。");
      } else {
        message.success(`本次新增 ${result.result.created} 名学生，跳过 ${skipped} 条相同记录`);
      }
      if (committed && result.result.created) await load();
    } catch (error_) {
      if (generation === importGeneration.current) message.error(errorText(error_));
    } finally {
      importMutation.current = false;
      if (generation === importGeneration.current) setSaving(false);
    }
  };

  if (loading && !students.length) return <LoadingState label="正在读取学生名册" />;
  if (error && !students.length) return <ErrorState message={error} onRetry={() => void load()} />;

  return (
    <div className="member-management-page student-management-page">
      <section className="member-management-heading">
        <div><h1>学生管理</h1><p>维护学生名册与登录账号；学生使用学校代码、学号和密码登录。</p></div>
        <div>
          <Button icon={<RefreshCw size={16} />} loading={loading} disabled={saving || Boolean(actioning)} onClick={() => void load()}>刷新</Button>
          <Button icon={<UploadIcon size={16} />} disabled={saving || Boolean(actioning)} onClick={() => setImportOpen(true)}>导入学生</Button>
          <Button type="primary" icon={<Plus size={16} />} disabled={saving || Boolean(actioning)} onClick={() => { setEditingStudent(null); form.resetFields(); setCreateOpen(true); }}>新增学生</Button>
        </div>
      </section>

      <section className="student-management-filters" aria-label="学生筛选">
        <label className="student-filter-field student-filter-search">
          <span>姓名或学号</span>
          <Input allowClear prefix={<Search size={15} />} value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder="搜索学生" />
        </label>
        <label className="student-filter-field student-filter-cohort">
          <span>届别年级</span>
          <Select aria-label="届别年级" value={cohortKey} options={[{ value: "all", label: "全部届别年级" }, ...cohortGrades.map((group) => ({ value: group.key, label: gradeCohortLabel(group.grade) }))]} onChange={(value) => {
            setCohortKey(value);
            const selectedClass = classById.get(classId);
            const selectedGrade = gradeById.get(selectedClass?.grade_id ?? "");
            if (value !== "all" && (!selectedGrade || gradeCohortKey(selectedGrade) !== value)) setClassId("all");
          }} />
        </label>
        <label className="student-filter-field student-filter-class">
          <span>班级</span>
          <Select aria-label="班级筛选" value={classId} options={[{ value: "all", label: "全部班级" }, ...classOptions]} onChange={setClassId} />
        </label>
        <Segmented className="student-filter-status" aria-label="学生状态" value={status} onChange={setStatus} options={[{ value: "all", label: "全部" }, { value: "active", label: "在籍" }, { value: "inactive", label: "已停用" }]} />
      </section>

      <section className="student-management-table">
        <div className="student-roster-toolbar"><div className="section-head"><div><h2>学生名册</h2><p>{filtered.length} 名学生</p></div></div></div>
        <ResponsiveTable className="dense-data-table" rowKey="id" size="small" columns={columns} dataSource={filtered} scroll={{ x: 1120 }} pagination={{ size: "small", pageSize: 20, showSizeChanger: true }} locale={{ emptyText: <EmptyState title={status === "inactive" ? "暂无已停用学生" : "暂无学生"} description={status === "inactive" ? "停用学生会显示在这里，可恢复或彻底删除。" : "可新增学生，或下载模板后批量导入。"} /> }} />
      </section>

      <Modal title={editingStudent ? "编辑学生" : "新增学生"} open={createOpen} okText="保存" cancelText="取消" confirmLoading={saving} maskClosable={!saving} closable={!saving} cancelButtonProps={{ disabled: saving }} onOk={() => form.submit()} onCancel={() => { if (!saving) { setCreateOpen(false); setEditingStudent(null); form.resetFields(); } }}>
        <Form name="student-details" form={form} layout="vertical" requiredMark={false} disabled={saving} onFinish={(values) => void submitStudent(values)}>
          <Form.Item name="class_id" label="班级" extra={editingStudent ? "如需转班，请使用列表中的“调整班级”。" : undefined} rules={editingStudent ? [] : [{ required: true, message: "请选择班级" }]}><Select aria-label="学生班级" disabled={Boolean(editingStudent)} showSearch optionFilterProp="label" options={classes.map((item) => ({ value: item.id, label: `${gradeCohortLabel(currentGradeForClass(item))} · ${classCohortLabel(item, currentGradeForClass(item))}` }))} /></Form.Item>
          <Form.Item name="student_no" label="学号" extra={editingStudent?.account_status && editingStudent.account_status !== "not_provisioned" ? "学号也是登录账号。修改后旧学号失效，学生需用新学号重新登录。" : "学号作为学生登录账号，同一学校内不能重复。"} rules={[{ required: true, whitespace: true, message: "请输入学号" }]}><Input maxLength={64} autoComplete="off" /></Form.Item>
          <Form.Item name="name" label="姓名" rules={[{ required: true, whitespace: true, message: "请输入姓名" }]}><Input maxLength={120} /></Form.Item>
          <Form.Item name="gender" label="性别（可选）"><Select allowClear options={[{ value: "male", label: "男" }, { value: "female", label: "女" }, { value: "unknown", label: "未填写" }]} /></Form.Item>
          {!editingStudent ? <section className="student-form-account" aria-label="学生登录账号"><h3>登录账号</h3><p>保存时同步开通账号，登录学校代码以列表显示为准。</p><StudentPasswordFields /></section> : null}
        </Form>
      </Modal>

      <Modal title={accountStudent ? `开通账号 · ${accountStudent.name}` : "开通学生账号"} open={Boolean(accountStudent)} okText="开通账号" cancelText="取消" confirmLoading={saving} maskClosable={!saving} closable={!saving} cancelButtonProps={{ disabled: saving }} onOk={() => accountForm.submit()} onCancel={() => { if (!saving) { setAccountStudent(null); accountForm.resetFields(); } }}>
        <p className="student-account-intro">登录账号：{accountStudent?.student_no}。开通后，学生使用学校代码、学号和密码登录。</p>
        <Form name="student-account" form={accountForm} layout="vertical" requiredMark={false} disabled={saving} onFinish={values => void submitAccount(values)}><StudentPasswordFields /></Form>
      </Modal>

      <Modal title={transferringStudent ? `调整班级 · ${transferringStudent.name}` : "调整班级"} open={Boolean(transferringStudent)} okText="保存" cancelText="取消" confirmLoading={saving} okButtonProps={{ disabled: !transferClassId || transferClassId === transferringStudent?.class_id }} cancelButtonProps={{ disabled: saving }} maskClosable={!saving} closable={!saving} onOk={() => void submitTransfer()} onCancel={() => { if (!saving) setTransferringStudent(null); }}>
        <p>当前班级：{(() => { const schoolClass = classById.get(transferringStudent?.class_id ?? ""); return schoolClass ? `${gradeCohortLabel(currentGradeForClass(schoolClass))} · ${classCohortLabel(schoolClass, currentGradeForClass(schoolClass))}` : "未分班"; })()}。调整后保留原班学籍记录。</p>
        <label>目标班级<Select aria-label="目标班级" style={{ width: "100%", marginTop: 8 }} disabled={saving} value={transferClassId} showSearch optionFilterProp="label" onChange={setTransferClassId} options={classes.filter((item) => item.school_id === transferringStudent?.school_id && item.status === "active").map((item) => ({ value: item.id, label: `${gradeCohortLabel(currentGradeForClass(item))} · ${classCohortLabel(item, currentGradeForClass(item))}` }))} /></label>
      </Modal>

      <Modal title="批量导入学生" open={importOpen} okText={importResult ? (importResult.committed ? "完成" : "返回调整") : "开始导入"} cancelText="取消" okButtonProps={{ disabled: importReading || !csv.trim() && !importResult }} maskClosable={!saving} closable={!saving} cancelButtonProps={{ disabled: saving }} keyboard={!saving} confirmLoading={saving} onOk={() => { if (importResult) { if (!importResult.committed) { setImportResult(null); } else { setImportOpen(false); resetImportFile(); } } else void submitImport(); }} onCancel={() => { if (!saving && !importMutation.current) { setImportOpen(false); resetImportFile(); } }}>
        <div className="student-import-copy"><p>支持 Excel（.xlsx）和 CSV，可核对列映射或指定整班。学号列请设为文本以保留前导0；每次最多10000名学生。</p><Button size="small" icon={<Download size={15} />} onClick={downloadTemplate}>下载模板</Button></div>
        <Alert type="info" showIcon message="导入只新增学生名册，不创建登录账号。导入后请在列表中开通账号并设置初始密码。" description="整批校验通过后才写入；任一行有问题，本次全部不导入。相同记录自动跳过，不修改已有学生或考试名册。" />
        <Upload.Dragger accept=".csv,.xlsx,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" disabled={saving} maxCount={1} beforeUpload={readImportFile} onRemove={() => resetImportFile()}><UploadIcon size={22} /><p>{importReading ? "正在读取名单…" : "选择或拖入 Excel、CSV 名单"}</p></Upload.Dragger>
        {importSheets.length > 1 ? <label>选择工作表<Select aria-label="导入工作表" disabled={saving || importReading} value={importSheetIndex} options={importSheets.map((sheet, index) => ({ value: index, label: sheet.name }))} onChange={index => applyImportSheet(importSheets[index], index)} /></label> : null}
        {csv && !importResult ? <section className="student-import-mapping" aria-label="导入列映射">
          {([['studentNo', '学号列'], ['name', '姓名列'], ['classCode', '班级代码列']] as const).map(([key, label]) => <label key={key}>{label}<Select aria-label={label} disabled={saving} allowClear value={importMapping[key] || undefined} options={importHeaders.map(value => ({ value, label: value }))} onChange={value => setImportMapping(current => ({ ...current, [key]: value ?? "" }))} /></label>)}
          <label>目标班级（可选）<Select aria-label="导入目标班级" disabled={saving} allowClear showSearch optionFilterProp="label" value={importClassID || undefined} onChange={value => setImportClassID(value ?? "")} options={classes.filter(item => item.status === "active").map(item => ({ value: item.id, label: `${gradeCohortLabel(currentGradeForClass(item))} · ${classCohortLabel(item, currentGradeForClass(item))} · ${item.code}` }))} /></label>
          <p>指定目标班级后，每行必须属于该班；未指定时，班级代码须在可访问范围内唯一。</p>
        </section> : null}
        {importPreview && !importResult ? <div className="student-import-preview" aria-live="polite">本地预检：{importPreview.sourceRows.length} 行格式通过，{importPreview.errors.length} 处问题。{importPreview.errors.length ? "请先修正全部问题，当前名单不会部分导入。" : "提交后，服务端会再核对已有学生和班级权限。"}{importPreview.errors.length ? <ul>{importPreview.errors.slice(0, 5).map((entry, index) => <li key={`${entry.row}:${index}`}>第 {entry.row} 行：{entry.message}</li>)}</ul> : null}</div> : null}
        {importResult ? <section className="student-import-result" aria-live="polite">
          <Alert showIcon type={importResult.committed ? "success" : "error"} message={importResult.committed ? `本次新增 ${importResult.created} 名学生，跳过 ${importResult.skipped} 条相同记录` : "本次整批未导入，新增 0 名学生"} description={importResult.committed ? "已有学生的姓名、班级和状态均未修改。新增学生的账号尚未开通，请到列表中设置。" : "请修正下列问题后重新导入整份名单，格式正确的其他行也尚未新增。"} />
          {importResult.errors.length ? <><Button icon={<Download size={15} />} onClick={() => downloadImportErrors(importResult.errors)}>下载完整错误表</Button><ol>{importResult.errors.map((entry, index) => <li key={`${entry.row}-${index}`}>第 {entry.row} 行：{entry.message}</li>)}</ol></> : null}
        </section> : null}
      </Modal>
    </div>
  );
}
