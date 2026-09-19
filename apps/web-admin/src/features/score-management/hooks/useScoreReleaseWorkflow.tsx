import { useReducer, useState } from "react";
import { App } from "antd";
import type { Exam } from "../../../api/exams";
import {
  createScoreRelease,
  publishScoreRelease,
  type ScoreRelease,
  type ScoreReleaseGate
} from "../../../api/scoreReleases";
import { closedWorkflowModal, workflowModalReducer } from "./workflowModal";

export const defaultReleaseVisibility = {
  show_question_scores: true,
  show_feedback: false,
  show_rubric_summary: false,
  show_cohort_statistics: true,
  show_percentile: true,
  show_exact_rank: true,
  show_question_statistics: true,
  show_answers: true
};

export const releaseVisibilityLabels: Record<keyof typeof defaultReleaseVisibility, string> = {
  show_question_scores: "逐题得分",
  show_feedback: "教师公开反馈",
  show_rubric_summary: "评分要点",
  show_cohort_statistics: "群体统计",
  show_percentile: "百分位",
  show_exact_rank: "具体排名",
  show_question_statistics: "题目统计",
  show_answers: "参考答案与本人答案"
};

type RunAction = (key: string, action: () => Promise<void>, successText: string) => Promise<void>;

export function useScoreReleaseWorkflow({
  selectedExamId,
  selectedExam,
  gradeTotal,
  releaseGate,
  runAction
}: {
  selectedExamId: string;
  selectedExam?: Exam;
  gradeTotal: number;
  releaseGate: ScoreReleaseGate | null;
  runAction: RunAction;
}) {
  const { message, modal } = App.useApp();
  const [releaseReason, setReleaseReason] = useState("");
  const [releaseHighScorePaper, setReleaseHighScorePaper] = useState(false);
  const [releaseVisibility, setReleaseVisibility] = useState(defaultReleaseVisibility);
  const [releaseModal, dispatchReleaseModal] = useReducer(workflowModalReducer, closedWorkflowModal);

  const createRelease = () => {
    if (!selectedExamId) {
      message.error("请先选择考试");
      return;
    }
    if (!releaseReason.trim()) {
      message.error("请填写本次发布版本的说明");
      return;
    }
    void runAction(
      "release-create",
      async () => {
        await createScoreRelease(selectedExamId, {
          reason: releaseReason.trim(),
          idempotency_key: crypto.randomUUID(),
          visibility_policy: { ...releaseVisibility, show_high_score_paper: releaseHighScorePaper },
          appeal_window: { enabled: selectedExam?.appeal_enabled ?? false }
        });
        dispatchReleaseModal({ type: "close" });
        setReleaseReason("");
        setReleaseHighScorePaper(false);
      },
      "已创建成绩发布草稿"
    );
  };

  const publishRelease = (release: ScoreRelease) => {
    if (!releaseGate?.passed) {
      message.error("发布门禁尚未通过，请先处理阻断项");
      return;
    }
    const visibleItems = (Object.keys(releaseVisibilityLabels) as Array<keyof typeof releaseVisibilityLabels>)
      .filter((key) => release.visibility_policy[key])
      .map((key) => releaseVisibilityLabels[key])
      .join("、") || "总分";
    modal.confirm({
      title: `发布成绩版本 V${release.version}`,
      content: (
        <div>
          <p>考试：{selectedExam?.name} · 共 {gradeTotal} 份成绩 · 第 {release.version} 版</p>
          <p>向学生公开：{visibleItems}{release.visibility_policy.show_high_score_paper ? "、最高分答卷" : ""}</p>
          <p>后续更正需创建新版本；本次公开内容以此版本的冻结设置为准。</p>
        </div>
      ),
      okText: "确认发布",
      cancelText: "取消",
      onOk: () => runAction("release-publish", async () => { await publishScoreRelease(release.id); }, "成绩版本已发布")
    });
  };

  const closeReleaseModal = () => {
    dispatchReleaseModal({ type: "close" });
    setReleaseReason("");
    setReleaseHighScorePaper(false);
  };

  return {
    releaseReason,
    setReleaseReason,
    releaseHighScorePaper,
    setReleaseHighScorePaper,
    releaseVisibility,
    setReleaseVisibility,
    releaseModalOpen: releaseModal.status === "open",
    openReleaseModal: () => dispatchReleaseModal({ type: "open" }),
    closeReleaseModal,
    createRelease,
    publishRelease
  };
}
