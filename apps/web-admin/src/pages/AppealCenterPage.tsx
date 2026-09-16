import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Input, InputNumber, Select, Space, Tag, type TableColumnsType } from "antd";
import { CheckCircle2, RefreshCw, Search, Send, UserRoundCheck } from "lucide-react";
import type { SessionUser } from "../auth/session";
import { canSubmitTeacherAppealRecommendation } from "../auth/capabilities";
import { getUserErrorMessage } from "../api/client";
import { listExams, type Exam } from "../api/exams";
import { listClasses, listStudents, type SchoolClass, type Student } from "../api/org";
import { assignAppeal, getAppeal, getAppealStatistics, listAppeals, reviewAppeal, submitAppealRecommendation, type Appeal, type AppealStatistics, type ReviewAppealPayload, type SubmitAppealRecommendationPayload } from "../api/appeals";
import { listManagedUsers, type ManagedUser } from "../api/users";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { examSubjectLabel } from "../constants/examStatus";
import type { ProductExperience } from "../router/experience";
import type { StatusTone } from "../types";

interface AppealCenterPageProps {
  mode: ProductExperience; canRead: boolean; canManage: boolean; canWork: boolean;
  canReadAudit: boolean; canReadIdentities: boolean; canReadExams: boolean;
  currentUser: SessionUser; initialExamId?: string;
}
interface IdentityMaps { students: Record<string, Student>; classes: Record<string, SchoolClass>; exams: Record<string, Exam> }

const statusLabels: Record<string, string> = { submitted: "待分派", under_review: "复核中", need_more_info: "继续复核", accepted: "申诉成立", rejected: "维持原分", score_adjusted: "已调整分数", closed: "已完成" };
const reasonLabels: Record<string, string> = { recognition_error: "答题内容识别不完整", missing_step_credit: "作答步骤疑似漏评", rubric_disagreement: "评分标准适用有异议", calculation_error: "得分记录或合计有误", annotation_issue: "批注与实际扣分不一致", other: "其他明确评分问题" };
const recommendationLabels: Record<string, string> = { accept: "申诉成立", reject: "维持原分", adjust_score: "建议改分", need_more_info: "需继续复核" };
const recommendationOptions = Object.entries(recommendationLabels).map(([value, label]) => ({ value, label }));

function formatError(error: unknown) { return getUserErrorMessage(error, "操作失败，请稍后重试"); }
function formatTime(value?: string) { if (!value) return "-"; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false }); }
function formatScore(value?: number | null) { return value === undefined || value === null || !Number.isFinite(value) ? "-" : Number(value.toFixed(2)).toString(); }
function statusTone(status: string): StatusTone { if (["accepted", "score_adjusted", "closed"].includes(status)) return "success"; if (status === "rejected") return "neutral"; if (["submitted", "need_more_info"].includes(status)) return "warning"; return "processing"; }
function readableReason(value: string) {
  const text = value.trim(); if (reasonLabels[text]) return reasonLabels[text]; const lower = text.toLowerCase();
  if (lower.includes("grading does not match") || lower.includes("published rubric")) return "评分标准适用有异议";
  if (lower.includes("ocr") || lower.includes("recognition")) return "答题内容识别不完整";
  if (lower.includes("missing") || lower.includes("step")) return "作答步骤疑似漏评";
  if (lower.includes("calculation") || lower.includes("total")) return "得分记录或合计有误";
  return text || "其他明确评分问题";
}
function readableReasonDetail(value: string) {
  if (value.trim().toLowerCase().includes("grading does not match the published rubric")) return "学生认为本题评分未按已发布的评分标准执行，请核对作答过程与各评分点。";
  return value.trim();
}
function evidenceNumber(appeal: Appeal | null, key: string) { const value = appeal?.evidence?.final_grade?.[key]; return typeof value === "number" && Number.isFinite(value) ? value : undefined; }
function rubricItems(appeal: Appeal | null) {
  const points = appeal?.evidence?.rubric?.points; if (!Array.isArray(points)) return [];
  return points.flatMap((point, index) => { if (!point || typeof point !== "object") return []; const item = point as Record<string, unknown>; const label = [item.description, item.name, item.code].find((value): value is string => typeof value === "string" && Boolean(value.trim())) ?? `评分点 ${index + 1}`; const score = typeof item.score === "number" ? item.score : typeof item.points === "number" ? item.points : undefined; return [{ label, score }]; });
}

