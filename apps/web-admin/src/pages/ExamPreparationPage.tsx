import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Checkbox, Modal, Space } from "antd";
import { ArrowRight, ClipboardCheck, RefreshCw, Save } from "lucide-react";
import { ApiClientError, getSafeUserText, getUserErrorMessage } from "../api/client";
import { confirmExamReadiness, getExamReadiness, type ExamReadiness } from "../api/configuration";
import { getExam, refreshExamCandidates, updateExam, type Exam } from "../api/exams";
import { listClasses, listGrades, type Grade, type SchoolClass } from "../api/org";
import { ErrorState, LoadingState } from "../components/PageState";
import { preparationSteps } from "../features/exams/preparation/preparationSteps";
import { ExamScoringPreparation } from "../components/features/assessment/ExamScoringPreparation";

export function readinessCheckRoute(examId: string, section: string) {
  if (section === "exam") return "/exams";
  const routeSection = preparationSteps.find((step) => step.key === section)?.route ?? "settings";
  return `/exams/${encodeURIComponent(examId)}/${routeSection}`;
}

// 每个年级只替换本组勾选，保留其他年级已选班级。
export function mergeClassSelection(selected: string[], groupIds: string[], groupValues: string[]) {
  const group = new Set(groupIds);
  return [...selected.filter((id) => !group.has(id)), ...groupValues];
}

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error(`开考准备操作失败：${error.status} ${error.code}`, error);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

const questionTypeLabels: Record<string, string> = {
  single_choice: "单选题",
  multiple_choice: "多选题",
  true_false: "判断题",
  fill_blank: "填空题",
  numeric: "数值题",
  formula: "公式题",
  short_answer: "简答题",
  calculation: "计算题",
  essay: "作文",
  discussion: "论述题",
  coding: "编程题"
};

interface PreparationFreezeReview {
  examId: string;
  version: number;
  configurationHash: string;
}

function hasReadinessConfirmation(readiness?: ExamReadiness) {
  // 后续阅卷阶段 confirmed 可能为 false；服务端只返回仍匹配当前配置的已通过快照。
  return Boolean(readiness?.confirmed || (readiness?.snapshot_id && readiness.confirmed_at));
}

function assertFreezeReady(readiness: ExamReadiness) {
  if (!readiness.ready || readiness.checks.some(check => !check.passed) || readiness.scoring_preview?.some(item => item.blocking_reason) || hasReadinessConfirmation(readiness)) {
    throw new Error("准备状态已变化，请重新检查后再确认");
  }
}

