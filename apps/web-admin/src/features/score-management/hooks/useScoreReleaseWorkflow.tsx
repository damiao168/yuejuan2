import { useReducer, useState } from "react";
import { App } from "antd";
import type { Exam } from "../../../api/exams";
import {
  createScoreRelease,
  publishScoreRelease,
  revokeHighScorePaper,
  type ScoreRelease,
  type ScoreReleaseGate
} from "../../../api/scoreReleases";
import { closedWorkflowModal, workflowModalReducer } from "./workflowModal";
import { useStepUp } from "../../../auth/stepUpContext";

export const defaultReleaseVisibility = {
  show_question_scores: true,
  show_feedback: false,
  show_rubric_summary: false,
  show_cohort_statistics: true,
  show_percentile: true,
  show_exact_rank: true,
  show_question_statistics: true,
  show_answers: true,
  show_high_score_paper: false
};

export const releaseVisibilityLabels: Record<keyof typeof defaultReleaseVisibility, string> = {
  show_question_scores: "逐题得分",
  show_feedback: "教师公开反馈",
  show_rubric_summary: "评分要点",
  show_cohort_statistics: "群体统计",
  show_percentile: "百分位",
  show_exact_rank: "具体排名",
  show_question_statistics: "题目统计",
  show_answers: "参考答案与本人答案",
  show_high_score_paper: "匿名高分范例卷"
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
  const { runWithStepUp } = useStepUp();
  const [releaseReason, setReleaseReason] = useState("");
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
          visibility_policy: releaseVisibility,
          appeal_window: { enabled: selectedExam?.appeal_enabled ?? false }
        });
        dispatchReleaseModal({ type: "close" });
        setReleaseReason("");
      },
      "已创建成绩发布草稿"
    );
  };

  const publishRelease = (release: ScoreRelease) => {
    if (!releaseGate?.passed) {
      message.error("发布门禁尚未通过，请先处理阻断项");
      return;
    }
    // 正式发布展示草稿版本已冻结的策略，不能读取仍可编辑的新建发布表单。
    const visibleItems = (Object.keys(releaseVisibilityLabels) as Array<keyof typeof releaseVisibilityLabels>)
      .filter((key) => release.visibility_policy[key])
      .map((key) => releaseVisibilityLabels[key])
      .join("、") || "总分";
    modal.confirm({
      title: `发布成绩版本 V${release.version}`,
      content: (
        <div>
          <p>考试：{selectedExam?.name} · 共 {gradeTotal} 份成绩 · 第 {release.version} 版</p>
          <p>向学生公开：{visibleItems}</p>
          {release.visibility_policy.show_high_score_paper ? <p>发布前将生成匿名范例卷；如答卷缺少已确认的版面和身份区域，发布会被阻断。</p> : null}
          <p>后续更正需创建新版本；本次公开内容以此版本的冻结设置为准。</p>
        </div>
      ),
      okText: "确认发布",
      cancelText: "取消",
      onOk: () => runAction("release-publish", async () => {
        await runWithStepUp({ reason: `正式发布成绩版本 V${release.version}`, action: () => publishScoreRelease(release.id) });
      }, "成绩版本已发布")
    });
  };

  const revokeSharedPaper = (release: ScoreRelease) => {
    modal.confirm({
      title: `撤回 V${release.version} 的范例卷分享`,
      content: "撤回后学生将立即无法访问该版本的范例卷页面，成绩版本保持可查。",
      okText: "确认撤回",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: () => runAction("release-high-score-revoke", async () => {
        await runWithStepUp({ reason: `撤回成绩版本 V${release.version} 的范例卷分享`, action: () => revokeHighScorePaper(release.id) });
      }, "范例卷分享已撤回")
    });
  };

  const closeReleaseModal = () => {
    dispatchReleaseModal({ type: "close" });
    setReleaseReason("");
  };

  return {
    releaseReason,
    setReleaseReason,
    releaseVisibility,
    setReleaseVisibility,
    releaseModalOpen: releaseModal.status === "open",
    openReleaseModal: () => dispatchReleaseModal({ type: "open" }),
    closeReleaseModal,
    createRelease,
    publishRelease,
    revokeSharedPaper
  };
}
