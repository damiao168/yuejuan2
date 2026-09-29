import { useCallback, useEffect, useMemo, useReducer, useState } from "react";
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
import { listManagedUsers, type ManagedUser } from "../../../api/users";
import { closedWorkflowModal, workflowModalReducer } from "./workflowModal";

type RunAction = (key: string, action: () => Promise<void>, successText: string) => Promise<void>;

function formatError(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function formatScore(value?: number | null) {
  if (value === undefined || value === null || !Number.isFinite(value)) return "-";
  return Number(value.toFixed(1)).toString();
}

export function useRegradeWorkflow({
  canManage,
  selectedExamId,
  publishedRelease,
  grades,
  runAction,
  setActioning
}: {
  canManage: boolean;
  selectedExamId: string;
  publishedRelease?: ScoreRelease;
  grades: SubmissionGrade[];
  runAction: RunAction;
  setActioning: (key: string | null) => void;
}) {
  const { message, modal } = App.useApp();
  const [regradeModal, dispatchRegradeModal] = useReducer(workflowModalReducer, closedWorkflowModal);
  const [regradeQuestionId, setRegradeQuestionId] = useState("");
  const [regradeReasonCode, setRegradeReasonCode] = useState("quality_incident");
  const [regradeReasonText, setRegradeReasonText] = useState("");
  const [regradeStrategy, setRegradeStrategy] = useState("human_recheck");
  const [regradeAssigneeID, setRegradeAssigneeID] = useState("");
  const [regradeGraders, setRegradeGraders] = useState<ManagedUser[]>([]);
  const [regradeGradersError, setRegradeGradersError] = useState("");
  const [loadingRegradeGraders, setLoadingRegradeGraders] = useState(false);
  const [regradePreview, setRegradePreview] = useState<RegradePreview | null>(null);
  const [regradeReview, setRegradeReview] = useState<RegradeSummary | null>(null);
  const [regradeReviewOpen, setRegradeReviewOpen] = useState(false);
  const [regradeReviewScores, setRegradeReviewScores] = useState<Record<string, number | null>>({});
  const [previewingRegrade, setPreviewingRegrade] = useState(false);

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
      const result = await listManagedUsers({ limit: 200 });
      const available = result.users.filter((user) => user.status === "active" && user.roles.includes("grader"));
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
  }, [canManage]);

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
    if (!regradePreview || regradePreview.question_id !== regradeQuestionId || regradePreview.source_release_id !== publishedRelease.id) {
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
    modal.confirm({
      title: "生成复评后的新成绩版本",
      content: "此操作只创建一个新的草稿版本，不会改写当前已发布成绩。新版本仍需通过发布门禁后才能对学生生效。",
      okText: "生成新版本",
      cancelText: "取消",
      onOk: () => runAction(
        "regrade-release",
        async () => { await createRegradeScoreRelease(job.id, { reason: `题目复评完成：${job.reason_text}`, idempotency_key: crypto.randomUUID() }); },
        "已生成复评后的成绩版本草稿"
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
    setActioning("regrade-load-review");
    try {
      const response = await getRegradeJob(job.id);
      setRegradeReview(response.regrade);
      setRegradeReviewScores(Object.fromEntries(response.regrade.items.map((item) => [item.id, item.candidate_score ?? null])));
      setRegradeReviewOpen(true);
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setActioning(null);
    }
  };

  const decideRegradeItem = (item: RegradeSummary["items"][number], decision: "accept" | "reject" | "exception") => {
    const score = regradeReviewScores[item.id];
    if (decision === "accept" && (score === null || score === undefined)) {
      message.error("接受候选前请填写最终得分");
      return;
    }
    void runAction(
      `regrade-review-${item.id}`,
      async () => {
        await reviewRegradeItem(item.id, {
          decision,
          ...(decision === "accept" && score !== item.candidate_score ? { reviewed_score: score } : {}),
          expected_revision: item.revision
        });
        if (regradeReview) {
          const refreshed = await getRegradeJob(regradeReview.job.id);
          setRegradeReview(refreshed.regrade);
          setRegradeReviewScores(Object.fromEntries(refreshed.regrade.items.map((value) => [value.id, value.candidate_score ?? null])));
        }
      },
      decision === "accept" ? "已接受重评候选" : decision === "reject" ? "已驳回重评候选" : "已标记重评异常"
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
    closeRegradeReview: () => setRegradeReviewOpen(false),
    regradeReviewScores,
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
