import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Input, Select, Space } from "antd";
import { RefreshCw, Search } from "lucide-react";
import type { SessionUser } from "../auth/session";
import { canSubmitTeacherAppealRecommendation } from "../auth/capabilities";
import { getUserErrorMessage } from "../api/client";
import { listExams, type Exam } from "../api/exams";
import { listClasses, listStudents, type SchoolClass, type Student } from "../api/org";
import {
  assignAppeal,
  getAppeal,
  getAppealStatistics,
  listAppeals,
  reviewAppeal,
  submitAppealRecommendation,
  type Appeal,
  type AppealStatistics,
  type ReviewAppealPayload,
  type SubmitAppealRecommendationPayload,
} from "../api/appeals";
import { listManagedUsers, type ManagedUser } from "../api/users";
import { ErrorState } from "../components/PageState";
import { examSubjectLabel } from "../constants/examStatus";
import type { ProductExperience } from "../router/experience";
import { useUnsavedChangesGuard } from "../router/useUnsavedChangesGuard";
import { AppealTaskList } from "../features/appeals/AppealTaskList";
import { AppealCaseHeading, AppealCasePanel, AppealEvidence } from "../features/appeals/AppealEvidence";
import { AppealDecisionPanel } from "../features/appeals/AppealDecisionPanel";
import {
  evidenceNumber,
  formatScore,
  readableReason,
  statusLabels,
} from "../features/appeals/appealPresentation";

interface AppealCenterPageProps {
  mode: ProductExperience;
  canRead: boolean;
  canManage: boolean;
  canWork: boolean;
  canReadIdentities: boolean;
  canReadExams: boolean;
  currentUser: SessionUser;
  initialExamId?: string;
  onEditorStateChange?: (state: { dirty: boolean; busy: boolean }) => void;
}
interface IdentityMaps {
  students: Record<string, Student>;
  classes: Record<string, SchoolClass>;
  exams: Record<string, Exam>;
}

