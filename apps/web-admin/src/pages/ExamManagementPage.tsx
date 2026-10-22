import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App, Button, Drawer, Dropdown, Input, Select, Space, Table, Tooltip, type MenuProps, type TableColumnsType } from "antd";
import { Archive, Eye, MoreHorizontal, Pencil, RefreshCw, RotateCcw, Search } from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import { getManagementExam, listAllManagementExams, type ManagementExam } from "../api/exams";
import { listClasses, listGrades, listSchools, type Grade, type School, type SchoolClass } from "../api/org";
import type { SessionUser } from "../auth/session";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { examStatusTone, examSubjectLabel } from "../constants/examStatus";
import { examTypeOptions } from "../constants/examCatalog";
import { ManagementExamArchiveDialog } from "../features/exams/management/ManagementExamArchiveDialog";
import { ManagementExamDetails } from "../features/exams/management/ManagementExamDetails";
import { ManagementExamEditDrawer } from "../features/exams/management/ManagementExamEditDrawer";
import { filterManagementExams, isManagementExamArchived, managementExamKey, managementStatusOptions, type ManagementFilters } from "../features/exams/management/managementPresentation";
import type { ProductExperience } from "../router/experience";
import { hashQueryParam } from "../router/query";

export function ExamManagementPage({ mode, canManage, currentUser, onOpenWorkspace, onCreateExam }: Readonly<{
  mode: ProductExperience;
  canManage: boolean;
  currentUser: SessionUser;
  onOpenWorkspace: (exam: ManagementExam) => void;
  onCreateExam: () => void;
}>) {
  const { message } = App.useApp();
  const [filters, setFilters] = useState<ManagementFilters>(() => {
    const status = hashQueryParam("status");
    return { search: "", schoolId: "", gradeId: "", status: status === "archived" ? "" : status, examType: "", archive: status === "archived" ? "archived" : "active" };
  });
  const [exams, setExams] = useState<ManagementExam[]>([]);
  const [schools, setSchools] = useState<School[]>([]);
  const [grades, setGrades] = useState<Grade[]>([]);
  const [classes, setClasses] = useState<SchoolClass[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [detail, setDetail] = useState<{ exam: ManagementExam | null; loading: boolean; error: string | null } | null>(null);
  const [editExam, setEditExam] = useState<ManagementExam | null>(null);
  const [actioningId, setActioningId] = useState<string | null>(null);
  const [archiveSelection, setArchiveSelection] = useState<{ exam: ManagementExam; action: "archive" | "restore" } | null>(null);
  const loadSequence = useRef(0);
  const detailSequence = useRef(0);
  const editSequence = useRef(0);
  const teacherMode = mode === "teacher";

  const loadData = useCallback(async () => {
    const request = ++loadSequence.current;
    setLoading(true);
    setError(null);
    try {
      const results = await Promise.all([
        listAllManagementExams({ school_id: teacherMode ? undefined : filters.schoolId || undefined, archive: "all" }),
        teacherMode ? Promise.resolve({ schools: [] as School[] }) : listSchools(),
        teacherMode ? Promise.resolve({ grades: [] as Grade[] }) : listGrades(filters.schoolId || undefined),
        teacherMode ? Promise.resolve({ classes: [] as SchoolClass[] }) : listClasses()
      ]);
      // 学校或账户范围切换后，较慢的旧列表不能覆盖新范围的整场考试。
      if (request !== loadSequence.current) return;
      setExams(results[0].exams);
      setSchools(results[1].schools);
      setGrades(results[2].grades);
      setClasses(results[3].classes);
    } catch (currentError) {
      if (request === loadSequence.current) setError(getUserErrorMessage(currentError, "考试列表加载失败"));
    } finally {
      if (request === loadSequence.current) setLoading(false);
    }
  }, [filters.schoolId, teacherMode]);

  useEffect(() => {
    void loadData();
    return () => { loadSequence.current += 1; detailSequence.current += 1; editSequence.current += 1; };
  }, [loadData]);

  const schoolById = useMemo(() => new Map(schools.map((school) => [school.id, school])), [schools]);
  const gradeById = useMemo(() => new Map(grades.map((grade) => [grade.id, grade])), [grades]);
  const classNames = useMemo(() => new Map(classes.map((schoolClass) => [schoolClass.id, schoolClass.name])), [classes]);
  const classGrade = useMemo(() => new Map(classes.map((schoolClass) => [schoolClass.id, schoolClass.grade_id])), [classes]);
  const filteredExams = useMemo(() => filterManagementExams(exams, filters, classGrade), [exams, filters, classGrade]);
  const gradeName = (exam: ManagementExam) => gradeById.get(exam.grade_id)?.name ?? ([...new Set(exam.class_ids.map((id) => gradeById.get(classGrade.get(id) ?? "")?.name).filter(Boolean))].join("、") || "未关联年级");

  const openDetail = async (exam: ManagementExam) => {
    const request = ++detailSequence.current;
    setDetail({ exam: null, loading: true, error: null });
    try {
      const result = await getManagementExam(exam.kind, exam.id);
      if (request === detailSequence.current) setDetail({ exam: result.exam, loading: false, error: null });
    } catch (currentError) {
      if (request === detailSequence.current) setDetail({ exam: null, loading: false, error: getUserErrorMessage(currentError, "考试详情加载失败") });
    }
  };

  const openEdit = async (exam: ManagementExam) => {
    const request = ++editSequence.current;
    setActioningId(managementExamKey(exam));
    try {
      const result = await getManagementExam(exam.kind, exam.id);
      if (request !== editSequence.current) return;
      if (!result.exam.can_edit || !result.exam.can_manage || !result.exam.scope_complete) message.warning("当前考试或权限不允许编辑整场设置，请刷新后检查。");
      else setEditExam(result.exam);
    } catch (currentError) {
      if (request === editSequence.current) message.error(getUserErrorMessage(currentError, "无法读取考试设置"));
    } finally {
      if (request === editSequence.current) setActioningId(null);
    }
  };

  const columns: TableColumnsType<ManagementExam> = [
    { title: "考试名称", dataIndex: "name", width: 220, ellipsis: true, render: (value: string, exam) => <Tooltip title={value}><button className="exam-name-link" type="button" onClick={(event) => { event.stopPropagation(); onOpenWorkspace(exam); }}>{value}</button></Tooltip> },
    { title: "考试类型", dataIndex: "exam_type", width: 105, render: (value: string) => examTypeOptions.find((item) => item.value === value)?.label ?? "其他类型" },
    { title: "年级", width: 100, ellipsis: true, render: (_, exam) => gradeName(exam) },
    { title: "参考班级", width: 100, render: (_, exam) => <Tooltip title={exam.class_ids.map((id) => classNames.get(id) ?? id).join("、") || "暂无班级信息"}>{exam.class_ids.length} 个班级</Tooltip> },
    { title: "学科", width: 170, ellipsis: true, render: (_, exam) => {
      const names = exam.exams.map((subject) => examSubjectLabel(subject.subject));
      return <Tooltip title={names.join("、")}>{names.slice(0, 3).join("、")} · 共 {exam.exams.length} 科{!exam.scope_complete ? "（已授权）" : ""}</Tooltip>;
    } },
    { title: "考试进度", width: 220, render: (_, exam) => <StatusTag tone={isManagementExamArchived(exam) ? "neutral" : examStatusTone(exam.progress.status)}>{isManagementExamArchived(exam) ? "已归档" : exam.progress.label}</StatusTag> },
    // 菜单及其弹层的点击不能冒泡到整行，否则查看详情、编辑和归档会意外跳转。
    { title: "操作", width: 64, onCell: () => ({ onClick: (event) => event.stopPropagation() }), render: (_, exam) => {
      const items: MenuProps["items"] = [
        { key: "detail", label: "查看考试详情", icon: <Eye size={14} /> },
        ...(canManage ? [{ key: "edit", label: "编辑考试", icon: <Pencil size={14} />, disabled: !exam.can_edit || !exam.can_manage || !exam.scope_complete }] : []),
        ...(canManage && !isManagementExamArchived(exam) ? [{ key: "archive", label: "归档考试", icon: <Archive size={14} />, danger: true, disabled: !exam.can_manage || !exam.scope_complete }] : []),
        ...(canManage && isManagementExamArchived(exam) ? [{ key: "restore", label: "恢复考试", icon: <RotateCcw size={14} />, disabled: !exam.can_restore || !exam.can_manage || !exam.scope_complete }] : [])
      ];
      return <Dropdown trigger={["click"]} menu={{ items, onClick: ({ key, domEvent }) => {
          domEvent.stopPropagation();
          if (key === "detail") void openDetail(exam);
          if (key === "edit") void openEdit(exam);
          if (key === "archive" || key === "restore") setArchiveSelection({ exam, action: key });
        } }}><Button size="small" loading={actioningId === managementExamKey(exam)} aria-label={`${exam.name}更多操作`} icon={<MoreHorizontal size={15} />} /></Dropdown>;
    } }
  ];

  // 列表与详情只拆展示分支，请求代次和整场操作仍由页面持有。
  const renderExamList = () => {
    if (loading) return <section className="workspace-section"><LoadingState label="正在加载考试列表" /></section>;
    if (error) return <ErrorState message={error} onRetry={() => void loadData()} />;
    return <section className="member-table-section"><div className="section-head"><h2>{teacherMode ? "已授权考试" : "考试列表"}</h2></div><section aria-label="考试列表表格">
      <Table<ManagementExam> rowKey={managementExamKey} dataSource={filteredExams} columns={columns} pagination={{ pageSize: 10, showSizeChanger: false, showTotal: (total) => `共 ${total} 场考试` }} locale={{ emptyText: <EmptyState title="暂无考试" description={canManage ? "没有符合条件的考试。试试调整筛选条件，或新建考试。" : "没有符合条件的考试，请联系管理员为你授权。"} /> }} size="small" className="dense-data-table" tableLayout="fixed" scroll={{ x: 980 }} rowClassName="exam-navigable-row" onRow={(exam) => ({ onClick: () => onOpenWorkspace(exam) })} />
    </section></section>;
  };

  const renderDetail = () => {
    if (detail?.loading) return <LoadingState label="正在读取整场考试详情" />;
    if (detail?.error) return <ErrorState message={detail.error} />;
    if (!detail?.exam) return null;
    return <ManagementExamDetails exam={detail.exam} gradeName={gradeName(detail.exam)} classNames={classNames} creatorName={detail.exam.created_by === currentUser.id ? currentUser.name : undefined} />;
  };

  return <div className="page-stack exam-management-page">
    <section className="page-heading"><div><h1>{teacherMode ? "我的考试" : "考试管理"}</h1><p>{teacherMode ? "查看已授权考试及学科，进入对应工作区。" : "管理整场考试，进入考试工作区后选择学科配置。"}</p></div><Space wrap><Button icon={<RefreshCw size={16} />} onClick={() => void loadData()} loading={loading}>刷新</Button>{canManage ? <Button type="primary" onClick={onCreateExam}>新建考试</Button> : null}</Space></section>
    <section className="workspace-section filter-panel"><div className="filter-grid exam-filter-primary">
      <Input prefix={<Search size={16} />} placeholder="搜索考试名称" value={filters.search} onChange={(event) => setFilters((current) => ({ ...current, search: event.target.value }))} />
      <Select aria-label="考试进度筛选" placeholder="考试进度" allowClear value={filters.status || undefined} options={[{ label: "进行中", value: "active" }, ...managementStatusOptions]} onChange={(value) => setFilters((current) => ({ ...current, status: value ?? "" }))} />
      <Select aria-label="归档范围筛选" value={filters.archive} options={[{ label: "未归档", value: "active" }, { label: "已归档", value: "archived" }, { label: "全部考试", value: "all" }]} onChange={(value: ManagementFilters["archive"]) => setFilters((current) => ({ ...current, archive: value }))} />
    </div>{!teacherMode ? <details className="advanced-filter-disclosure"><summary>更多筛选</summary><div className="filter-grid">
      <Select placeholder="学校" allowClear value={filters.schoolId || undefined} options={schools.map((school) => ({ label: school.name, value: school.id }))} onChange={(value) => setFilters((current) => ({ ...current, schoolId: value ?? "", gradeId: "" }))} />
      <Select placeholder="年级" allowClear value={filters.gradeId || undefined} options={grades.map((grade) => ({ label: grade.name, value: grade.id }))} onChange={(value) => setFilters((current) => ({ ...current, gradeId: value ?? "" }))} />
      <Select placeholder="考试类型" allowClear value={filters.examType || undefined} options={examTypeOptions} onChange={(value) => setFilters((current) => ({ ...current, examType: value ?? "" }))} />
    </div></details> : null}</section>
    {renderExamList()}
    <Drawer title="考试详情" open={Boolean(detail)} onClose={() => { detailSequence.current += 1; setDetail(null); }} width={820}>
      {renderDetail()}
    </Drawer>
    <ManagementExamEditDrawer exam={editExam} classes={classes} schoolName={editExam ? schoolById.get(editExam.school_id)?.name : undefined} gradeName={editExam ? gradeName(editExam) : undefined} onClose={() => setEditExam(null)} onSaved={loadData} />
    <ManagementExamArchiveDialog selection={archiveSelection} actorScope={`${currentUser.tenant}:${currentUser.id}`} onClose={() => setArchiveSelection(null)} onSaved={loadData} />
  </div>;
}