export function AppealCenterPage({ mode, canRead, canManage, canWork, canReadIdentities, canReadExams, currentUser, initialExamId = "" }: AppealCenterPageProps) {
  const { message, modal } = App.useApp();
  const [appeals, setAppeals] = useState<Appeal[]>([]); const [selectedId, setSelectedId] = useState(""); const [selected, setSelected] = useState<Appeal | null>(null);
  const [statistics, setStatistics] = useState<AppealStatistics | null>(null); const [identities, setIdentities] = useState<IdentityMaps>({ students: {}, classes: {}, exams: {} }); const [workers, setWorkers] = useState<ManagedUser[]>([]);
  const [examFilter, setExamFilter] = useState(initialExamId || "all"); const [statusFilter, setStatusFilter] = useState("all"); const [keyword, setKeyword] = useState(""); const [assignedTo, setAssignedTo] = useState("");
  const [recommendation, setRecommendation] = useState<SubmitAppealRecommendationPayload["recommendation"]>("accept"); const [decision, setDecision] = useState("accepted"); const [score, setScore] = useState<number | null>(null); const [reason, setReason] = useState("");
  const [loading, setLoading] = useState(true); const [detailLoading, setDetailLoading] = useState(false); const [actioning, setActioning] = useState(""); const [error, setError] = useState("");

  const load = useCallback(async () => {
    if (!canRead) { setLoading(false); return; } setLoading(true); setError("");
    try {
      const response = await listAppeals({ exam_id: examFilter === "all" ? undefined : examFilter, status: statusFilter === "all" ? undefined : statusFilter, limit: 100 });
      const studentIds = Array.from(new Set(response.appeals.map((item) => item.student_id).filter(Boolean)));
      const [studentResult, classResult, examResult, statsResult, workerResult] = await Promise.all([
        canReadIdentities && studentIds.length ? listStudents({ ids: studentIds, limit: 200 }) : Promise.resolve({ students: [] }),
        canReadIdentities ? listClasses() : Promise.resolve({ classes: [] }), canReadExams ? listExams({ limit: 200 }) : Promise.resolve({ exams: [] }),
        canManage ? getAppealStatistics(examFilter === "all" ? undefined : examFilter) : Promise.resolve({ statistics: null }), canManage ? listManagedUsers({ limit: 200 }) : Promise.resolve({ users: [] })
      ]);
      setAppeals(response.appeals); setSelectedId((current) => response.appeals.some((item) => item.id === current) ? current : response.appeals[0]?.id ?? "");
      setIdentities({ students: Object.fromEntries(studentResult.students.map((item) => [item.id, item])), classes: Object.fromEntries(classResult.classes.map((item) => [item.id, item])), exams: Object.fromEntries(examResult.exams.map((item) => [item.id, item])) });
      setStatistics(statsResult.statistics as AppealStatistics | null); setWorkers(workerResult.users.filter((user) => user.status === "active" && user.roles.some((role) => ["teacher", "grader", "arbitrator"].includes(role))));
    } catch (failure) { setError(formatError(failure)); setAppeals([]); } finally { setLoading(false); }
  }, [canManage, canRead, canReadExams, canReadIdentities, examFilter, statusFilter]);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    if (!selectedId) { setSelected(null); return; } setDetailLoading(true);
    void getAppeal(selectedId).then(({ appeal }) => { setSelected(appeal); setAssignedTo(appeal.assigned_to ?? ""); setRecommendation((appeal.teacher_recommendation as SubmitAppealRecommendationPayload["recommendation"]) || "accept"); setDecision(["accepted", "rejected", "score_adjusted", "need_more_info"].includes(appeal.status) ? appeal.status : "accepted"); setScore(evidenceNumber(appeal, "score") ?? null); setReason(""); }).catch((failure) => setError(formatError(failure))).finally(() => setDetailLoading(false));
  }, [selectedId]);

  const selectedStudent = selected ? identities.students[selected.student_id] : undefined; const selectedClass = selectedStudent ? identities.classes[selectedStudent.class_id] : undefined;
  const currentScore = evidenceNumber(selected, "score"); const maxScore = evidenceNumber(selected, "max_score"); const terminal = Boolean(selected && ["accepted", "rejected", "score_adjusted", "closed"].includes(selected.status));
  const canSubmitTeacherRecommendation = canSubmitTeacherAppealRecommendation({ experience: mode, canWork, terminal, assignedTo: selected?.assigned_to, currentUserId: currentUser.id });
  const workerNames = useMemo(() => Object.fromEntries(workers.map((worker) => [worker.id, worker.display_name || worker.username])), [workers]);
  const workerOptions = useMemo(() => workers.map((worker) => ({ value: worker.id, label: worker.display_name || worker.username })), [workers]);
  const examOptions = useMemo(() => Object.values(identities.exams).map((exam) => ({ value: exam.id, label: `${exam.name} · ${examSubjectLabel(exam.subject)}` })), [identities.exams]);
  const filtered = useMemo(() => { const term = keyword.trim().toLowerCase(); return term ? appeals.filter((appeal) => [appeal.anonymous_code, appeal.exam_name, appeal.question_no, readableReason(appeal.reason), identities.students[appeal.student_id]?.name].some((value) => value?.toLowerCase().includes(term))) : appeals; }, [appeals, identities.students, keyword]);
  const columns = useMemo<TableColumnsType<Appeal>>(() => [
    { title: mode === "teacher" ? "答卷编号" : "学生", key: "person", width: 140, render: (_, appeal) => mode === "teacher" ? appeal.anonymous_code || "匿名答卷" : identities.students[appeal.student_id]?.name || "学生信息未匹配" },
    { title: "考试与科目", key: "exam", render: (_, appeal) => <div className="appeal-table-primary"><strong>{identities.exams[appeal.exam_id]?.name ?? appeal.exam_name ?? "未命名考试"}</strong><span>{appeal.subject ? examSubjectLabel(appeal.subject) : "未标注学科"}</span></div> },
    { title: "题号", dataIndex: "question_no", width: 80, render: (value?: string) => value || "整卷" }, { title: "申诉原因", dataIndex: "reason", render: readableReason },
    { title: "复核进度", key: "review", width: 150, render: (_, appeal) => appeal.assigned_to ? `已分派 · ${appeal.teacher_recommendation ? "已反馈" : "待反馈"}` : "待系统分派" },
    { title: "状态", dataIndex: "status", width: 110, render: (value: string) => <StatusTag tone={statusTone(value)}>{statusLabels[value] ?? "处理中"}</StatusTag> }, { title: "提交时间", dataIndex: "created_at", width: 170, render: formatTime }
  ], [identities.exams, identities.students, mode]);
  const refreshSelected = async (id: string) => { await load(); const response = await getAppeal(id); setSelectedId(id); setSelected(response.appeal); };
  const reassign = async () => { if (!selected || !assignedTo) return; setActioning("assign"); try { await assignAppeal(selected.id, assignedTo, selected.revision); message.success("复核任务已重新分派"); await refreshSelected(selected.id); } catch (failure) { message.error(formatError(failure)); } finally { setActioning(""); } };
  const submitTeacherReview = async () => { if (!selected || !canSubmitTeacherRecommendation || reason.trim().length < 10) { message.error("请确认任务已分派给当前账号，并填写不少于 10 个字的复核依据"); return; } setActioning("recommend"); try { await submitAppealRecommendation(selected.id, { recommendation, reason: reason.trim(), ...(recommendation === "adjust_score" && score !== null ? { recommended_score: score } : {}), expected_revision: selected.revision }); message.success("独立复核意见已提交"); await refreshSelected(selected.id); } catch (failure) { message.error(formatError(failure)); } finally { setActioning(""); } };
  const submitAdminDecision = () => {
    if (!selected || reason.trim().length < 6) { message.error("请填写最终处理说明"); return; }
    const payload: ReviewAppealPayload = { status: decision, reason: reason.trim(), assigned_to: selected.assigned_to, final_grade_id: selected.final_grade_id, expected_revision: selected.revision, ...(decision === "score_adjusted" && score !== null ? { adjusted_score: score } : {}) };
    modal.confirm({ title: decision === "score_adjusted" ? "确认调整成绩" : "确认提交最终结论", content: decision === "score_adjusted" ? `本题将由 ${formatScore(currentScore)} 分调整为 ${formatScore(score)} 分。` : "提交后该结论将作为学生可见的正式处理结果。", okText: "确认提交", cancelText: "取消", onOk: async () => { setActioning("decision"); try { await reviewAppeal(selected.id, payload); message.success("最终处理已提交"); await refreshSelected(selected.id); } catch (failure) { message.error(formatError(failure)); } finally { setActioning(""); } } });
  };

  return <div className={`appeal-shell appeal-shell-v2 ${mode === "teacher" ? "teacher" : "admin"}`}>
    <section className="appeal-topbar"><div><h1>{mode === "teacher" ? "复核任务" : "成绩复核"}</h1><p>{mode === "teacher" ? "独立核对匿名答卷和评分标准，提交复核结论。" : "查看学生复核申请、复核进度与需要终审的争议。"}</p></div><Space wrap><Select className="appeal-filter-select" value={examFilter} options={[{ value: "all", label: "全部考试" }, ...examOptions]} onChange={setExamFilter} /><Select className="appeal-filter-select narrow" value={statusFilter} options={[{ value: "all", label: "全部状态" }, ...Object.entries(statusLabels).map(([value, label]) => ({ value, label }))]} onChange={setStatusFilter} /><Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button></Space></section>
    {!canRead ? <Alert type="error" showIcon message="当前账号无权查看复核任务" /> : null}{error ? <ErrorState message={error} onRetry={() => void load()} /> : null}
    {mode === "admin" ? <section className="appeal-summary-strip compact"><div><span>全部申请</span><strong>{statistics?.total ?? appeals.length}</strong></div><div><span>等待处理</span><strong>{statistics ? (statistics.by_status.submitted ?? 0) + (statistics.by_status.under_review ?? 0) : appeals.filter((item) => ["submitted", "under_review"].includes(item.status)).length}</strong></div><div><span>需要终审</span><strong>{appeals.filter((item) => item.teacher_recommendation === "adjust_score" || item.status === "need_more_info").length}</strong></div></section> : null}
    <section className="appeal-queue-panel"><div className="appeal-queue-head"><div><h2>{mode === "teacher" ? "分配给我的任务" : "复核申请"}</h2><p>{filtered.length} 条记录</p></div><Input prefix={<Search size={16} />} allowClear value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder={mode === "teacher" ? "搜索考试、题号或原因" : "搜索学生、考试、题号或原因"} /></div>{loading ? <LoadingState label="正在读取复核申请" /> : <ResponsiveTable className="dense-data-table" rowKey="id" size="small" columns={columns} dataSource={filtered} pagination={{ pageSize: 8 }} onRow={(record) => ({ onClick: () => setSelectedId(record.id) })} rowClassName={(record) => record.id === selectedId ? "selected-table-row" : ""} locale={{ emptyText: <EmptyState title="暂无复核申请" description="当前筛选条件下没有待处理记录。" /> }} />}</section>
    {detailLoading ? <section className="appeal-case-panel"><LoadingState label="正在读取答卷与复核信息" /></section> : selected ? <section className="appeal-case-panel">
      <header className="appeal-case-head"><div><h2>{identities.exams[selected.exam_id]?.name ?? selected.exam_name ?? "考试"} · {selected.subject ? examSubjectLabel(selected.subject) : "未标注学科"} · {selected.question_no || "整卷"}</h2><p>{mode === "teacher" ? `匿名答卷 ${selected.anonymous_code || "未编号"}` : `${selectedStudent?.name ?? "学生信息未匹配"}${selectedClass?.name ? ` · ${selectedClass.name}` : ""} · 提交于 ${formatTime(selected.created_at)}`}</p></div><StatusTag tone={statusTone(selected.status)}>{statusLabels[selected.status] ?? "处理中"}</StatusTag></header>
      <div className="appeal-case-grid"><main className="appeal-evidence-main"><section><h3>申诉内容</h3><div className="appeal-reason-box"><Tag color="blue">{readableReason(selected.reason)}</Tag><p>{readableReasonDetail(selected.reason)}</p></div></section><section><h3>答题区域</h3><div className="appeal-answer-grid"><div><span>原卷作答</span><p>{selected.evidence?.raw_answer || "暂无可展示的原卷答题内容"}</p></div><div><span>文字识别结果</span><p>{selected.evidence?.ocr_text || "暂无识别文本，请以原卷作答为准"}</p></div></div></section><section><h3>评分标准</h3>{rubricItems(selected).length ? <div className="appeal-rubric-list">{rubricItems(selected).map((item, index) => <div key={`${item.label}-${index}`}><span>{item.label}</span><strong>{item.score === undefined ? "" : `${formatScore(item.score)} 分`}</strong></div>)}</div> : <p className="muted">暂无结构化评分要点，请按本题正式评分标准复核。</p>}</section>{mode === "admin" ? <section><h3>原处理结果</h3><div className="appeal-result-strip"><div><span>原最终分</span><strong>{formatScore(currentScore)} / {formatScore(maxScore)}</strong></div><div><span>复核教师</span><strong>{selected.assigned_to ? workerNames[selected.assigned_to] ?? "已分派" : "等待系统分派"}</strong></div><div><span>复核意见</span><strong>{selected.teacher_recommendation ? recommendationLabels[selected.teacher_recommendation] ?? "已反馈" : "待反馈"}</strong></div></div>{selected.teacher_recommendation_reason ? <p className="appeal-review-note">{selected.teacher_recommendation_reason}</p> : null}</section> : null}</main>
        <aside className="appeal-decision-panel">{mode === "teacher" ? <><h3>提交独立复核</h3><p className="muted">学生姓名、班级和原阅卷教师已隐藏。复核建议仅供管理员终审，不会直接修改成绩或关闭申诉；请只依据答卷和评分标准判断。</p><Select value={recommendation} options={recommendationOptions} onChange={setRecommendation} disabled={!canSubmitTeacherRecommendation} />{recommendation === "adjust_score" ? <InputNumber min={0} max={maxScore} precision={2} value={score} onChange={setScore} placeholder="建议分数" /> : null}<Input.TextArea rows={5} value={reason} onChange={(event) => setReason(event.target.value)} maxLength={500} placeholder="写明对应评分点、答卷证据和结论（不少于10字）" /><Button type="primary" icon={<Send size={16} />} disabled={!canSubmitTeacherRecommendation} loading={actioning === "recommend"} onClick={() => void submitTeacherReview()}>提交复核意见</Button></> : <><h3>复核进度与终审</h3><div className="appeal-routing"><span>当前分派</span><strong>{selected.assigned_to ? workerNames[selected.assigned_to] ?? "已分派教师" : "尚未分派"}</strong><small>当前需管理员核验复核人不得为原阅卷人；双人自动分派启用后将接管正常流转。</small></div><details className="appeal-reassign"><summary>异常情况下重新分派</summary><Select showSearch optionFilterProp="label" value={assignedTo || undefined} options={workerOptions} onChange={setAssignedTo} placeholder="选择其他复核教师" /><Button icon={<UserRoundCheck size={16} />} disabled={!canManage || terminal || !assignedTo} loading={actioning === "assign"} onClick={() => void reassign()}>重新分派</Button></details><div className="appeal-decision-divider" /><Select value={decision} onChange={setDecision} disabled={!canManage || terminal} options={[{ value: "accepted", label: "申诉成立，维持当前分数" }, { value: "rejected", label: "申诉不成立，维持原分" }, { value: "score_adjusted", label: "申诉成立，调整分数" }, { value: "need_more_info", label: "争议较大，继续复核" }]} />{decision === "score_adjusted" ? <InputNumber min={0} max={maxScore} precision={2} value={score} onChange={setScore} placeholder="调整后分数" /> : null}<Input.TextArea rows={5} value={reason} onChange={(event) => setReason(event.target.value)} maxLength={500} placeholder="填写学生可见的最终处理说明" /><Button type="primary" icon={<CheckCircle2 size={16} />} disabled={!canManage || terminal} loading={actioning === "decision"} onClick={submitAdminDecision}>提交最终结论</Button></>}</aside>
      </div></section> : null}
  </div>;
}
export default AppealCenterPage;


