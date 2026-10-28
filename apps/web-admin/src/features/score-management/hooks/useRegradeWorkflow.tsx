import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { App } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import {
  approveRegradeJob,
  createRegradeJob,
  createRegradeScoreRelease,
  finalizeRegradeJob,
  getRegradeJob,
  pauseRegradeJob,
  previewRegrade,
  resumeRegradeJob,
  reviewRegradeItem,
  startRegradeJob,
  type RegradeJob,
  type RegradePreview,
  type RegradeSummary,
  type ScoreRelease
} from "../../../api/scoreReleases";
import type { SubmissionGrade } from "../../../api/scores";
import { listReviewActors, type WorkflowActor } from "../../../api/workflowActors";
import { closedWorkflowModal, workflowModalReducer } from "./workflowModal";
import { getBusinessCommandSession, isBusinessCommandSessionCurrent } from "../../../api/businessCommand";

type RunAction = (key: string, action: () => Promise<void>, successText: string) => Promise<void>;

function formatError(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function formatScore(value?: number | null) {
  if (value === undefined || value === null || !Number.isFinite(value)) return "-";
  return Number(value.toFixed(1)).toString();
}

const regradeReviewSuccessText = {
  accept: "已接受重评候选",
  reject: "已驳回重评候选",
  exception: "已标记重评异常"
} as const;

export function useRegradeWorkflow({
  canManage,
  selectedExamId,
  publishedRelease,
  grades,
  runAction,
  setActioning,
  onReleaseCreated
}: {
  canManage: boolean;
  selectedExamId: string;
  publishedRelease?: ScoreRelease;
  grades: SubmissionGrade[];
  runAction: RunAction;
  setActioning: (key: string | null) => void;
  onReleaseCreated?: (release: ScoreRelease) => void;
}) {
  const { message, modal } = App.useApp();
  const [regradeModal, dispatchRegradeModal] = useReducer(workflowModalReducer, closedWorkflowModal);
  const [regradeQuestionId, setRegradeQuestionId] = useState("");
  const [regradeReasonCode, setRegradeReasonCode] = useState("quality_incident");
  const [regradeReasonText, setRegradeReasonText] = useState("");
  const [regradeStrategy, setRegradeStrategy] = useState("human_recheck");
  const [regradeAssigneeID, setRegradeAssigneeID] = useState("");
  const [regradeGraders, setRegradeGraders] = useState<WorkflowActor[]>([]);
  const [regradeGradersError, setRegradeGradersError] = useState("");
  const [loadingRegradeGraders, setLoadingRegradeGraders] = useState(false);
  const [regradePreview, setRegradePreview] = useState<RegradePreview | null>(null);
  const [regradeReview, setRegradeReview] = useState<RegradeSummary | null>(null);
  const [regradeReviewOpen, setRegradeReviewOpen] = useState(false);
  const [regradeReviewScores, setRegradeReviewScores] = useState<Record<string, number | null>>({});
  const [regradeReviewNotes, setRegradeReviewNotes] = useState<Record<string, string>>({});
  const [reviewedEvidence, setReviewedEvidence] = useState<Record<string, number>>({});
  const [regradeReviewConflicts, setRegradeReviewConflicts] = useState<Record<string, number>>({});
  const [previewingRegrade, setPreviewingRegrade] = useState(false);
  const reviewRequest = useRef(0);
  const reviewState = useRef({ summary: regradeReview, scores: regradeReviewScores, notes: regradeReviewNotes });
  reviewState.current = { summary: regradeReview, scores: regradeReviewScores, notes: regradeReviewNotes };
  const reviewExam = useRef(selectedExamId); reviewExam.current = selectedExamId;

  const regradeQuestionOptions = useMemo(() => {
    const byQuestion = new Map<string, { label: string; value: string }>();
    for (const grade of grades) {
      for (const item of grade.items ?? []) {
        if (!byQuestion.has(item.question_id)) {
          byQuestion.set(item.question_id, {
            value: item.question_id,
            label: `${item.question_no} · 满分 ${formatScore(item.max_score)}`
          });
        }
      }
    }
    return [...byQuestion.values()];
  }, [grades]);
  const regradeGraderOptions = useMemo(() => regradeGraders.map((user) => ({
    value: user.id,
    label: user.display_name || user.username
  })), [regradeGraders]);

  const loadRegradeGraders = useCallback(async () => {
    if (!canManage) return;
    setLoadingRegradeGraders(true);
    try {
      const available = await listReviewActors(selectedExamId);
      setRegradeGraders(available);
      setRegradeGradersError(available.length ? "" : "当前没有可分配的有效阅卷员账号");
      setRegradeAssigneeID((current) => available.some((user) => user.id === current) ? current : available[0]?.id || "");
    } catch (error) {
      setRegradeGraders([]);
      setRegradeAssigneeID("");
      setRegradeGradersError(formatError(error));
    } finally {
      setLoadingRegradeGraders(false);
    }
  }, [canManage, selectedExamId]);

  useEffect(() => {
    if (regradeModal.status === "open") void loadRegradeGraders();
  }, [loadRegradeGraders, regradeModal.status]);

  const previewSelectedRegrade = async () => {
    if (!selectedExamId || !regradeQuestionId || !publishedRelease) {
      message.error("请选择题目，且本场考试必须已有已发布的成绩版本");
      return;
    }
    setPreviewingRegrade(true);
    try {
      const response = await previewRegrade(selectedExamId, regradeQuestionId, {
        source_release_id: publishedRelease.id,
        selector: {}
      });
      setRegradePreview(response.preview);
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setPreviewingRegrade(false);
    }
  };

  const createSelectedRegrade = () => {
    if (!selectedExamId || !regradeQuestionId || !publishedRelease || !regradeReasonText.trim() || !regradeAssigneeID) {
      message.error("请完成题目、原因、阅卷员分派和影响预览");
      return;
    }
    // 预览只适用于同一题目和来源发布版本，切换目标后必须重新确认影响范围。
    if (regradePreview?.question_id !== regradeQuestionId || regradePreview.source_release_id !== publishedRelease.id) {
      message.error("请先预览本次复评的影响范围");
      return;
    }
    void runAction(
      "regrade-create",
      async () => {
        await createRegradeJob(selectedExamId, regradeQuestionId, {
          source_release_id: publishedRelease.id,
          reason_code: regradeReasonCode,
          reason_text: regradeReasonText.trim(),
          strategy: regradeStrategy,
          selector: {},
          assignee_id: regradeAssigneeID,
          idempotency_key: crypto.randomUUID()
        });
        dispatchRegradeModal({ type: "close" });
        setRegradePreview(null);
        setRegradeReasonText("");
      },
      "题目复评任务已创建"
    );
  };

  const materializeRegradeRelease = (job: RegradeJob) => {
    const session = getBusinessCommandSession();
    const examId = selectedExamId;
    modal.confirm({
      title: "继续本次复评的成绩版本",
      content: "尚无结果时创建草稿；已有结果时继续原版本。草稿须通过发布检查并正式发布后才会对学生生效。",
      okText: "生成或继续结果版本",
      cancelText: "取消",
      onOk: () => runAction(
        "regrade-release",
        async () => {
          const response = await createRegradeScoreRelease(job.id, { reason: `题目复评完成：${job.reason_text}`, idempotency_key: crypto.randomUUID() });
          // 服务端按复评任务复用草稿；回执只在当前会话和考试打开，不能把迟到结果带入另一场考试。
          if (isBusinessCommandSessionCurrent(session) && reviewExam.current === examId) onReleaseCreated?.(response.score_release);
        },
        "已定位本次复评的成绩版本"
      )
    });
  };

  const transitionRegrade = (job: RegradeJob, action: "approve" | "start" | "pause" | "resume" | "finalize") => {
    const actions = {
      approve: { request: approveRegradeJob, label: "已批准复评任务" },
      start: { request: startRegradeJob, label: "复评任务已启动，已分派的阅卷员可以领取" },
      pause: { request: pauseRegradeJob, label: "复评任务已暂停" },
      resume: { request: resumeRegradeJob, label: "复评任务已恢复" },
      finalize: { request: finalizeRegradeJob, label: "复评已完成，可生成新的成绩版本" }
    } as const;
    const current = actions[action];
    void runAction(`regrade-${action}`, async () => { await current.request(job.id); }, current.label);
  };

  const openRegradeReview = async (job: RegradeJob) => {
    const request = ++reviewRequest.current;
    const examId = selectedExamId;
    setActioning("regrade-load-review");
    try {
      const response = await getRegradeJob(job.id);
      if (request !== reviewRequest.current || examId !== reviewExam.current) return;
      setRegradeReview(response.regrade);
      setRegradeReviewConflicts({});
      setReviewedEvidence({});
      setRegradeReviewNotes({});
      setRegradeReviewScores(Object.fromEntries(response.regrade.items.map((item) => [item.id, item.candidate_score ?? null])));
      setRegradeReviewOpen(true);
    } catch (error) {
      if (request === reviewRequest.current && examId === reviewExam.current) message.error(formatError(error));
    } finally {
      if (request === reviewRequest.current && examId === reviewExam.current) setActioning(null);
    }
  };

  const decideRegradeItem = (item: RegradeSummary["items"][number], decision: "accept" | "reject" | "exception") => {
    if (regradeReviewConflicts[item.id]) { message.error("候选已更新，请核对新材料并选择保留或重置本页修改"); return; }
    if (reviewedEvidence[item.id] !== item.revision) { message.error("请先核对本条候选的答卷与冻结标准"); return; }
    const note = regradeReviewNotes[item.id]?.trim() ?? "";
    if (item.status === "exception" && (!note || decision === "exception")) {
      message.error("请填写异常处理说明，再接受或驳回候选"); return;
    }
    const score = regradeReviewScores[item.id];
    const request = reviewRequest.current;
    const examId = selectedExamId;
    if (decision === "accept" && (score === null || score === undefined)) {
      message.error("接受候选前请填写最终得分");
      return;
    }
    void runAction(
      `regrade-review-${item.id}`,
      async () => {
        await reviewRegradeItem(item.id, {
          decision,
          ...(note ? { note } : {}),
          ...(decision === "accept" && score !== item.candidate_score ? { reviewed_score: score } : {}),
          expected_revision: item.revision
        });
        if (regradeReview && request === reviewRequest.current && examId === reviewExam.current) {
          const refreshed = await getRegradeJob(regradeReview.job.id);
          if (request !== reviewRequest.current || examId !== reviewExam.current) return;
          const current = reviewState.current;
          const previousItems = new Map(current.summary?.items.map(value => [value.id, value]) ?? []);
          const conflicts: Record<string, number> = {};
          // 只接收本次提交项的结果；其他候选的本页改分和说明必须保留，0 分也是主动修改。
          const scores = Object.fromEntries(refreshed.regrade.items.map(value => {
            const previous = previousItems.get(value.id);
            const edited = value.id !== item.id && previous && (
              (Object.prototype.hasOwnProperty.call(current.scores, value.id) && current.scores[value.id] !== (previous.candidate_score ?? null)) || Boolean(current.notes[value.id])
            );
            if (edited && previous.revision !== value.revision) conflicts[value.id] = value.revision;
            return [value.id, edited && Object.prototype.hasOwnProperty.call(current.scores, value.id) ? current.scores[value.id] : value.candidate_score ?? null];
          }));
          setRegradeReview(refreshed.regrade);
          setRegradeReviewScores(scores);
          setRegradeReviewNotes(currentNotes => Object.fromEntries(refreshed.regrade.items.filter(value => value.id !== item.id && Object.prototype.hasOwnProperty.call(currentNotes, value.id)).map(value => [value.id, currentNotes[value.id]])));
          setRegradeReviewConflicts(previous => ({ ...previous, ...conflicts }));
          // 修订变化后必须重新读取证据；保留旧输入不代表旧核对仍可用于新候选。
          setReviewedEvidence(previous => Object.fromEntries(refreshed.regrade.items.filter(value => previous[value.id] === value.revision).map(value => [value.id, value.revision])));
        }
      },
      regradeReviewSuccessText[decision]
    );
  };

  return {
    regradeModalOpen: regradeModal.status === "open",
    openRegradeModal: () => dispatchRegradeModal({ type: "open" }),
    closeRegradeModal: () => dispatchRegradeModal({ type: "close" }),
    regradeQuestionId,
    setRegradeQuestionId,
    regradeReasonCode,
    setRegradeReasonCode,
    regradeReasonText,
    setRegradeReasonText,
    regradeStrategy,
    setRegradeStrategy,
    regradeAssigneeID,
    setRegradeAssigneeID,
    regradeGradersError,
    loadingRegradeGraders,
    regradePreview,
    clearRegradePreview: () => setRegradePreview(null),
    regradeReview,
    regradeReviewOpen,
    closeRegradeReview: () => { reviewRequest.current++; setRegradeReviewOpen(false); },
    regradeReviewScores,
    regradeReviewNotes,
    regradeReviewConflicts,
    resolveReviewConflict: (id: string, keepLocal: boolean) => {
      const candidate = regradeReview?.items.find(value => value.id === id);
      if (!candidate || reviewedEvidence[id] !== candidate.revision) return;
      if (!keepLocal) {
        setRegradeReviewScores(previous => ({ ...previous, [id]: candidate.candidate_score ?? null }));
        setRegradeReviewNotes(previous => ({ ...previous, [id]: "" }));
      }
      setRegradeReviewConflicts(previous => { const next = { ...previous }; delete next[id]; return next; });
    },
    setRegradeReviewNotes,
    reviewedEvidence,
    markEvidenceReviewed: (id: string, revision: number) => setReviewedEvidence(current => ({ ...current, [id]: revision })),
    setRegradeReviewScores,
    previewingRegrade,
    regradeQuestionOptions,
    regradeGraderOptions,
    previewSelectedRegrade,
    createSelectedRegrade,
    materializeRegradeRelease,
    transitionRegrade,
    openRegradeReview,
    decideRegradeItem
  };
}
