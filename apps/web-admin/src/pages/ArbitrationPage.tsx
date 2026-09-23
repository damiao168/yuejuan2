import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Descriptions, Empty, Input, InputNumber, Select, Space, Tabs, type TableColumnsType } from "antd";
import { CheckCircle2, ClipboardCheck, Gavel, RefreshCw, ScrollText, Search, UserCheck } from "lucide-react";
import { ApiClientError, getSafeUserText, getUserErrorMessage } from "../api/client";
import { listAuditLogs, type AuditLog } from "../api/audit";
import { listExams } from "../api/exams";
import { listQuestions, type Question } from "../api/papers";
import {
  assignArbitrationTask,
  getArbitrationTask,
  listArbitrationTasks,
  submitArbitration,
  type ArbitrationTask,
  type FinalGrade
} from "../api/review";
import type { SessionUser } from "../auth/session";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { hashQueryParam } from "../router/query";
import { GoldCoverageGaps } from "../features/gold-papers";
import { AnswerGroupingDrawer } from "../features/answer-groups";
import { ArbitrationAiTab, ArbitrationAuditList, ArbitrationContextTab, ArbitrationRubricTab, ArbitrationScoreComparison } from "./ArbitrationPanels";
import { activeStatusMatched, formatScore, formatTime, statusLabels, taskTone } from "./arbitrationPresentation.model";

type ScopeFilter = "mine" | "all";
type StatusFilter = "active" | "pending" | "assigned" | "submitted";

interface ArbitrationDetailState {
  task: ArbitrationTask;
  question?: Question;
  warnings: string[];
}

interface DecisionDraft {
  finalScore: number | null;
  reason: string;
  studentFeedback: string;
}

const statusOptions: { label: string; value: StatusFilter }[] = [
  { label: "未完成（待分配+已分配）", value: "active" },
  { label: "待分配", value: "pending" },
  { label: "已分配", value: "assigned" },
  { label: "已提交", value: "submitted" }
];

const scopeOptions: { label: string; value: ScopeFilter }[] = [
  { label: "我的任务", value: "mine" },
  { label: "全部任务", value: "all" }
];

const finalGradeSourceLabels: Record<string, string> = {
  ai: "AI 评分",
  human: "人工评分",
  arbitration: "仲裁定分",
  appeal: "申诉改分"
};

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

async function loadQuestion(task: ArbitrationTask) {
  const result = await listQuestions(task.exam_id);
  return result.questions.find((item) => item.id === task.question_id);
}