export function ExamStudentScopePage({ examId, canManage, onExamChanged }: Readonly<{ examId: string; canManage: boolean; onExamChanged?: () => void }>) {
  const { message } = App.useApp();
  const [exam, setExam] = useState<Exam>();
  const [classes, setClasses] = useState<SchoolClass[]>([]);
  const [grades, setGrades] = useState<Grade[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
	const [syncing, setSyncing] = useState(false);
  const [error, setError] = useState<string>();
  // 考试标识隔离跨考试结果，请求序号另外淘汰同一考试的旧刷新。
  const currentExamId = useRef(examId);
  currentExamId.current = examId;
  const loadVersion = useRef(0);

  const load = useCallback(async () => {
    const version = ++loadVersion.current;
    setLoading(true);
    setError(undefined);
    try {
      const [examResponse, classResponse, gradeResponse] = await Promise.all([getExam(examId), listClasses(), listGrades()]);
      if (version !== loadVersion.current || currentExamId.current !== examId) return;
      setExam(examResponse.exam);
      setClasses(classResponse.classes.filter((item) => item.school_id === examResponse.exam.school_id));
      setGrades(gradeResponse.grades.filter((item) => item.school_id === examResponse.exam.school_id));
      setSelected(examResponse.exam.class_ids);
    } catch (loadError) { if (version === loadVersion.current && currentExamId.current === examId) setError(formatError(loadError)); }
    finally { if (version === loadVersion.current && currentExamId.current === examId) setLoading(false); }
  }, [examId]);

  useEffect(() => {
    setExam(undefined); setClasses([]); setGrades([]); setSelected([]); setLoading(true); setSaving(false); setSyncing(false);
    void load();
    return () => { loadVersion.current++; };
  }, [load]);

  const gradeById = useMemo(() => new Map(grades.map((item) => [item.id, item])), [grades]);
  const changed = exam ? [...selected].sort((left, right) => left.localeCompare(right)).join(",") !== [...exam.class_ids].sort((left, right) => left.localeCompare(right)).join(",") : false;
  // 准备确认后名册已经冻结；后续阶段从考试列表查看准备配置时，班级选择保持只读。
  const canEditScope = canManage && Boolean(exam && ["draft", "configured"].includes(exam.status));

  async function save() {
    if (!canEditScope || exam?.id !== currentExamId.current || !selected.length) { message.error("请等待当前考试加载并至少选择一个班级"); return; }
    const requestExamId = examId;
    setSaving(true);
    try {
      const response = await updateExam(exam.id, { class_ids: selected, expected_revision: exam.revision });
      if (currentExamId.current !== requestExamId) return;
      setExam(response.exam);
      onExamChanged?.();
      message.success("学生范围已保存；开考准备需要重新确认。");
    } catch (saveError) { if (currentExamId.current === requestExamId) message.error(formatError(saveError)); }
    finally { if (currentExamId.current === requestExamId) setSaving(false); }
  }

  async function syncCandidates() {
	if (exam?.id !== currentExamId.current) return;
	const requestExamId = examId;
	setSyncing(true);
	try {
	  const { candidate_refresh: result } = await refreshExamCandidates(exam.id);
	  if (currentExamId.current !== requestExamId) return;
	  const changes = [result.added_count ? `新增 ${result.added_count} 人` : "", result.removed_count ? `移除 ${result.removed_count} 人` : ""].filter(Boolean).join("，");
	  message.success(changes ? `学生名单已同步：${changes}，当前 ${result.after_count} 人` : `学生名单已是最新，共 ${result.after_count} 人`);
	} catch (syncError) { if (currentExamId.current === requestExamId) message.error(formatError(syncError)); }
	finally { if (currentExamId.current === requestExamId) setSyncing(false); }
  }

  if (loading || (exam && exam.id !== examId)) return <LoadingState label="正在加载学生范围" />;
  if (error) return <ErrorState message={error} onRetry={() => void load()} />;

  return (
    <div className="preparation-page">
      <section className="preparation-heading"><div><h2>学生范围</h2><p>选择参加本场考试的班级；确认准备完成时，系统将按当前在读学籍生成最终冻结名单。</p></div><Space><Button icon={<RefreshCw size={16} />} disabled={!canEditScope || changed} loading={syncing} onClick={() => void syncCandidates()}>同步学生名单</Button><Button type="primary" icon={<Save size={16} />} disabled={!canEditScope || !changed || !selected.length} loading={saving} onClick={() => void save()}>保存范围</Button></Space></section>
      {!canEditScope && exam && !["draft", "configured"].includes(exam.status) ? <Alert type="info" showIcon message="本场学生名单已冻结" description="学生范围和名单同步已关闭。若发现漏选或名单错误，请联系考试负责人核对处理方案，不能通过修改学校学籍直接改变本场冻结名单。" /> : null}
      {!classes.length ? <Alert type="warning" showIcon message="当前学校还没有班级" description="请先在组织与用户中创建年级、班级并导入学生。" /> : (
        <div className="class-scope-list">
          {grades.map((grade) => {
            const gradeClasses = classes.filter((item) => item.grade_id === grade.id);
            if (!gradeClasses.length) return null;
            return <section key={grade.id}><div><strong>{grade.name}</strong><span>{grade.academic_year}</span></div><Checkbox.Group value={selected} onChange={(values) => setSelected((previous) => mergeClassSelection(previous, gradeClasses.map((item) => item.id), values as string[]))} disabled={!canEditScope}>{gradeClasses.map((item) => <Checkbox key={item.id} value={item.id}><span>{item.name}</span><small>{item.code}</small></Checkbox>)}</Checkbox.Group></section>;
          })}
          {classes.filter((item) => !gradeById.has(item.grade_id)).length ? <section><div><strong>未分组班级</strong></div><Checkbox.Group value={selected} onChange={(values) => setSelected((previous) => mergeClassSelection(previous, classes.filter((item) => !gradeById.has(item.grade_id)).map((item) => item.id), values as string[]))} disabled={!canEditScope}>{classes.filter((item) => !gradeById.has(item.grade_id)).map((item) => <Checkbox key={item.id} value={item.id}>{item.name}</Checkbox>)}</Checkbox.Group></section> : null}
        </div>
      )}
    </div>
  );
}

function readinessStatusDisplay(confirmed: boolean, editable: boolean, failedCount: number, ready: boolean) {
  if (confirmed) return { label: "考试准备已完成", className: "is-complete" };
  const className = failedCount > 0 ? "is-pending" : "is-neutral";
  if (!editable) return { label: "准备配置只读", className };
  if (failedCount > 0) return { label: `待处理 ${failedCount} 项`, className };
  return { label: ready ? "待确认冻结" : "待重新检查", className };
}

function readinessActionHint(canManage: boolean, editable: boolean, loading: boolean, failedCount: number, ready: boolean) {
  if (!canManage) return "当前账号没有准备确认权限，请联系考试负责人。";
  if (!editable) return "本场已进入后续考试阶段，不能重新冻结考试准备。";
  if (loading) return "正在读取准备状态，请等待检查完成。";
  if (failedCount > 0) return "请先完成待处理项目，再重新检查。";
  if (!ready) return "准备检查尚未通过，请重新检查。";
  return "评分方案已匹配，可以确认准备完成。";
}

function buildReadinessDisplay({ readiness, exam, examId, canManage, loading, working, reviewLoading, freezeError, freezeReview, reviewBeforeFreeze, onNavigate }: {
  readiness: ExamReadiness;
  exam: Exam;
  examId: string;
  canManage: boolean;
  loading: boolean;
  working: boolean;
  reviewLoading: boolean;
  freezeError: string;
  freezeReview: PreparationFreezeReview | null;
  reviewBeforeFreeze: () => Promise<void>;
  onNavigate: (path: string) => void;
}) {
  const confirmed = hasReadinessConfirmation(readiness);
  // 这里统计准备检查项目；评分区另按题目统计，二者不能相加作为待处理数量。
  const failedCount = readiness.checks.filter((check) => !check.passed).length;
  const scoringBlocked = readiness.scoring_preview?.some(item => item.blocking_reason);
  const editable = ["draft", "configured"].includes(exam.status);
  const status = readinessStatusDisplay(confirmed, editable, failedCount, readiness.ready);
  const actionHint = readinessActionHint(canManage, editable, loading, failedCount, readiness.ready);
  return {
    status: <span className={`readiness-status ${status.className}`} role="status">{status.label}</span>,
    actionHint: !confirmed ? actionHint : "",
    freezeAction: !confirmed ? <Button type="primary" icon={<ClipboardCheck size={16} />} disabled={!canManage || loading || working || !readiness.ready || failedCount > 0 || scoringBlocked || !editable} loading={reviewLoading} onClick={() => void reviewBeforeFreeze()}>确认准备完成</Button> : null,
    reviewFailure: freezeError && !freezeReview ? <Alert type="error" showIcon message={confirmed ? "准备已确认，状态刷新未完成" : "暂时无法完成冻结核对"} description={freezeError} /> : null,
    captureAction: confirmed && ["ready", "collecting"].includes(exam.status) ? <div className="exam-setup-actions"><Button type="primary" icon={<ArrowRight size={16} />} onClick={() => onNavigate(`/exams/${encodeURIComponent(examId)}/capture`)}>前往答卷导入</Button></div> : null,
    confirmedNotice: confirmed ? <p className="readiness-state-note">准备已确认，当前配置有匹配的确认记录；准备配置只读。</p> : null,
    frozenNotice: !confirmed && !editable ? <p className="readiness-state-note">当前没有匹配的准备确认记录，请联系考试负责人核对。</p> : null,
    collectingNotice: exam.status === "collecting" ? <p className="readiness-state-note">考试已进入答卷导入阶段，后续处理请前往答卷导入工作区。</p> : null
  };
}

export function ExamReadinessPage({ examId, canManage, canManageAssessment = false, onNavigate, onExamChanged }: Readonly<{ examId: string; canManage: boolean; canManageAssessment?: boolean; onNavigate: (path: string) => void; onExamChanged?: () => void }>) {
  const { message } = App.useApp();
  const [exam, setExam] = useState<Exam>();
  const [readiness, setReadiness] = useState<ExamReadiness>();
  const [loading, setLoading] = useState(true);
  const [working, setWorking] = useState(false);
  const [reviewLoading, setReviewLoading] = useState(false);
  const [freezeReview, setFreezeReview] = useState<PreparationFreezeReview | null>(null);
  const [freezeError, setFreezeError] = useState("");
  const [error, setError] = useState<string>();
  const currentExamId = useRef(examId);
  currentExamId.current = examId;
  const latestFreezeAccess = useRef({ examId, canManage, exam, readiness });
  latestFreezeAccess.current = { examId, canManage, exam, readiness };
  const loadVersion = useRef(0);
  // 关闭窗口、重新核对或切换考试后，旧读取及旧窗口回调不能恢复已经丢弃的核对内容。
  const freezeVersion = useRef(0);
  // state 控制忙碌展示；ref 在同一事件内拦截重复调用及确认开始后尚未重绘的关闭回调。
  const freezeWorking = useRef(false);
  const freezeReviewLoading = useRef(false);

  const load = useCallback(async () => {
    const version = ++loadVersion.current;
    setLoading(true);
    setError(undefined);
    try {
      const [examResponse, readinessResponse] = await Promise.all([getExam(examId), getExamReadiness(examId)]);
      if (version !== loadVersion.current || currentExamId.current !== examId) return false;
      setExam(examResponse.exam);
      setReadiness(readinessResponse.readiness);
      setFreezeError("");
      return true;
    } catch (loadError) {
      if (version === loadVersion.current && currentExamId.current === examId) {
        setError(formatError(loadError));
      }
      return false;
    }
    finally { if (version === loadVersion.current && currentExamId.current === examId) setLoading(false); }
  }, [examId]);

  useEffect(() => {
    freezeWorking.current = false; freezeReviewLoading.current = false;
    setExam(undefined); setReadiness(undefined); setLoading(true); setWorking(false); setFreezeReview(null); setFreezeError(""); setReviewLoading(false);
    void load();
    return () => { loadVersion.current++; freezeVersion.current++; };
  }, [load]);

  const failedChecks = readiness?.checks.filter((check) => !check.passed) ?? [];

  function canFreezeCurrent() {
    const current = latestFreezeAccess.current;
    return current.examId === examId && current.canManage && current.exam?.id === examId
      && ["draft", "configured"].includes(current.exam.status) && Boolean(current.readiness?.ready)
      && !current.readiness?.checks.some((check) => !check.passed) && !current.readiness?.scoring_preview?.some(item => item.blocking_reason)
      && !hasReadinessConfirmation(current.readiness);
  }

  function closeFreezeReview() {
    if (freezeWorking.current) return;
    freezeVersion.current++;
    freezeReviewLoading.current = false;
    setReviewLoading(false);
    setFreezeReview(null);
    setFreezeError("");
  }

  async function reviewBeforeFreeze() {
    // 冻结后名单同步已关闭；后续阶段即使准备记录缺失，也不能再次发起这套准备复核。
    if (!canFreezeCurrent() || freezeWorking.current || freezeReviewLoading.current) return;
    const requestExamId = examId;
    const version = ++freezeVersion.current;
    const isCurrentReview = () => currentExamId.current === requestExamId && version === freezeVersion.current;
    freezeReviewLoading.current = true;
    setReviewLoading(true); setFreezeError("");
    try {
      // 有效评分方案和版本由同次准备检查给出；最终名单在服务端确认事务中生成，不在打开窗口时另做同步。
      const response = await getExamReadiness(requestExamId);
      if (!isCurrentReview() || !canFreezeCurrent()) return;
      setReadiness(response.readiness);
      assertFreezeReady(response.readiness);
      setFreezeReview({ examId: requestExamId, version, configurationHash: response.readiness.configuration_hash });
    } catch (reviewError) {
      if (isCurrentReview()) setFreezeError(formatError(reviewError));
    } finally { if (isCurrentReview()) { freezeReviewLoading.current = false; setReviewLoading(false); } }
  }

  async function confirm() {
    if (!canFreezeCurrent() || freezeWorking.current || exam?.id !== currentExamId.current || freezeReview?.examId !== examId || freezeReview.version !== freezeVersion.current) return;
    const requestExamId = examId;
    const version = freezeReview.version;
    const isCurrentReview = () => currentExamId.current === requestExamId && version === freezeVersion.current;
    freezeWorking.current = true;
    let completed = false;
    setWorking(true);
    setFreezeError("");
    try {
      const latest = await getExamReadiness(examId);
      // 权限或状态可能在预读期间改变；旧窗口的 canManage 闭包不能继续发出冻结写请求。
      if (!isCurrentReview() || !canFreezeCurrent()) return;
      if (!latest.readiness.ready || latest.readiness.checks.some((check) => !check.passed) || latest.readiness.scoring_preview?.some(item => item.blocking_reason) || hasReadinessConfirmation(latest.readiness) || latest.readiness.configuration_hash !== freezeReview.configurationHash) {
        setReadiness(latest.readiness);
        throw new Error("考试准备内容已变化，请关闭确认窗口并重新检查");
      }
      // 服务端在考试锁内比较这个复核版本，覆盖本次预读结束到 POST 到达之间的变化。
      const response = await confirmExamReadiness(examId, freezeReview.configurationHash);
      if (!isCurrentReview()) return;
      setReadiness(response.readiness);
      setFreezeReview(null);
      const refreshed = await load();
      if (!isCurrentReview()) return;
      completed = true;
      onExamChanged?.();
      if (refreshed) message.success("考试准备已确认完成");
      else {
        setFreezeError("考试准备已确认，但页面状态暂未重新读取。请点击“重新检查”读取结果，无需再次确认。");
        message.error("考试准备已确认，状态刷新未完成；请重新检查，无需再次确认。");
      }
    } catch (confirmError) { if (isCurrentReview()) { setFreezeError(formatError(confirmError)); message.error(formatError(confirmError)); } }
    finally { if (isCurrentReview()) { freezeWorking.current = false; setWorking(false); if (completed) freezeVersion.current++; } }
  }

  if ((loading && !readiness) || (exam && exam.id !== examId)) return <LoadingState label="正在执行开考准备检查" />;
  if (error && !readiness) return <ErrorState message={error} onRetry={() => void load()} />;
  if (!readiness || !exam) return null;

  const display = buildReadinessDisplay({ readiness, exam, examId, canManage, loading, working, reviewLoading, freezeError, freezeReview, reviewBeforeFreeze, onNavigate });

  return (
    <div className="preparation-page readiness-page exam-setup-form-page">
      <section className="preparation-heading readiness-heading">
        <div><div className="readiness-heading-title"><h2>确认准备完成</h2>{display.status}</div><p>确认后冻结学生名单、题目、答案、评分配置和答题区域，之后不能直接修改。</p></div>
        <div className="readiness-heading-actions"><Space wrap>{display.freezeAction}<Button icon={<RefreshCw size={16} />} disabled={working || reviewLoading} loading={loading} onClick={() => void load()}>重新检查</Button></Space>{display.actionHint ? <p className="readiness-action-hint">{display.actionHint}</p> : null}</div>
      </section>
      {display.confirmedNotice}
      {display.frozenNotice}
      {error ? <Alert type="error" showIcon message="准备状态读取失败" description={error} /> : null}
      {display.reviewFailure}
      {failedChecks.length ? <ul className="exam-setup-inline-issues exam-readiness-issues" aria-label="待处理准备检查">{failedChecks.map((check) => <li key={check.code}>
        <div><strong>{getSafeUserText(check.label, "需要完善")}</strong><span>{getSafeUserText(check.message, "请完成相关设置")}</span></div>
        <Button type="link" onClick={() => onNavigate(readinessCheckRoute(examId, check.section))}>去处理 <ArrowRight size={15} /></Button>
      </li>)}</ul> : null}
      <ExamScoringPreparation key={exam.id} exam={exam} preview={readiness.scoring_preview} canManage={canManageAssessment} onNavigate={onNavigate} />
      {display.captureAction}

      {(readiness.advisories ?? []).map((advisory) => (
        <Alert
          key={advisory.code}
          className="readiness-advisory"
          type={advisory.severity === "warning" ? "warning" : "info"}
          showIcon
          message={<span>{advisory.label}<small>提醒项，不阻断开考</small></span>}
          description={
            <>
              <p>{advisory.message}</p>
              {advisory.questions.length ? (
                <ul>
                  {advisory.questions.map((question) => (
                    <li key={question.question_id}><strong>{question.question_no}</strong><span>{questionTypeLabels[question.question_type] ?? question.question_type}</span><span>{question.score.toFixed(2)} 分</span></li>
                  ))}
                </ul>
              ) : null}
            </>
          }
          action={<Button size="small" onClick={() => onNavigate(`/exams/${encodeURIComponent(examId)}/${advisory.section}`)}>查看题目</Button>}
        />
      ))}

      {display.collectingNotice}
      <PreparationFreezeModal freezeReview={freezeReview} working={working} disabled={!canFreezeCurrent() || loading}
        freezeError={freezeError} onCancel={closeFreezeReview} onConfirm={confirm} />
    </div>
  );
}

function PreparationFreezeModal({ freezeReview, working, disabled, freezeError, onCancel, onConfirm }: Readonly<{
  freezeReview: PreparationFreezeReview | null; working: boolean; disabled: boolean;
  freezeError: string; onCancel: () => void;
  onConfirm: () => Promise<void>;
}>) {
  return (
      <Modal title="确认准备完成" width={560} open={Boolean(freezeReview)} confirmLoading={working} okButtonProps={{ disabled: disabled }} maskClosable={!working} closable={!working} okText="确认冻结并完成准备" cancelText="返回修改" onCancel={onCancel} onOk={() => void onConfirm()}>
        {freezeReview ? <>
          <Alert type="warning" showIcon message="确认后将冻结本场考试准备" description="系统将按当前在读学籍生成最终学生名单，并冻结题目、答案、评分方案、评分细则和答题区域，之后不能直接修改。" />
          <p>确认准备完成后，可前往答卷导入；阅卷与成绩请从各自入口进入。</p>
          {freezeError ? <Alert type="error" showIcon message="冻结未完成，可重新检查或重试" description={freezeError} /> : null}
        </> : null}
      </Modal>
  );
}