function formatError(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

export function AppealCenterPage({
  mode,
  canRead,
  canManage,
  canWork,
  canReadIdentities,
  canReadExams,
  currentUser,
  initialExamId = "",
  onEditorStateChange,
}: Readonly<AppealCenterPageProps>) {
  const { message, modal } = App.useApp();
  const [appeals, setAppeals] = useState<Appeal[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [selected, setSelected] = useState<Appeal | null>(null);
  const [statistics, setStatistics] = useState<AppealStatistics | null>(null);
  const [identities, setIdentities] = useState<IdentityMaps>({
    students: {},
    classes: {},
    exams: {},
  });
  const [workers, setWorkers] = useState<ManagedUser[]>([]);
  const [examFilter, setExamFilter] = useState(initialExamId || "all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [keyword, setKeyword] = useState("");
  const [assignedTo, setAssignedTo] = useState("");
  const [recommendation, setRecommendation] = useState<SubmitAppealRecommendationPayload["recommendation"]>("accept");
  const [decision, setDecision] = useState("accepted");
  const [score, setScore] = useState<number | null>(null);
  const [reason, setReason] = useState("");
  const [draftTouched, setDraftTouched] = useState(false);
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [actioning, setActioning] = useState("");
  useEffect(() => { onEditorStateChange?.({ dirty: draftTouched, busy: Boolean(actioning) }); }, [draftTouched, actioning, onEditorStateChange]);
  const [error, setError] = useState("");
  useUnsavedChangesGuard({
    dirty: draftTouched,
    busy: Boolean(actioning),
    description: "复核意见或终审结论尚未提交，离开后无法恢复这些内容。",
  });
  const detailRequestRef = useRef(0);
  // 点击列表时先同步记录目标，迟到请求和提交不能沿用上一条申诉。
  const selectedIdRef = useRef("");

  const load = useCallback(async () => {
    if (!canRead) {
      setLoading(false);
      return;
    }
    setLoading(true);
    setError("");
    try {
      const requestedExamId = examFilter === "all" ? undefined : examFilter;
      const response = await listAppeals({
        exam_id: requestedExamId,
        status: statusFilter === "all" ? undefined : statusFilter,
        limit: 100,
      });
      const studentIds = Array.from(new Set(response.appeals.map((item) => item.student_id).filter(Boolean)));
      // 身份与管理数据按能力单独请求，教师匿名复核不依赖学生姓名或管理员统计。
      const [studentResult, classResult, examResult, statsResult, workerResult] = await Promise.all([
        canReadIdentities && studentIds.length
          ? listStudents({ ids: studentIds, limit: 200 })
          : Promise.resolve({ students: [] }),
        canReadIdentities ? listClasses() : Promise.resolve({ classes: [] }),
        canReadExams ? listExams({ limit: 200 }) : Promise.resolve({ exams: [] }),
        canManage
          ? getAppealStatistics(requestedExamId)
          : Promise.resolve({ statistics: null }),
        canManage ? listManagedUsers({ limit: 200 }) : Promise.resolve({ users: [] }),
      ]);
      const nextSelectedId = response.appeals.some((item) => item.id === selectedIdRef.current)
        ? selectedIdRef.current
        : (response.appeals[0]?.id ?? "");
      selectedIdRef.current = nextSelectedId;
      setAppeals(response.appeals);
      setSelectedId(nextSelectedId);
      setIdentities({
        students: Object.fromEntries(studentResult.students.map((item) => [item.id, item])),
        classes: Object.fromEntries(classResult.classes.map((item) => [item.id, item])),
        exams: Object.fromEntries(examResult.exams.map((item) => [item.id, item])),
      });
      setStatistics(statsResult.statistics as AppealStatistics | null);
      setWorkers(
        workerResult.users.filter(
          (user) =>
            user.status === "active" && user.roles.some((role) => ["teacher", "grader", "arbitrator"].includes(role)),
        ),
      );
    } catch (error_) {
      setError(formatError(error_));
      setAppeals([]);
    } finally {
      setLoading(false);
    }
  }, [canManage, canRead, canReadExams, canReadIdentities, examFilter, statusFilter]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    const requestId = ++detailRequestRef.current;
    if (!selectedId) {
      setSelected(null);
      setDetailLoading(false);
      return;
    }

    // 切换申诉时先清空旧详情；请求完成后还要核对序号和当前 ID，避免慢响应覆盖新任务。
    setSelected(null);
    setDetailLoading(true);
    setAssignedTo("");
    setRecommendation("accept");
    setDecision("accepted");
    setScore(null);
    setReason("");
    setDraftTouched(false);
    void getAppeal(selectedId)
      .then(({ appeal }) => {
        if (requestId !== detailRequestRef.current || selectedIdRef.current !== selectedId) return;
        setSelected(appeal);
        setAssignedTo(appeal.assigned_to ?? "");
        setRecommendation(
          (appeal.teacher_recommendation as SubmitAppealRecommendationPayload["recommendation"]) || "accept",
        );
        setDecision(
          ["accepted", "rejected", "score_adjusted", "need_more_info"].includes(appeal.status)
            ? appeal.status
            : "accepted",
        );
        setScore(evidenceNumber(appeal, "score") ?? null);
      })
      .catch((error_) => {
        if (requestId === detailRequestRef.current && selectedIdRef.current === selectedId)
          setError(formatError(error_));
      })
      .finally(() => {
        if (requestId === detailRequestRef.current && selectedIdRef.current === selectedId) setDetailLoading(false);
      });
  }, [selectedId]);

  const confirmDraftDiscard = (action: () => void, actionLabel: string) => {
    if (actioning) return;
    if (!draftTouched) {
      action();
      return;
    }
    modal.confirm({
      title: "放弃未提交的复核内容？",
      content: "当前选择和填写内容仅在本页，切换申诉或刷新后无法恢复。",
      okText: actionLabel,
      cancelText: "继续编辑",
      onOk: action,
    });
  };

  const selectAppeal = (id: string) => {
    if (actioning || id === selectedIdRef.current) return;
    confirmDraftDiscard(() => {
      selectedIdRef.current = id;
      setSelectedId(id);
    }, "放弃并切换");
  };

  const selectedStudent = selected ? identities.students[selected.student_id] : undefined;
  const selectedClass = selectedStudent ? identities.classes[selectedStudent.class_id] : undefined;
  const currentScore = evidenceNumber(selected, "score");
  const maxScore = evidenceNumber(selected, "max_score");
  const terminal = Boolean(selected && ["accepted", "rejected", "score_adjusted", "closed"].includes(selected.status));
  const canSubmitTeacherRecommendation = canSubmitTeacherAppealRecommendation({
    experience: mode,
    canWork,
    terminal,
    assignedTo: selected?.assigned_to,
    currentUserId: currentUser.id,
  });
  const workerNames = useMemo(
    () => Object.fromEntries(workers.map((worker) => [worker.id, worker.display_name || worker.username])),
    [workers],
  );
  const workerOptions = useMemo(
    () =>
      workers.map((worker) => ({
        value: worker.id,
        label: worker.display_name || worker.username,
      })),
    [workers],
  );
  const examOptions = useMemo(
    () =>
      Object.values(identities.exams).map((exam) => ({
        value: exam.id,
        label: `${exam.name} · ${examSubjectLabel(exam.subject)}`,
      })),
    [identities.exams],
  );
  const filtered = useMemo(() => {
    const term = keyword.trim().toLowerCase();
    return term
      ? appeals.filter((appeal) =>
          [
            appeal.anonymous_code,
            appeal.exam_name,
            appeal.question_no,
            readableReason(appeal.reason),
            identities.students[appeal.student_id]?.name,
          ].some((value) => value?.toLowerCase().includes(term)),
        )
      : appeals;
  }, [appeals, identities.students, keyword]);
  const refreshSelected = async (id: string) => {
    await load();
    if (selectedIdRef.current !== id) return;
    const requestId = ++detailRequestRef.current;
    setDetailLoading(true);
    try {
      const response = await getAppeal(id);
      if (requestId !== detailRequestRef.current || selectedIdRef.current !== id) return;
      setSelected(response.appeal);
      setAssignedTo(response.appeal.assigned_to ?? "");
      setRecommendation(
        (response.appeal.teacher_recommendation as SubmitAppealRecommendationPayload["recommendation"]) || "accept",
      );
      setDecision(
        ["accepted", "rejected", "score_adjusted", "need_more_info"].includes(response.appeal.status)
          ? response.appeal.status
          : "accepted",
      );
      setScore(evidenceNumber(response.appeal, "score") ?? null);
      setReason("");
      setDraftTouched(false);
    } catch (error_) {
      if (requestId === detailRequestRef.current && selectedIdRef.current === id) message.error(formatError(error_));
    } finally {
      if (requestId === detailRequestRef.current && selectedIdRef.current === id) setDetailLoading(false);
    }
  };
  const reassign = async (discardOpinion = false) => {
    if (selected?.id !== selectedIdRef.current || detailLoading || actioning || !assignedTo) return;
    // 重新分派会重读详情并清空终审表单；填写的结论不能因为保存分派人而静默丢失。
    const initialDecision = ["accepted", "rejected", "score_adjusted", "need_more_info"].includes(selected.status)
      ? selected.status
      : "accepted";
    if (!discardOpinion && (reason || decision !== initialDecision || score !== (currentScore ?? null))) {
      modal.confirm({
        title: "重新分派并放弃未提交的结论？",
        content: "分派成功后会重新读取申请；当前填写的终审说明、结论和调整分数将被清空。",
        okText: "放弃结论并分派",
        cancelText: "继续编辑",
        onOk: () => reassign(true),
      });
      return;
    }
    const appealId = selected.id;
    setActioning("assign");
    try {
      await assignAppeal(appealId, assignedTo, selected.revision);
      if (selectedIdRef.current === appealId) setDraftTouched(false);
      message.success("复核任务已重新分派");
      await refreshSelected(appealId);
    } catch (error_) {
      message.error(`${formatError(error_)}；本页填写内容已保留`);
    } finally {
      setActioning("");
    }
  };
  const submitTeacherReview = async () => {
    if (actioning) return;
    if (
      selected?.id !== selectedIdRef.current ||
      detailLoading ||
      !canSubmitTeacherRecommendation ||
      reason.trim().length < 10
    ) {
      message.error("请确认任务已分派给当前账号，并填写不少于 10 个字的复核依据");
      return;
    }
    const appealId = selected.id;
    setActioning("recommend");
    try {
      await submitAppealRecommendation(appealId, {
        recommendation,
        reason: reason.trim(),
        ...(recommendation === "adjust_score" && score !== null ? { recommended_score: score } : {}),
        expected_revision: selected.revision,
      });
      if (selectedIdRef.current === appealId) setDraftTouched(false);
      message.success("独立复核意见已提交");
      await refreshSelected(appealId);
    } catch (error_) {
      message.error(`${formatError(error_)}；本页填写内容已保留`);
    } finally {
      setActioning("");
    }
  };
  const submitAdminDecision = () => {
    if (actioning) return;
    if (selected?.id !== selectedIdRef.current || detailLoading || reason.trim().length < 6) {
      message.error("请填写最终处理说明");
      return;
    }
    const appealId = selected.id;
    const payload: ReviewAppealPayload = {
      status: decision,
      reason: reason.trim(),
      assigned_to: selected.assigned_to,
      final_grade_id: selected.final_grade_id,
      expected_revision: selected.revision,
      ...(decision === "score_adjusted" && score !== null ? { adjusted_score: score } : {}),
    };
    modal.confirm({
      title: decision === "score_adjusted" ? "确认调整成绩" : "确认提交最终结论",
      content:
        decision === "score_adjusted"
          ? `本题将由 ${formatScore(currentScore)} 分调整为 ${formatScore(score)} 分。`
          : "提交后该结论将作为学生可见的正式处理结果。",
      okText: "确认提交",
      cancelText: "取消",
      onOk: async () => {
        if (selectedIdRef.current !== appealId) {
          message.error("申诉详情已切换，请重新确认当前任务");
          return;
        }
        setActioning("decision");
        try {
          await reviewAppeal(appealId, payload);
          if (selectedIdRef.current === appealId) setDraftTouched(false);
          message.success("最终处理已提交");
          await refreshSelected(appealId);
        } catch (error_) {
          message.error(`${formatError(error_)}；本页填写内容已保留`);
        } finally {
          setActioning("");
        }
      },
    });
  };

  return (
    <div className={`appeal-shell appeal-shell-v2 ${mode === "teacher" ? "teacher" : "admin"}`}>
      <section className="appeal-topbar">
        <div>
          <h1>{mode === "teacher" ? "复核任务" : "成绩复核"}</h1>
          <p>
            {mode === "teacher"
              ? "独立核对匿名答卷和评分标准，提交复核结论。"
              : "查看学生复核申请、复核进度与需要终审的争议。"}
          </p>
        </div>
        <Space wrap>
          <Select
            className="appeal-filter-select"
            value={examFilter}
            options={[{ value: "all", label: "全部考试" }, ...examOptions]}
            onChange={(value) => confirmDraftDiscard(() => setExamFilter(value), "放弃并筛选")}
          />
          <Select
            className="appeal-filter-select narrow"
            value={statusFilter}
            options={[
              { value: "all", label: "全部状态" },
              ...Object.entries(statusLabels).map(([value, label]) => ({
                value,
                label,
              })),
            ]}
            onChange={(value) => confirmDraftDiscard(() => setStatusFilter(value), "放弃并筛选")}
          />
          <Button
            icon={<RefreshCw size={16} />}
            loading={loading}
            onClick={() =>
              confirmDraftDiscard(() => {
                void load();
              }, "放弃并刷新")
            }
          >
            刷新
          </Button>
        </Space>
      </section>
      {!canRead ? <Alert type="error" showIcon message="当前账号无权查看复核任务" /> : null}
      {error ? <ErrorState message={error} onRetry={() => void load()} /> : null}
      {mode === "admin" ? (
        <section className="appeal-summary-strip compact">
          <div>
            <span>全部申请</span>
            <strong>{statistics?.total ?? appeals.length}</strong>
          </div>
          <div>
            <span>等待处理</span>
            <strong>
              {statistics
                ? (statistics.by_status.submitted ?? 0) + (statistics.by_status.under_review ?? 0)
                : appeals.filter((item) => ["submitted", "under_review"].includes(item.status)).length}
            </strong>
          </div>
          <div>
            <span>需要终审</span>
            <strong>
              {
                appeals.filter(
                  (item) => item.teacher_recommendation === "adjust_score" || item.status === "need_more_info",
                ).length
              }
            </strong>
          </div>
        </section>
      ) : null}
      <div className="appeal-body">
        <section className="appeal-queue-panel">
          <div className="appeal-queue-head">
            <div>
              <h2>{mode === "teacher" ? "分配给我的任务" : "复核申请"}</h2>
              <p>{filtered.length} 条已加载</p>
            </div>
            <Input
              prefix={<Search size={16} />}
              allowClear
              value={keyword}
              onChange={(event) => setKeyword(event.target.value)}
              placeholder={mode === "teacher" ? "搜索考试、题号或原因" : "搜索学生、考试、题号或原因"}
            />
          </div>
          <AppealTaskList
            loading={loading}
            appeals={filtered}
            selectedId={selectedId}
            mode={mode}
            exams={identities.exams}
            students={identities.students}
            onSelect={selectAppeal}
          />
        </section>
        <AppealCasePanel loading={detailLoading} hasSelection={selected !== null}>
        {selected ? (
          <>
            <AppealCaseHeading
              appeal={selected}
              mode={mode}
              examName={identities.exams[selected.exam_id]?.name}
              student={selectedStudent}
              schoolClass={selectedClass}
            />
            <div className="appeal-case-grid">
              <AppealEvidence appeal={selected} mode={mode} workerNames={workerNames} />
              <AppealDecisionPanel
                mode={mode} selected={selected} terminal={terminal} draftTouched={draftTouched}
                actioning={actioning} score={score} maxScore={maxScore} reason={reason}
                recommendation={recommendation} canSubmit={canSubmitTeacherRecommendation}
                workerNames={workerNames} workerOptions={workerOptions} assignedTo={assignedTo}
                decision={decision} canManage={canManage}
                onScoreChange={(value) => { setDraftTouched(true); setScore(value); }}
                onReasonChange={(value) => { setDraftTouched(true); setReason(value); }}
                onRecommendationChange={(value) => { setDraftTouched(true); setRecommendation(value); }}
                onAssignedToChange={(value) => { setDraftTouched(true); setAssignedTo(value); }}
                onDecisionChange={(value) => { setDraftTouched(true); setDecision(value); }}
                onSubmit={() => { void submitTeacherReview(); }}
                onReassign={() => { void reassign(); }}
                onAdminSubmit={submitAdminDecision}
              />
            </div>
          </>
        ) : null}
        </AppealCasePanel>
      </div>
    </div>
  );
}
export default AppealCenterPage;
