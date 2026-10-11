import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App, Button, Space } from "antd";
import { RefreshCw } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { listAuditLogs, type AuditLog } from "../api/audit";
import { listExams } from "../api/exams";
import type { Question } from "../api/papers";
import {
  assignArbitrationTask,
  getArbitrationTask,
  listArbitrationTasks,
  submitArbitration,
  type ArbitrationTask,
  type FinalGrade
} from "../api/review";
import type { SessionUser } from "../auth/session";
import { hashQueryParam } from "../router/query";
import { useUnsavedChangesGuard } from "../router/useUnsavedChangesGuard";
import { GoldCoverageGaps } from "../features/gold-papers";
import { AnswerGroupingDrawer } from "../features/answer-groups";
import { ArbitrationTaskRail } from "./ArbitrationTaskRail";
import { ArbitrationWorkspaceMain } from "./ArbitrationWorkspaceMain";
import { ArbitrationDecisionPanel } from "./ArbitrationDecisionPanel";
import {
  arbitrationStatusOptions,
  arbitrationTaskAssignee,
  chooseArbitrationTaskId,
  filterArbitrationTasks,
  type ArbitrationScopeFilter as ScopeFilter,
  type ArbitrationStatusFilter as StatusFilter,
  type ArbitrationDetailState,
  type ArbitrationDecisionDraft as DecisionDraft
} from "./arbitrationQueue.model";

export function isCurrentArbitrationDetail(detailTaskId: string | undefined, selectedTaskId: string, detailLoading: boolean): boolean {
  return !detailLoading && Boolean(detailTaskId) && detailTaskId === selectedTaskId;
}

export function parseArbitrationScore(value: number | null, maxScore: number): number | null {
  if (value === null) return null;
  const score = Number(value);
  return Number.isFinite(score) && maxScore > 0 && score >= 0 && score <= maxScore ? score : null;
}

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.warn("仲裁页请求失败", error.status, error.code, error.message);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function createInitialDraft(task?: ArbitrationTask): DecisionDraft {
  return {
    finalScore: task?.final_score ?? null,
    reason: task?.reason ?? "",
    studentFeedback: task?.student_feedback ?? ""
  };
}

