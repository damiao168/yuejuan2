import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Drawer, Input, Select, Space, Table, Tooltip, type TableColumnsType } from "antd";
import { RefreshCw, Search } from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import { getManagementExam, listAllManagementExams, type ManagementExam } from "../api/exams";
import { listClasses, listGrades, listSchools, type Grade, type School, type SchoolClass } from "../api/org";
import type { SessionUser } from "../auth/session";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { examSubjectLabel } from "../constants/examStatus";
import { ManagementExamDetails } from "../features/exams/management/ManagementExamDetails";
import { ManagementExamEditDrawer } from "../features/exams/management/ManagementExamEditDrawer";
import { filterManagementExams, managementExamKey, managementGradingComplete, managementProgressFilter, managementProgressLabel, managementStatusOptions, type ManagementFilters } from "../features/exams/management/managementPresentation";
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
    return { search: "", schoolId: "", gradeId: "", status: managementProgressFilter(hashQueryParam("status")) };
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
  const loadSequence = useRef(0);
  const detailSequence = useRef(0);
  const editSequence = useRef(0);
  const teacherMode = mode === "teacher";
  const editSubjectId = hashQueryParam("edit_exam");
  const editTarget = hashQueryParam("target") === "scope" ? "scope" : undefined;
  const [focusedEditNotice, setFocusedEditNotice] = useState("");
  const focusedEditAttempt = useRef("");
  const focusedEditKey = `${currentUser.tenant}:${currentUser.id}:${canManage}:${editSubjectId}`;

  const loadData = useCallback(async () => {
    const request = ++loadSequence.current;
    setLoading(true);
    setError(null);
    try {
      const results = await Promise.all([
        listAllManagementExams({ school_id: teacherMode ? undefined : filters.schoolId || undefined, status: filters.status || undefined, archive: "active" }),
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
  }, [filters.schoolId, filters.status, teacherMode]);

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

  useEffect(() => {
    if (!editSubjectId) { focusedEditAttempt.current = ""; setFocusedEditNotice(""); return; }
    if (loading || error || focusedEditAttempt.current === focusedEditKey) return;
    // 保存后的列表刷新不应再次弹出刚关闭的编辑器；只有新入口或明确刷新才重试定位。
    focusedEditAttempt.current = focusedEditKey;
    let active = true;
    // 深链只在本次已授权列表中解析学科归属；不能把任意 URL 标识当作整场管理权限。
    const target = exams.find(exam => exam.exams.some(subject => subject.id === editSubjectId));
    setFocusedEditNotice("");
    if (!target) { setFocusedEditNotice("当前授权范围内未找到该考试，请联系学校考试管理员核对入口与授权。"); return; }
    const request = ++editSequence.current;
    setActioningId(managementExamKey(target));
    void getManagementExam(target.kind, target.id).then(({ exam }) => {
      if (!active || request !== editSequence.current) return;
      if (!exam.exams.some(subject => subject.id === editSubjectId)) throw new Error("考试归属已变化，请重新检查准备状态。");
      if (!canManage || !exam.can_edit || !exam.can_manage || !exam.scope_complete) {
        setEditExam(null);
        setDetail({ exam, loading: false, error: null });
        setFocusedEditNotice(`已定位“${exam.name}”。当前状态或授权不允许修改，请联系学校考试管理员核对年级与参考班级。`);
      } else setEditExam(exam);
    }).catch(cause => { if (active && request === editSequence.current) setFocusedEditNotice(getUserErrorMessage(cause, "指定考试设置读取失败，请刷新重试。")); })
      .finally(() => { if (active && request === editSequence.current) setActioningId(null); });
    return () => { active = false; };
  }, [editSubjectId, focusedEditKey, exams, loading, error, canManage]);

  const columns: TableColumnsType<ManagementExam> = [
    { title: "考试名称", dataIndex: "name", width: 220, ellipsis: true, render: (value: string, exam) => <Tooltip title={value}><button className="exam-name-link" type="button" onClick={(event) => { event.stopPropagation(); onOpenWorkspace(exam); }}>{value}</button></Tooltip> },
    { title: "年级", width: 100, ellipsis: true, render: (_, exam) => gradeName(exam) },
    { title: "参考班级", width: 100, render: (_, exam) => <Tooltip title={exam.class_ids.map((id) => classNames.get(id) ?? id).join("、") || "暂无班级信息"}>{exam.class_ids.length} 个班级</Tooltip> },
    { title: "学科", width: 170, ellipsis: true, render: (_, exam) => {
      const names = exam.exams.map((subject) => examSubjectLabel(subject.subject));
      return <Tooltip title={names.join("、")}>{names.slice(0, 3).join("、")} · 共 {exam.exams.length} 科{!exam.scope_complete ? "（已授权）" : ""}</Tooltip>;
    } },
    { title: "考试进度", width: 140, render: (_, exam) => <Tooltip title={exam.progress.label}><StatusTag tone={managementGradingComplete(exam) ? "success" : exam.progress.status === "unavailable" ? "warning" : "processing"}>{managementProgressLabel(exam)}</StatusTag></Tooltip> },
    // 编辑按钮独立操作，不能冒泡触发整行跳转；无整场管理入口的用户不显示此列。
    ...(canManage ? [{ title: "编辑", width: 80, onCell: () => ({ onClick: (event) => event.stopPropagation() }), render: (_, exam) => (
      <Button type="link" size="small" loading={actioningId === managementExamKey(exam)}
        disabled={!exam.can_edit || !exam.can_manage || !exam.scope_complete} aria-label={`编辑${exam.name}`}
        onClick={(event) => { event.stopPropagation(); void openEdit(exam); }}>编辑</Button>
    ) } satisfies TableColumnsType<ManagementExam>[number]] : [])
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
    <section className="page-heading"><div><h1>{teacherMode ? "我的考试" : "考试管理"}</h1><p>{teacherMode ? "查看已授权考试及学科，进入对应工作区。" : "管理整场考试，进入考试工作区后选择学科配置。"}</p></div><Space wrap><Button icon={<RefreshCw size={16} />} onClick={() => { focusedEditAttempt.current = ""; void loadData(); }} loading={loading}>刷新</Button>{canManage ? <Button type="primary" onClick={onCreateExam}>新建考试</Button> : null}</Space></section>
    <section className="workspace-section filter-panel"><div className="filter-grid exam-filter-primary">
      <Input prefix={<Search size={16} />} placeholder="搜索考试名称" value={filters.search} onChange={(event) => setFilters((current) => ({ ...current, search: event.target.value }))} />
      <Select aria-label="考试进度筛选" value={filters.status} options={managementStatusOptions} onChange={(value) => setFilters((current) => ({ ...current, status: value ?? "" }))} />
    </div>{!teacherMode ? <details className="advanced-filter-disclosure"><summary>更多筛选</summary><div className="filter-grid">
      <Select placeholder="学校" allowClear value={filters.schoolId || undefined} options={schools.map((school) => ({ label: school.name, value: school.id }))} onChange={(value) => setFilters((current) => ({ ...current, schoolId: value ?? "", gradeId: "" }))} />
      <Select placeholder="年级" allowClear value={filters.gradeId || undefined} options={grades.map((grade) => ({ label: grade.name, value: grade.id }))} onChange={(value) => setFilters((current) => ({ ...current, gradeId: value ?? "" }))} />
    </div></details> : null}</section>
    {focusedEditNotice ? <Alert type="warning" showIcon message={focusedEditNotice} /> : null}
    {renderExamList()}
    <Drawer title="考试详情" open={Boolean(detail)} onClose={() => { detailSequence.current += 1; setDetail(null); }} width={820}>
      {renderDetail()}
    </Drawer>
    <ManagementExamEditDrawer exam={editExam} classes={classes} focusTarget={editTarget} schoolName={editExam ? schoolById.get(editExam.school_id)?.name : undefined} gradeName={editExam ? gradeName(editExam) : undefined} onClose={() => setEditExam(null)} onSaved={loadData} />
  </div>;
}