export function ArbitrationPage({ canAssign, canWork, canReadAudit, canReadExams, currentUser, initialExamId = "", personalScope = false }: { canAssign: boolean; canWork: boolean; canReadAudit: boolean; canReadExams: boolean; currentUser: SessionUser; initialExamId?: string; personalScope?: boolean }) {
  const { message } = App.useApp();
  const actorId = currentUser.id;
  const [scope, setScope] = useState<ScopeFilter>("mine");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>(() => {
    const status = hashQueryParam("status");
    return statusOptions.some((option) => option.value === status) ? status as StatusFilter : "active";
  });
  const [keyword, setKeyword] = useState("");
  const [tasks, setTasks] = useState<ArbitrationTask[]>([]);
  const [examNames, setExamNames] = useState<Record<string, string>>({});
  const [selectedTaskId, setSelectedTaskId] = useState("");
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
  const [finalGrade, setFinalGrade] = useState<FinalGrade | null>(null);
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([]);
  const [auditLoading, setAuditLoading] = useState(false);
  const [auditError, setAuditError] = useState<string | null>(null);
  const [actioning, setActioning] = useState<string | null>(null);
  const [answerGroupingOpen, setAnswerGroupingOpen] = useState(false);
  const taskRequestRef = useRef(0);
  const detailRequestRef = useRef(0);
  const auditRequestRef = useRef(0);
  const examNamesRequestRef = useRef(0);
  const canSubmit = canWork && (!personalScope || detail?.task.assigned_to === currentUser.id);

  const filteredTasks = useMemo(() => {
    const text = keyword.trim().toLowerCase();
    return tasks.filter((task) => {
      const statusMatched = statusFilter === "active" ? activeStatusMatched(task) : task.status === statusFilter;
      const examLabel = examNames[task.exam_id] ?? task.exam_id;
      const keywordMatched =
        !text ||
        task.id.toLowerCase().includes(text) ||
        task.anonymous_code.toLowerCase().includes(text) ||
        task.question_no.toLowerCase().includes(text) ||
        task.exam_id.toLowerCase().includes(text) ||
        examLabel.toLowerCase().includes(text);
      return (!initialExamId || task.exam_id === initialExamId) && statusMatched && keywordMatched;
    });
  }, [examNames, initialExamId, keyword, statusFilter, tasks]);

  const selectedTask = useMemo(() => tasks.find((task) => task.id === selectedTaskId), [selectedTaskId, tasks]);
  const maxScore = detail?.question?.score ?? detail?.question?.rubric?.max_score ?? 0;
  const rubricPoints = detail?.question?.rubric?.points ?? [];
  const aiSuggestion = detail?.task.context?.ai_suggestion;

  const loadAudits = useCallback(
    async (task: ArbitrationTask, grade?: FinalGrade) => {
      const requestId = ++auditRequestRef.current;
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
        if (requestId !== auditRequestRef.current) return;
        setAuditLogs(merged);
      } catch (error) {
        if (requestId !== auditRequestRef.current) return;
        setAuditError(formatError(error));
        setAuditLogs([]);
      } finally {
        if (requestId === auditRequestRef.current) setAuditLoading(false);
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
        assigned_to: personalScope ? currentUser.id : scope === "mine" ? actorId : undefined,
        exam_id: initialExamId || undefined,
        limit: 50
      });
      if (requestId !== taskRequestRef.current) return;
      const scopedTasks = initialExamId
        ? result.arbitration_tasks.filter((task) => task.exam_id === initialExamId)
        : result.arbitration_tasks;
      const requestedId = requestedTaskRef.current;
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
      setSelectedTaskId((current) => requestedId || (scopedTasks.some((task) => task.id === current) ? current : scopedTasks[0]?.id ?? ""));
      void loadExamNames(scopedTasks);
    } catch (error) {
      if (requestId !== taskRequestRef.current) return;
      setTaskError(formatError(error));
      setTasks([]);
      setNextTaskCursor("");
      setHasMoreTasks(false);
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
        assigned_to: personalScope ? currentUser.id : scope === "mine" ? actorId : undefined,
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
      if (!taskId) {
        setDetail(null);
        setFinalGrade(null);
        setAuditLogs([]);
        setDetailLoading(false);
        return;
      }
      setDetailLoading(true);
      setDetailError(null);
      setFinalGrade(null);
      try {
        const result = await getArbitrationTask(taskId);
        if (requestId !== detailRequestRef.current) return;
        const warnings: string[] = [];
        let question: Question | undefined;
        try {
          question = canReadExams ? await loadQuestion(result.arbitration_task) : undefined;
          if (requestId !== detailRequestRef.current) return;
          if (!question) {
            warnings.push("未找到该题的题目信息与评分标准，请联系管理员核对试卷设置。");
          }
        } catch (error) {
          warnings.push(formatError(error));
        }
        if (!result.arbitration_task.context?.raw_answer) {
          warnings.push("暂无该答卷的原始作答内容。");
        }
        if (!result.arbitration_task.context?.ocr_text) {
          warnings.push("暂无该答卷的识别文本。");
        }
        if (requestId !== detailRequestRef.current) return;
        setDetail({ task: result.arbitration_task, question, warnings });
        setDraft(createInitialDraft(result.arbitration_task));
        await loadAudits(result.arbitration_task);
      } catch (error) {
        if (requestId !== detailRequestRef.current) return;
        setDetailError(formatError(error));
        setDetail(null);
      } finally {
        if (requestId === detailRequestRef.current) setDetailLoading(false);
      }
    },
    [canReadExams, loadAudits]
  );

  useEffect(() => {
    void loadTasks();
  }, [loadTasks]);

  useEffect(() => {
    void loadDetail(selectedTaskId);
  }, [loadDetail, selectedTaskId]);

  const refreshCurrent = async () => {
    await loadTasks();
    if (selectedTaskId) {
      await loadDetail(selectedTaskId);
    }
  };

  const assignToMe = async () => {
    if (!canAssign) {
      message.error("当前账号不能分配仲裁任务");
      return;
    }
    if (!detail?.task) {
      message.error("请先选择仲裁任务");
      return;
    }
    if (!actorId) {
      message.error("无法识别当前登录用户，请刷新页面后重试");
      return;
    }
    setActioning("assign");
    try {
      const result = await assignArbitrationTask(detail.task.id, {
        assigned_to: actorId,
        expected_revision: detail.task.revision
      });
      setDetail((current) => (current ? { ...current, task: result.arbitration_task } : current));
      setDraft(createInitialDraft(result.arbitration_task));
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
    if (!detail?.task) {
      message.error("请先选择仲裁任务");
      return;
    }
    const score = Number(draft.finalScore);
    if (!Number.isFinite(score)) {
      message.error("请输入仲裁最终分");
      return;
    }
    if (!maxScore || score < 0 || score > maxScore) {
      message.error("仲裁最终分必须在 0 到题目满分之间");
      return;
    }
    if (!draft.reason.trim()) {
      message.error("请填写仲裁说明");
      return;
    }
    setActioning("submit");
    try {
        const result = await submitArbitration(detail.task.id, {
          final_score: score,
          reason: draft.reason.trim(),
          student_feedback: draft.studentFeedback.trim(),
          expected_revision: detail.task.revision
        });
      setDetail((current) => (current ? { ...current, task: result.arbitration_task } : current));
      setDraft(createInitialDraft(result.arbitration_task));
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

  const columns: TableColumnsType<ArbitrationTask> = [
    {
      title: "考试",
      dataIndex: "exam_id",
      width: 220,
      render: (value: string) => (
        <span className="arbitration-exam-cell" title={value}>
          {examNames[value] ?? (canReadExams ? "未匹配到考试" : "权限受限")}
        </span>
      )
    },
    { title: "题号", dataIndex: "question_no", width: 88 },
    { title: "匿名码", dataIndex: "anonymous_code", width: 150 },
    { title: "A 分", dataIndex: "first_score", width: 86, render: (value: number) => formatScore(value) },
    { title: "B 分", dataIndex: "second_score", width: 86, render: (value: number) => formatScore(value) },
    {
      title: "分差",
      dataIndex: "score_difference",
      width: 92,
      render: (value: number) => <StatusTag tone={value > 0 ? "warning" : "neutral"}>{formatScore(value)}</StatusTag>
    },
    {
      title: "状态",
      dataIndex: "status",
      width: 104,
      render: (value: string) => <StatusTag tone={taskTone(value)}>{statusLabels[value] ?? "未知状态"}</StatusTag>
    }
  ];

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
          <Button icon={<RefreshCw size={16} />} onClick={() => void refreshCurrent()} loading={loadingTasks || detailLoading}>
            刷新
          </Button>
        </Space>
      </section>

      {!personalScope ? <GoldCoverageGaps examId={initialExamId} /> : null}

      <section className="arbitration-filterbar">
        {personalScope ? <span className="scope-fixed-label">仅显示分配给我的任务</span> : <Select className="toolbar-select" value={scope} options={scopeOptions} onChange={setScope} />}
        <Select className="toolbar-select" value={statusFilter} options={statusOptions} onChange={setStatusFilter} />
        <Input prefix={<Search size={16} />} placeholder="搜索考试、匿名码或题号" value={keyword} onChange={(event) => setKeyword(event.target.value)} />
        <span className="muted arbitration-filter-count">{filteredTasks.length} / {tasks.length} 个仲裁任务</span>
      </section>

      <section className="arbitration-queue-panel">
        {loadingTasks ? (
          <LoadingState label="正在读取仲裁任务" />
        ) : taskError ? (
          <ErrorState message={taskError} onRetry={() => void loadTasks()} />
        ) : (
          <>
            <ResponsiveTable
              className="dense-data-table"
              rowKey="id"
              size="small"
              columns={columns}
              dataSource={filteredTasks}
              pagination={{ pageSize: 6, showSizeChanger: false }}
              rowClassName={(record) => (record.id === selectedTaskId ? "arbitration-row-active" : "")}
              locale={{ emptyText: <Empty description="当前没有需要仲裁的评分差异" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
              onRow={(record) => ({
                onClick: () => setSelectedTaskId(record.id)
              })}
            />
            {hasMoreTasks ? (
              <Button block loading={loadingMoreTasks} onClick={() => void loadMoreTasks()}>
                加载更多仲裁任务
              </Button>
            ) : null}
          </>
        )}
      </section>

      <section className="arbitration-workspace">
        {detailLoading ? (
          <main className="arbitration-main-empty">
            <LoadingState label="正在读取仲裁详情" />
          </main>
        ) : detailError ? (
          <main className="arbitration-main-empty">
            <ErrorState message={detailError} onRetry={() => void loadDetail(selectedTaskId)} />
          </main>
        ) : !detail ? (
          <main className="arbitration-main-empty">
            <EmptyState title="请选择仲裁任务" description="选择上方任务后展示双评分差和裁决表单。" />
          </main>
        ) : (
          <main className="arbitration-main">
            {detail.warnings.length > 0 ? <Alert type="warning" showIcon message="部分评卷材料缺失" description={detail.warnings.map((warning) => getSafeUserText(warning, "评卷材料暂时不可用")).join("；")} /> : null}

            <section className="arbitration-context-row">
              <Descriptions bordered size="small" column={4}>
                <Descriptions.Item label="考试"><span title={detail.task.exam_id}>{examNames[detail.task.exam_id] ?? (canReadExams ? "未匹配到考试" : "权限受限")}</span></Descriptions.Item>
                <Descriptions.Item label="题号">{detail.task.question_no}</Descriptions.Item>
                <Descriptions.Item label="匿名码">{detail.task.anonymous_code}</Descriptions.Item>
                <Descriptions.Item label="状态"><span title={detail.task.status}>{statusLabels[detail.task.status] ?? "未知状态"}</span></Descriptions.Item>
                <Descriptions.Item label="分配给">{detail.task.assigned_to ? <span title={detail.task.assigned_to}>{detail.task.assigned_to === currentUser.id ? "我" : "已分配"}</span> : "未分配"}</Descriptions.Item>
                <Descriptions.Item label="创建时间">{formatTime(detail.task.created_at)}</Descriptions.Item>
                <Descriptions.Item label="更新时间">{formatTime(detail.task.updated_at)}</Descriptions.Item>
              </Descriptions>
            </section>

            <section className="arbitration-evidence-panel">
              <div className="panel-head">
                <div>
                  <h2>证据与评分标准</h2>
                  <p>满分 {maxScore || "未知"}，当前分差 {formatScore(detail.task.score_difference)}</p>
                </div>
              </div>
              <Tabs
                size="small"
                items={[
                  { key: "context", label: "作答与识别文本", children: <ArbitrationContextTab task={detail.task} /> },
                  { key: "ai", label: "AI 建议", children: <ArbitrationAiTab suggestion={aiSuggestion} /> },
                  { key: "rubric", label: "评分标准", children: <ArbitrationRubricTab question={detail.question} rubricPoints={rubricPoints} /> }
                ]}
              />
            </section>

            <section className="arbitration-reviewer-panel">
              <div className="panel-head">
                <div>
                  <h2>双评分差</h2>
                  <p>{detail.task.difference_reason || "暂无分差说明"}</p>
                </div>
                <Gavel size={18} />
              </div>
              <ArbitrationScoreComparison task={detail.task} />
            </section>
          </main>
        )}

        <aside className="arbitration-decision-panel">
          <div className="panel-head">
            <div>
              <h2>仲裁裁决</h2>
              <p>{selectedTask ? `${selectedTask.anonymous_code} · ${selectedTask.question_no}` : "未选择任务"}</p>
            </div>
            <ClipboardCheck size={18} />
          </div>

          <div className="score-input-row">
            <InputNumber
              min={0}
              max={maxScore || undefined}
              precision={1}
              value={draft.finalScore}
              placeholder="最终分"
              disabled={!detail || detail.task.status === "submitted"}
              onChange={(value) => setDraft((current) => ({ ...current, finalScore: value === null ? null : Number(value) }))}
            />
            <span>/ {maxScore || "-"}</span>
          </div>

          <Input.TextArea
            rows={4}
            placeholder="仲裁说明"
            value={draft.reason}
            disabled={!detail || detail.task.status === "submitted"}
            onChange={(event) => setDraft((current) => ({ ...current, reason: event.target.value }))}
          />
          <Input.TextArea
            rows={3}
            placeholder="学生可见反馈"
            value={draft.studentFeedback}
            disabled={!detail || detail.task.status === "submitted"}
            onChange={(event) => setDraft((current) => ({ ...current, studentFeedback: event.target.value }))}
          />

          <Space wrap>
            {canAssign ? <Button icon={<UserCheck size={16} />} disabled={!detail || detail.task.status === "submitted"} loading={actioning === "assign"} onClick={() => void assignToMe()}>
              分配给我
            </Button> : null}
            <Button type="primary" icon={<CheckCircle2 size={16} />} disabled={!canSubmit || !detail || detail.task.status === "submitted"} loading={actioning === "submit"} onClick={() => void submitDecision()}>
              提交仲裁
            </Button>
          </Space>

          {finalGrade ? (
            <div className="final-grade-result">
              <StatusTag tone="success">最终分已写入</StatusTag>
              <strong>
                {formatScore(finalGrade.score)} / {formatScore(finalGrade.max_score)}
              </strong>
              <span>{finalGradeSourceLabels[finalGrade.source] ?? "仲裁定分"} · {finalGrade.locked ? "已锁定" : "未锁定"}</span>
            </div>
          ) : detail?.task.final_score != null ? (
            <div className="final-grade-result">
              <StatusTag tone="success">已提交</StatusTag>
              <strong>{formatScore(detail.task.final_score)}</strong>
              <span>{detail.task.reason || "未填写仲裁说明"}</span>
            </div>
          ) : null}

          <section className="arbitration-audit-panel">
            <div className="panel-head compact">
              <div>
                <h2>操作记录</h2>
                <p>操作全程留痕</p>
              </div>
              <ScrollText size={18} />
            </div>
            <ArbitrationAuditList loading={auditLoading} error={auditError} logs={auditLogs} onRetry={() => detail?.task && void loadAudits(detail.task, finalGrade ?? undefined)} />
          </section>
        </aside>
      </section>
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