export function ArbitrationPage({ canAssign, canWork, canReadAudit, canReadExams, currentUser, initialExamId = "", personalScope = false }: Readonly<{ canAssign: boolean; canWork: boolean; canReadAudit: boolean; canReadExams: boolean; currentUser: SessionUser; initialExamId?: string; personalScope?: boolean }>) {
  const { message, modal } = App.useApp();
  const actorId = currentUser.id;
  const [scope, setScope] = useState<ScopeFilter>(hashQueryParam("exam") && canAssign ? "all" : "mine");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>(() => {
    const status = hashQueryParam("status");
    return arbitrationStatusOptions.some((option) => option.value === status) ? status as StatusFilter : "active";
  });
  const [keyword, setKeyword] = useState("");
  const [tasks, setTasks] = useState<ArbitrationTask[]>([]);
  const [examNames, setExamNames] = useState<Record<string, string>>({});
  const [selectedTaskId, setSelectedTaskId] = useState("");
  // 点击任务时先同步记录目标，提交与迟到响应不等待下一次渲染。
  const selectedTaskIdRef = useRef("");
  const requestedTaskRef = useRef(hashQueryParam("task"));
  const [loadingTasks, setLoadingTasks] = useState(true);
  const [loadingMoreTasks, setLoadingMoreTasks] = useState(false);
  const [nextTaskCursor, setNextTaskCursor] = useState("");
  const [hasMoreTasks, setHasMoreTasks] = useState(false);
  const [taskError, setTaskError] = useState<string | null>(null);
  const [detail, setDetail] = useState<ArbitrationDetailState | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [draft, setDraft] = useState<DecisionDraft>(() => createInitialDraft());
  const [draftTouched, setDraftTouched] = useState(false);
  const [finalGrade, setFinalGrade] = useState<FinalGrade | null>(null);
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([]);
  const [auditLoading, setAuditLoading] = useState(false);
  const [auditError, setAuditError] = useState<string | null>(null);
  const [actioning, setActioning] = useState<string | null>(null);
  const [answerGroupingOpen, setAnswerGroupingOpen] = useState(false);
  // 列表、详情、审计和考试名称独立淘汰旧响应，避免一个子请求阻止其他区域刷新。
  const taskRequestRef = useRef(0);
  const detailRequestRef = useRef(0);
  const auditRequestRef = useRef(0);
  const examNamesRequestRef = useRef(0);
  const canSubmit = canWork && (!personalScope || detail?.task.assigned_to === currentUser.id);

  const filteredTasks = useMemo(() => filterArbitrationTasks(tasks, examNames, initialExamId, statusFilter, keyword), [examNames, initialExamId, keyword, statusFilter, tasks]);

  const selectedTask = useMemo(() => tasks.find((task) => task.id === selectedTaskId), [selectedTaskId, tasks]);
  const maxScore = detail?.question?.score ?? detail?.question?.rubric?.max_score ?? 0;
  const materialsReady = maxScore > 0 && Boolean(detail?.question?.rubric) && detail?.question?.rubric?.status !== "missing";
  const editingDisabled = Boolean(actioning) || !isCurrentArbitrationDetail(detail?.task.id, selectedTaskId, detailLoading) || detail?.task.status === "submitted";

  const loadAudits = useCallback(
    async (task: ArbitrationTask, grade?: FinalGrade) => {
      // 任务已切换时，旧审计记录不能写回新任务的侧栏。
      if (selectedTaskIdRef.current !== task.id) return;
      const requestId = ++auditRequestRef.current;
      const isCurrentRequest = () => requestId === auditRequestRef.current && selectedTaskIdRef.current === task.id;
      if (!canReadAudit) {
        setAuditLogs([]);
        setAuditLoading(false);
        return;
      }
      setAuditLoading(true);
      setAuditError(null);
      try {
        const targets = [listAuditLogs({ target_type: "arbitration_task", target_id: task.id, limit: 20 })];
        if (grade?.id) {
          targets.push(listAuditLogs({ target_type: "final_grade", target_id: grade.id, limit: 20 }));
        }
        const results = await Promise.all(targets);
        const merged = results
          .flatMap((result) => result.audit_logs)
          .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime());
        if (!isCurrentRequest()) return;
        setAuditLogs(merged);
      } catch (error) {
        if (!isCurrentRequest()) return;
        setAuditError(formatError(error));
        setAuditLogs([]);
      } finally {
        if (isCurrentRequest()) setAuditLoading(false);
      }
    },
    [canReadAudit]
  );

  const loadExamNames = useCallback(async (nextTasks: ArbitrationTask[]) => {
    const requestId = ++examNamesRequestRef.current;
    if (!canReadExams) {
      setExamNames({});
      return;
    }
    const ids = Array.from(new Set(nextTasks.map((task) => task.exam_id).filter(Boolean)));
    if (ids.length === 0) {
      setExamNames({});
      return;
    }
    try {
      const result = await listExams({ limit: 200 });
      const wanted = new Set(ids);
      const next = Object.fromEntries(result.exams.filter((exam) => wanted.has(exam.id)).map((exam) => [exam.id, exam.name]));
      if (requestId === examNamesRequestRef.current) setExamNames(next);
    } catch {
      if (requestId === examNamesRequestRef.current) setExamNames({});
    }
  }, [canReadExams]);

  const loadTasks = useCallback(async () => {
    const requestId = ++taskRequestRef.current;
    setLoadingTasks(true);
    setTaskError(null);
    try {
      const result = await listArbitrationTasks({
        status: statusFilter,
        assigned_to: arbitrationTaskAssignee(personalScope, currentUser.id, scope, actorId),
        exam_id: initialExamId || undefined,
        limit: 50
      });
      if (requestId !== taskRequestRef.current) return;
      const scopedTasks = initialExamId
        ? result.arbitration_tasks.filter((task) => task.exam_id === initialExamId)
        : result.arbitration_tasks;
      const requestedId = requestedTaskRef.current;
      // 深链任务可能不在首个分页内；补取后仍须核对考试和个人分派范围。
      if (requestedId && !scopedTasks.some((task) => task.id === requestedId)) {
        const { arbitration_task: task } = await getArbitrationTask(requestedId);
        if (requestId !== taskRequestRef.current) return;
        if ((initialExamId && task.exam_id !== initialExamId) || ((personalScope || scope === "mine") && task.assigned_to !== actorId)) throw new Error("此任务不在当前考试或已转派，请返回我的工作查看最新任务");
        scopedTasks.unshift(task);
      }
      requestedTaskRef.current = "";
      setTasks(scopedTasks);
      setNextTaskCursor(result.next_cursor ?? "");
      setHasMoreTasks(Boolean(result.has_more));
      const nextSelectedId = chooseArbitrationTaskId(scopedTasks, requestedId, selectedTaskIdRef.current);
      selectedTaskIdRef.current = nextSelectedId;
      setSelectedTaskId(nextSelectedId);
      void loadExamNames(scopedTasks);
    } catch (error) {
      if (requestId !== taskRequestRef.current) return;
      setTaskError(formatError(error));
      setTasks([]);
      setNextTaskCursor("");
      setHasMoreTasks(false);
      selectedTaskIdRef.current = "";
      setSelectedTaskId("");
    } finally {
      if (requestId === taskRequestRef.current) setLoadingTasks(false);
    }
  }, [actorId, currentUser.id, initialExamId, loadExamNames, personalScope, scope, statusFilter]);

  const loadMoreTasks = useCallback(async () => {
    if (!hasMoreTasks || !nextTaskCursor || loadingMoreTasks) return;
    const requestId = ++taskRequestRef.current;
    setLoadingMoreTasks(true);
    try {
      const result = await listArbitrationTasks({
        status: statusFilter,
        assigned_to: arbitrationTaskAssignee(personalScope, currentUser.id, scope, actorId),
        exam_id: initialExamId || undefined,
        limit: 50,
        cursor: nextTaskCursor
      });
      if (requestId !== taskRequestRef.current) return;
      const byID = new Map(tasks.map((task) => [task.id, task]));
      result.arbitration_tasks.forEach((task) => byID.set(task.id, task));
      const mergedTasks = Array.from(byID.values());
      setTasks(mergedTasks);
      setNextTaskCursor(result.next_cursor ?? "");
      setHasMoreTasks(Boolean(result.has_more));
      void loadExamNames(mergedTasks);
    } catch (error) {
      if (requestId === taskRequestRef.current) message.error(formatError(error));
    } finally {
      if (requestId === taskRequestRef.current) setLoadingMoreTasks(false);
    }
  }, [actorId, currentUser.id, hasMoreTasks, initialExamId, loadExamNames, loadingMoreTasks, message, nextTaskCursor, personalScope, scope, statusFilter, tasks]);

  const loadDetail = useCallback(
    async (taskId: string) => {
      const requestId = ++detailRequestRef.current;
      const isCurrentRequest = () => requestId === detailRequestRef.current && selectedTaskIdRef.current === taskId;
      ++auditRequestRef.current;
      setAuditLoading(false);
      setAuditError(null);
      if (!taskId) {
        setDetail(null);
        setDraft(createInitialDraft());
        setDraftTouched(false);
        setFinalGrade(null);
        setAuditLogs([]);
        setDetailLoading(false);
        return;
      }
      // 任务切换期间不保留上一题的详情和草稿，提交入口也必须绑定当前任务。
      setDetail(null);
      setDraft(createInitialDraft());
      setDraftTouched(false);
      setDetailLoading(true);
      setDetailError(null);
      setFinalGrade(null);
      setAuditLogs([]);
      try {
        const result = await getArbitrationTask(taskId);
        if (!isCurrentRequest()) return;
        const warnings: string[] = [];
        const task = result.arbitration_task;
        // 评分依据随已获授权的任务返回；整场考试管理接口不能充当教师的标准读取入口。
        const question = task.context?.question?.id === task.question_id && task.context.question.exam_id === task.exam_id
          ? { ...task.context.question, rubric: task.context.rubric } as Question : undefined;
        if (!question?.rubric || question.rubric.status === "missing" || question.score <= 0) {
          warnings.push("本题冻结评分材料不完整，暂不能提交仲裁。请联系本场考试负责人补齐后重新读取任务。");
        }
        if (!result.arbitration_task.context?.raw_answer) {
          warnings.push("暂无该答卷的原始作答内容。");
        }
        if (!result.arbitration_task.context?.ocr_text) {
          warnings.push("暂无该答卷的识别文本。");
        }
        if (!isCurrentRequest()) return;
        setDetail({ task: result.arbitration_task, question, warnings });
        setDraft(createInitialDraft(result.arbitration_task));
        setDraftTouched(false);
        await loadAudits(result.arbitration_task);
      } catch (error) {
        if (!isCurrentRequest()) return;
        setDetailError(formatError(error));
        setDetail(null);
      } finally {
        if (isCurrentRequest()) setDetailLoading(false);
      }
    },
    [loadAudits]
  );

  useEffect(() => {
    void loadTasks();
  }, [loadTasks]);

  useEffect(() => {
    void loadDetail(selectedTaskId);
  }, [loadDetail, selectedTaskId]);

  useUnsavedChangesGuard({ dirty: draftTouched, busy: Boolean(actioning), description: "最终分、仲裁说明和学生反馈尚未提交，离开后无法恢复这些内容。" });

  const confirmDraftDiscard = (action: () => void, actionLabel: string) => {
    if (actioning) { message.warning("仲裁操作正在提交，请等待完成后再切换或刷新。"); return; }
    if (!draftTouched) { action(); return; }
    modal.confirm({
      title: "放弃当前未提交的仲裁内容？",
      content: "最终分、仲裁说明和学生反馈目前只在本页，离开当前任务后无法恢复。",
      okText: actionLabel,
      cancelText: "继续编辑",
      onOk: action
    });
  };

  const refreshCurrent = async () => {
    confirmDraftDiscard(() => { void refreshWithoutDraft(); }, "放弃并刷新");
  };

  const refreshWithoutDraft = async () => {
    await loadTasks();
    if (selectedTaskId) {
      await loadDetail(selectedTaskId);
    }
  };

  const assignToMe = async (discardDraft = false) => {
    if (actioning) return;
    if (draftTouched && !discardDraft) {
      confirmDraftDiscard(() => { void assignToMe(true); }, "放弃并分配");
      return;
    }
    if (!canAssign) {
      message.error("当前账号不能分配仲裁任务");
      return;
    }
    if (!detail?.task) {
      message.error("请先选择仲裁任务");
      return;
    }
    if (!isCurrentArbitrationDetail(detail.task.id, selectedTaskIdRef.current, detailLoading)) {
      message.error("仲裁详情正在切换，请等待当前任务加载完成");
      return;
    }
    if (!actorId) {
      message.error("无法识别当前登录用户，请刷新页面后重试");
      return;
    }
    const taskId = detail.task.id;
    const detailRequestId = detailRequestRef.current;
    setActioning("assign");
    try {
      const result = await assignArbitrationTask(taskId, {
        assigned_to: actorId,
        expected_revision: detail.task.revision
      });
      if (selectedTaskIdRef.current !== taskId || detailRequestRef.current !== detailRequestId) return;
      setDetail((current) => (current ? { ...current, task: result.arbitration_task } : current));
      setDraft(createInitialDraft(result.arbitration_task));
      setDraftTouched(false);
      await loadAudits(result.arbitration_task);
      await loadTasks();
      message.success("仲裁任务已分配");
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setActioning(null);
    }
  };

  const submitDecision = async () => {
    if (actioning) return;
    if (!detail?.task) {
      message.error(detailLoading ? "仲裁详情正在切换，请等待当前任务加载完成" : "请先选择仲裁任务");
      return;
    }
    if (!isCurrentArbitrationDetail(detail.task.id, selectedTaskIdRef.current, detailLoading)) {
      message.error("仲裁详情正在切换，请等待当前任务加载完成");
      return;
    }
    const taskId = detail.task.id;
    const expectedRevision = detail.task.revision;
    const detailRequestId = detailRequestRef.current;
    if (!materialsReady) {
      message.error("本题冻结评分材料不完整，请联系本场考试负责人补齐后重试");
      return;
    }
    if (draft.finalScore === null) {
      message.error("请输入仲裁最终分");
      return;
    }
    const score = parseArbitrationScore(draft.finalScore, maxScore);
    if (score === null) {
      message.error("仲裁最终分必须在 0 到题目满分之间");
      return;
    }
    if (!draft.reason.trim()) {
      message.error("请填写仲裁说明");
      return;
    }
    setActioning("submit");
    try {
      const result = await submitArbitration(taskId, {
        final_score: score,
        reason: draft.reason.trim(),
        student_feedback: draft.studentFeedback.trim(),
        expected_revision: expectedRevision
      });
      if (selectedTaskIdRef.current !== taskId || detailRequestRef.current !== detailRequestId) return;
      setDetail((current) => (current ? { ...current, task: result.arbitration_task } : current));
      setDraft(createInitialDraft(result.arbitration_task));
      setDraftTouched(false);
      setFinalGrade(result.final_grade);
      await loadTasks();
      await loadAudits(result.arbitration_task, result.final_grade);
      message.success("仲裁结果已提交");
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setActioning(null);
    }
  };

  const selectTask = (taskId: string) => {
    if (taskId === selectedTaskIdRef.current) return;
    const changeTask = () => {
      selectedTaskIdRef.current = taskId;
      setSelectedTaskId(taskId);
    };
    confirmDraftDiscard(changeTask, "放弃并切换");
  };

  return (
    <div className={tasks.length === 0 && !loadingTasks ? "arbitration-shell empty" : "arbitration-shell"}>
      <section className="arbitration-topbar">
        <div>
          <Space>
            <h1>{personalScope ? "我的仲裁任务" : "双评仲裁"}</h1>
          </Space>
          <p>对比两次评分及其依据，确认最终得分。</p>
        </div>
        <Space wrap>
          {!personalScope ? (
            <Button disabled={!selectedTask?.exam_id && !initialExamId} onClick={() => setAnswerGroupingOpen(true)}>
              相似答案分组
            </Button>
          ) : null}
          <Button disabled={Boolean(actioning)} icon={<RefreshCw size={16} />} onClick={() => void refreshCurrent()} loading={loadingTasks || detailLoading}>
            刷新
          </Button>
        </Space>
      </section>

      {!personalScope ? <GoldCoverageGaps examId={initialExamId} /> : null}

      <div className="arbitration-body">
        <ArbitrationTaskRail personalScope={personalScope} scope={scope} statusFilter={statusFilter} keyword={keyword}
          totalTasks={tasks.length} tasks={filteredTasks} selectedTaskId={selectedTaskId} examNames={examNames}
          canReadExams={canReadExams} actioning={Boolean(actioning)} loading={loadingTasks} error={taskError}
          hasMoreTasks={hasMoreTasks} loadingMoreTasks={loadingMoreTasks}
          onScopeChange={(value) => confirmDraftDiscard(() => setScope(value), "放弃并筛选")}
          onStatusChange={(value) => confirmDraftDiscard(() => setStatusFilter(value), "放弃并筛选")}
          onKeywordChange={setKeyword} onSelect={selectTask} onRetry={() => { void loadTasks(); }} onLoadMore={() => { void loadMoreTasks(); }} />
        <section className="arbitration-workspace">
          <ArbitrationWorkspaceMain detail={detail} loading={detailLoading} error={detailError} examNames={examNames}
            canReadExams={canReadExams} currentUserId={currentUser.id} maxScore={maxScore} onRetry={() => { void loadDetail(selectedTaskId); }} />
          <ArbitrationDecisionPanel selectedTask={selectedTask} detail={detail} draft={draft} draftTouched={draftTouched}
            finalGrade={finalGrade} maxScore={maxScore} actioning={actioning} editingDisabled={editingDisabled}
            canAssign={canAssign} canSubmit={canSubmit && materialsReady} auditLoading={auditLoading} auditError={auditError} auditLogs={auditLogs}
            setDraft={setDraft} setDraftTouched={setDraftTouched} onAssign={() => { void assignToMe(); }} onSubmit={() => { void submitDecision(); }}
            onAuditRetry={() => { if (detail?.task) void loadAudits(detail.task, finalGrade ?? undefined); }} />
        </section>
      </div>
      <AnswerGroupingDrawer
        open={answerGroupingOpen}
        examId={selectedTask?.exam_id || initialExamId}
        initialQuestionId={selectedTask?.question_id ?? ""}
        canManage={canAssign}
        onClose={() => setAnswerGroupingOpen(false)}
      />
    </div>
  );
}
