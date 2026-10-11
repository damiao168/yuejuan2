import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Checkbox, Modal, Space, Table } from "antd";
import { ArrowRight, ClipboardCheck, RefreshCw, Save } from "lucide-react";
import { ApiClientError, getSafeUserText, getUserErrorMessage } from "../api/client";
import { confirmExamReadiness, getExamReadiness, type ExamReadiness } from "../api/configuration";
import { getExam, refreshExamCandidates, updateExam, type Exam } from "../api/exams";
import { listClasses, listGrades, type Grade, type SchoolClass } from "../api/org";
import { ErrorState, LoadingState } from "../components/PageState";
import { preparationSteps } from "../features/exams/preparation/preparationSteps";
import { listExamRoster, type RosterEntry, type RosterReport } from "../api/scores";
import { listQuestions } from "../api/papers";
import { getQuestionAssessmentProfile } from "../api/assessment";
import { ExamScoringPreparation } from "../components/features/assessment/ExamScoringPreparation";

export function readinessCheckRoute(examId: string, section: string) {
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

const preparationScoringLabels: Record<string, string> = {
  RULE_AUTO: "规则自动评分", AI_ASSIST: "AI 建议，教师逐题确认", AI_FAST_CONFIRM: "AI 建议，教师快速确认",
  HUMAN_PRIMARY: "人工主评", DUAL_HUMAN: "两名教师独立评分", MANUAL_ONLY: "仅人工评分"
};

interface PreparationFreezeReview {
  examId: string;
  version: number;
  configurationHash: string;
  roster: RosterReport;
  scoring: Array<{ questionId: string; questionNo: string; mode: string }>;
}

function hasVerifiedScoringMode(mode: string) {
  return Object.prototype.hasOwnProperty.call(preparationScoringLabels, mode);
}

const unverifiedScoringMessage = "请先在小题与分值中保存每道题的评分配置，再回来重新核对。未保存或无法识别的策略不能作为冻结确认内容。";

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

function buildReadinessDisplay({ readiness, exam, examId, canManage, loading, reviewLoading, freezeError, freezeReview, reviewBeforeFreeze, onNavigate }: {
  readiness: ExamReadiness;
  exam: Exam;
  examId: string;
  canManage: boolean;
  loading: boolean;
  reviewLoading: boolean;
  freezeError: string;
  freezeReview: PreparationFreezeReview | null;
  reviewBeforeFreeze: () => Promise<void>;
  onNavigate: (path: string) => void;
}) {
  return {
    freezeAction: readiness.ready && !readiness.confirmed && ["draft", "configured"].includes(exam.status) ? <div className="exam-setup-actions"><Button type="primary" icon={<ClipboardCheck size={16} />} disabled={!canManage || loading} loading={reviewLoading} onClick={() => void reviewBeforeFreeze()}>核对并冻结考试准备</Button></div> : null,
    reviewFailure: freezeError && !freezeReview ? <Alert type="error" showIcon message="暂时无法完成冻结核对" description={freezeError} /> : null,
    captureAction: readiness.confirmed && ["ready", "collecting"].includes(exam.status) ? <div className="exam-setup-actions"><Button type="primary" icon={<ArrowRight size={16} />} onClick={() => onNavigate(`/exams/${encodeURIComponent(examId)}/capture`)}>前往答卷导入</Button></div> : null,
    unconfirmedNotice: readiness.ready && !readiness.confirmed ? <Alert type="info" showIcon message="已满足准备确认条件" description="请先核对学生名单和逐题评分方式。确认会冻结本场学生名单、试题、答案、评分细则与答题区域；之后不能直接修改这些考试事实。" /> : null,
    confirmedNotice: readiness.confirmed && exam.status === "ready" ? <Alert type="success" showIcon message="准备已确认" description="可以开始导入学生答卷。" /> : null,
    collectingNotice: exam.status === "collecting" ? <Alert type="success" showIcon message="考试已进入答卷导入阶段" description="后续答卷导入与页面处理将在答卷导入工作区完成。" /> : null
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
      if (version !== loadVersion.current || currentExamId.current !== examId) return;
      setExam(examResponse.exam);
      setReadiness(readinessResponse.readiness);
    } catch (loadError) { if (version === loadVersion.current && currentExamId.current === examId) setError(formatError(loadError)); }
    finally { if (version === loadVersion.current && currentExamId.current === examId) setLoading(false); }
  }, [examId]);

  useEffect(() => {
    freezeWorking.current = false; freezeReviewLoading.current = false;
    setExam(undefined); setReadiness(undefined); setLoading(true); setWorking(false); setFreezeReview(null); setFreezeError(""); setReviewLoading(false);
    void load();
    return () => { loadVersion.current++; freezeVersion.current++; };
  }, [load]);

  const failedChecks = readiness?.checks.filter((check) => !check.passed) ?? [];
  const hasUnverifiedScoring = Boolean(freezeReview && (!freezeReview.scoring.length || freezeReview.scoring.some((item) => !hasVerifiedScoringMode(item.mode))));

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
    if (!canManage || !["draft", "configured"].includes(exam?.status ?? "") || freezeWorking.current || freezeReviewLoading.current) return;
    const requestExamId = examId;
    const version = ++freezeVersion.current;
    const isCurrentReview = () => currentExamId.current === requestExamId && version === freezeVersion.current;
    freezeReviewLoading.current = true;
    setReviewLoading(true); setFreezeError("");
    try {
      // 名单同步与各题策略不是同一次读取；前后核对准备版本，拒绝把旧名单与新配置组合成确认内容。
      const before = await getExamReadiness(requestExamId);
      if (!isCurrentReview()) return;
      if (!before.readiness.ready || before.readiness.confirmed) throw new Error("准备状态已变化，请重新检查后再确认");
      await refreshExamCandidates(requestExamId);
      if (!isCurrentReview()) return;
      const [rosterResponse, questionsResponse] = await Promise.all([
        listExamRoster(requestExamId), listQuestions(requestExamId)
      ]);
      if (!isCurrentReview()) return;
      const scoring = await Promise.all(questionsResponse.questions.map(async (question) => {
        try {
          const response = await getQuestionAssessmentProfile(requestExamId, question.id);
          return { questionId: question.id, questionNo: question.question_no, mode: response.assessment_profile.scoring_policy.mode };
        } catch (profileError) {
          // 缺失的落库配置可以由准备检查使用默认值补齐，但不属于管理员已经看到并核实的逐题事实。
          if (profileError instanceof ApiClientError && profileError.status === 404) return { questionId: question.id, questionNo: question.question_no, mode: "UNCONFIGURED" };
          throw profileError;
        }
      }));
      if (!isCurrentReview()) return;
      const after = await getExamReadiness(requestExamId);
      if (!isCurrentReview()) return;
      if (!after.readiness.ready || after.readiness.confirmed || after.readiness.configuration_hash !== before.readiness.configuration_hash) throw new Error("读取期间考试准备内容发生变化，请重新核对名单和评分方式");
      setReadiness(after.readiness);
      setFreezeReview({ examId: requestExamId, version, configurationHash: after.readiness.configuration_hash, roster: rosterResponse.roster, scoring });
    } catch (reviewError) {
      if (isCurrentReview()) setFreezeError(formatError(reviewError));
    } finally { if (isCurrentReview()) { freezeReviewLoading.current = false; setReviewLoading(false); } }
  }

  async function confirm() {
    if (!canManage || freezeWorking.current || exam?.id !== currentExamId.current || freezeReview?.examId !== examId || freezeReview.version !== freezeVersion.current) return;
    // 后台准备检查可接受默认策略；本窗口只确认已保存且可识别的逐题事实，不能通过回调绕过按钮限制。
    if (!freezeReview.scoring.length || freezeReview.scoring.some((item) => !hasVerifiedScoringMode(item.mode))) {
      setFreezeError(unverifiedScoringMessage);
      message.error(unverifiedScoringMessage);
      return;
    }
    const requestExamId = examId;
    const version = freezeReview.version;
    const isCurrentReview = () => currentExamId.current === requestExamId && version === freezeVersion.current;
    freezeWorking.current = true;
    let completed = false;
    setWorking(true);
    setFreezeError("");
    try {
      const latest = await getExamReadiness(examId);
      if (!isCurrentReview()) return;
      if (!latest.readiness.ready || latest.readiness.confirmed || latest.readiness.configuration_hash !== freezeReview.configurationHash) throw new Error("考试准备内容已变化，请关闭确认窗口并重新核对名单和评分方式");
      // 服务端在考试锁内比较这个复核版本，覆盖本次预读结束到 POST 到达之间的变化。
      const response = await confirmExamReadiness(examId, freezeReview.configurationHash);
      if (!isCurrentReview()) return;
      setReadiness(response.readiness);
      setFreezeReview(null);
      await load();
      if (!isCurrentReview()) return;
      completed = true;
      onExamChanged?.();
      message.success("开考准备已确认");
    } catch (confirmError) { if (isCurrentReview()) { setFreezeError(formatError(confirmError)); message.error(formatError(confirmError)); } }
    finally { if (isCurrentReview()) { freezeWorking.current = false; setWorking(false); if (completed) freezeVersion.current++; } }
  }

  if ((loading && !readiness) || (exam && exam.id !== examId)) return <LoadingState label="正在执行开考准备检查" />;
  if (error && !readiness) return <ErrorState message={error} onRetry={() => void load()} />;
  if (!readiness || !exam) return null;

  const display = buildReadinessDisplay({ readiness, exam, examId, canManage, loading, reviewLoading, freezeError, freezeReview, reviewBeforeFreeze, onNavigate });

  return (
    <div className="preparation-page readiness-page exam-setup-form-page">
      <section className="preparation-heading"><div><h2>准备检查与确认</h2><p>检查当前考试的准备情况；需要处理的项目可以直接跳转到对应任务。</p></div><Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>重新检查</Button></section>
      <ExamScoringPreparation key={exam.id} exam={exam} canManage={canManageAssessment} onNavigate={onNavigate} onChanged={() => { void load(); onExamChanged?.(); }} />
      {failedChecks.length ? <ul className="exam-setup-inline-issues exam-readiness-issues">{failedChecks.map((check) => <li key={check.code}>
        <div><strong>{getSafeUserText(check.label, "需要完善")}</strong><span>{getSafeUserText(check.message, "请完成相关设置")}</span></div>
        <Button type="link" onClick={() => onNavigate(readinessCheckRoute(examId, check.section))}>去处理 <ArrowRight size={15} /></Button>
      </li>)}</ul> : null}

      {display.freezeAction}
      {display.reviewFailure}
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

      {display.unconfirmedNotice}
      {display.confirmedNotice}
      {display.collectingNotice}
      <Modal title="核对名单与评分方式后冻结" width={820} open={Boolean(freezeReview)} confirmLoading={working} okButtonProps={{ disabled: hasUnverifiedScoring }} maskClosable={!working} closable={!working} okText="确认冻结并完成准备" cancelText="返回修改" onCancel={closeFreezeReview} onOk={() => void confirm()}>
        {freezeReview ? <>
          <Alert type="warning" showIcon message={`将冻结 ${freezeReview.roster.summary.expected} 名学生的考试准备`} description="冻结后不能直接修改学生范围、题目、答案、评分细则和答题区域。若有漏人或内容错误，请返回相应准备页面处理后重新核对。服务端确认时仍会再次检查最新准备情况。" />
          {hasUnverifiedScoring ? <Alert type="error" showIcon message="逐题评分方式尚未核实，暂不能冻结" description={unverifiedScoringMessage} action={<Button disabled={working} onClick={() => { closeFreezeReview(); onNavigate(readinessCheckRoute(examId, "questions")); }}>前往题目配置</Button>} /> : null}
          {freezeError ? <Alert type="error" showIcon message="冻结未完成，核对内容已保留" description={freezeError} /> : null}
          <h3>本次同步的学生名单</h3>
          <Table<RosterEntry> rowKey="key" size="small" pagination={{ pageSize: 10 }} scroll={{ y: 260, x: 460 }} dataSource={freezeReview.roster.entries.filter((entry) => Boolean(entry.student_id))} columns={[
            { title: "姓名", dataIndex: "student_name", render: (value?: string) => value || "身份信息未提供" },
            { title: "学号", dataIndex: "student_no" }, { title: "班级", dataIndex: "class_name" }
          ]} />
          <h3>逐题评分方式</h3>
          <p>只有每道题的评分配置已保存且策略可识别时，才能确认冻结；AI 建议仍需教师确认。未保存或无法识别的策略请先回题目配置处理。</p>
          <Table rowKey="questionId" size="small" pagination={{ pageSize: 8 }} dataSource={freezeReview.scoring} columns={[
            { title: "题号", dataIndex: "questionNo", width: 100 }, { title: "评分方式", dataIndex: "mode", render: (mode: string) => hasVerifiedScoringMode(mode) ? preparationScoringLabels[mode] : "未配置可核实策略，请返回题目页核对" }
          ]} />
        </> : null}
      </Modal>
    </div>
  );
}
