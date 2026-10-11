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
  show_question_stem: false,
  show_knowledge_analysis: false,
  show_score_distribution: false,
  show_answers: true,
  show_high_score_paper: false
};

export function visibleReleaseContent(policy: ScoreRelease["visibility_policy"]) {
  const questionContent = new Set<keyof typeof releaseVisibilityLabels>(["show_feedback", "show_rubric_summary", "show_question_statistics", "show_question_stem", "show_knowledge_analysis", "show_answers", "show_high_score_paper"]);
  // 逐题详情关闭时，后端不返回这些子内容；确认摘要也只列实际会生效的公开项。
  return (Object.keys(releaseVisibilityLabels) as Array<keyof typeof releaseVisibilityLabels>)
    .filter((key) => policy[key] && (policy.show_question_scores || !questionContent.has(key)))
    .map((key) => releaseVisibilityLabels[key]);
}

export const releaseVisibilityLabels: Record<keyof typeof defaultReleaseVisibility, string> = {
  show_question_scores: "逐题得分",
  show_feedback: "教师公开反馈",
  show_rubric_summary: "评分要点",
  show_cohort_statistics: "群体统计",
  show_percentile: "百分位",
  show_exact_rank: "具体排名",
  show_question_statistics: "题目统计",
  show_question_stem: "题目原文",
  show_knowledge_analysis: "知识点分析",
  show_score_distribution: "成绩分布",
  show_answers: "参考答案与本人答案",
  show_high_score_paper: "匿名高分范例卷"
};

type RunAction = (key: string, action: () => Promise<void>, successText: string) => Promise<void>;

export function useScoreReleaseWorkflow({
  selectedExamId,
  selectedExam,
  releaseGate,
  currentPublishedRelease,
  runAction
}: {
  selectedExamId: string;
  selectedExam?: Exam;
  gradeTotal: number;
  releaseGate: ScoreReleaseGate | null;
  currentPublishedRelease?: ScoreRelease;
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
    // 现有申请绑定单题发布事实；只公开总分时没有可操作的申请对象，不能开放空入口。
    if (selectedExam?.appeal_enabled && !releaseVisibility.show_question_scores) {
      message.error("允许学生申请成绩复核时，必须公开逐题得分。请开启逐题得分后创建草稿。");
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
    // 校验冻结草稿，旧草稿也不能绕过当前表单的依赖。
    if (release.appeal_window.enabled && !release.visibility_policy.show_question_scores) {
      message.error("此草稿开放复核但未公开逐题得分，学生无法申请。请重新创建公开逐题得分的草稿。");
      return;
    }
    if (!releaseGate?.passed) {
      message.error("发布门禁尚未通过，请先处理阻断项");
      return;
    }
    // 正式发布展示草稿版本已冻结的策略，不能读取仍可编辑的新建发布表单。
    const visibleItems = visibleReleaseContent(release.visibility_policy).join("、") || "总分";
    modal.confirm({
      title: `发布门户成绩版本 V${release.version}`,
      content: (
        <div>
          <p>考试：{selectedExam?.name} · 第 {release.version} 版 · 发布内容以此草稿冻结的成绩为准</p>
          <p>向学生公开：{visibleItems}</p>
          <p>成绩复核：{release.appeal_window.enabled ? "学生可从逐题详情申请复核；未限制原因时提供六种常用原因。" : "此版本未开放学生复核申请。"}</p>
          {currentPublishedRelease && release.version < currentPublishedRelease.version
            ? <p role="alert">门户当前为 V{currentPublishedRelease.version}；此次发布较早创建的 V{release.version}，学生看到的版本号会变小。请核对草稿内容。</p>
            : null}
          {release.visibility_policy.show_high_score_paper ? <p>发布前将生成匿名范例卷；如答卷缺少已确认的版面和身份区域，发布会被阻断。</p> : null}
          <p>请先从版本记录打开学生视角预览。发布后学生门户将展示此版本；学校原有成绩查询的状态不会因此改变。后续更正需创建新版本。</p>
        </div>
      ),
      okText: "发布到学生门户",
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
