import { useEffect, useReducer, useRef, useState } from "react";
import { App } from "antd";
import type { Exam } from "../../../api/exams";
import {
  createRegradeScoreRelease,
  createScoreRelease,
  getScoreReleaseDetail,
  publishScoreRelease,
  revokeHighScorePaper,
  type ScoreRelease
} from "../../../api/scoreReleases";
import { ApiClientError } from "../../../api/client";
import { getBusinessCommandSession, isBusinessCommandSessionCurrent } from "../../../api/businessCommand";
import { isStepUpCancelledError, useStepUp } from "../../../auth/stepUpContext";
import type { PendingCorrection } from "../releaseStatus";
import type { ExamEducationStage } from "./useExamEducationStage";
import { closedWorkflowModal, workflowModalReducer } from "./workflowModal";

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

export type ReleaseVisibility = typeof defaultReleaseVisibility;

export function releaseVisibilityPreset(kind: "basic" | "question", appealEnabled: boolean) {
  if (kind === "question") return { ...defaultReleaseVisibility, show_feedback: true, show_rubric_summary: true, show_question_stem: true, show_exact_rank: false, show_high_score_paper: false };
  // 答案随已公开的逐题成绩固定提供；基础模板仍可只显示总分，不顺带公开反馈或其它逐题内容。
  return { ...Object.fromEntries(Object.keys(defaultReleaseVisibility).map(key => [key, false])) as typeof defaultReleaseVisibility, show_question_scores: appealEnabled, show_answers: true };
}

/** 会显示学生名次或相对位置的公开项。义务教育学校考试结果不排名、不公布（教基厅函〔2021〕34号）。 */
export const rankVisibilityKeys = ["show_exact_rank", "show_percentile"] as const;

export function revealsRank(policy: Pick<ReleaseVisibility, (typeof rankVisibilityKeys)[number]>) {
  return rankVisibilityKeys.some(key => policy[key]);
}

/** 发布弹窗的两个预设都不公开排名和百分位；确需公开时在“自定义”里单独勾选。 */
export function presetVisibility(kind: "basic" | "question", appealEnabled: boolean): ReleaseVisibility {
  return { ...releaseVisibilityPreset(kind, appealEnabled), show_exact_rank: false, show_percentile: false };
}

export const questionReleaseContent = new Set<keyof typeof releaseVisibilityLabels>(["show_feedback", "show_rubric_summary", "show_question_statistics", "show_question_stem", "show_knowledge_analysis", "show_answers", "show_high_score_paper"]);

export function visibleReleaseContent(policy: ScoreRelease["visibility_policy"]) {
  // 答案不再单独配置，旧版策略中的false也不隐藏答案；逐题详情未公开时仍不能承诺可见。
  return (Object.keys(releaseVisibilityLabels) as Array<keyof typeof releaseVisibilityLabels>)
    .filter((key) => (key === "show_answers" || policy[key]) && (policy.show_question_scores || !questionReleaseContent.has(key)))
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
  show_high_score_paper: "匿名优秀答卷"
};

export type PublishPreset = "basic" | "question" | "custom";
export const defaultHighScoreTopN = 10;
export const maxHighScoreTopN = 100;

type RunAction = (key: string, action: () => Promise<void>, successText: string, isCurrent?: () => boolean) => Promise<void>;
export type ScorePublicationPreviewOptions = { onSnapshot?: (snapshot: string) => void; onConfirm?: (snapshot: string) => void; retrySnapshot?: string; isCurrent?: () => boolean };

// 名次范围决定草稿冻结的优秀答卷集合，修改范围也须生成新草稿；固定公开的答案标记不参与区分。
function draftKey(examId: string, policy: ReleaseVisibility, appealEnabled: boolean, reason: string, highScoreTopN: number | null) {
  const normalized = { ...policy, show_answers: true };
  const ordered = Object.keys(normalized).sort().map(key => `${key}=${normalized[key as keyof ReleaseVisibility]}`);
  return JSON.stringify([examId, ordered, appealEnabled, reason, policy.show_high_score_paper ? highScoreTopN : null]);
}

/**
 * 发布页的三条写路径：一步发布（先生成草稿再发布）、发布复评更正、发布记录里已有的草稿。
 * 正式发布都经过近期身份验证；身份验证取消时保留已生成的草稿，重试复用同一份，不重复占用版本号。
 */
export function useScoreReleaseWorkflow({
  selectedExamId,
  selectedExam,
  currentPublishedRelease,
  educationStage,
  runAction,
  reload,
  onPreview
}: {
  selectedExamId: string;
  selectedExam?: Exam;
  currentPublishedRelease?: ScoreRelease;
  educationStage: ExamEducationStage;
  runAction: RunAction;
  reload: () => Promise<void>;
  onPreview: (release: ScoreRelease, options?: ScorePublicationPreviewOptions) => void;
}) {
  const { message, modal } = App.useApp();
  const { runWithStepUp, open: awaitingAuthentication } = useStepUp();
  const [publishModal, dispatchPublishModal] = useReducer(workflowModalReducer, closedWorkflowModal);
  const [preset, setPreset] = useState<PublishPreset>("basic");
  const [visibility, setVisibility] = useState<ReleaseVisibility>(() => presetVisibility("basic", false));
  const [highScoreTopN, setHighScoreTopN] = useState<number | null>(defaultHighScoreTopN);
  const [rankReason, setRankReason] = useState("");
  const [correctionNote, setCorrectionNote] = useState("");
  const [correction, setCorrection] = useState<PendingCorrection | null>(null);
  // 本次弹窗已生成的草稿。键覆盖考试、公开策略和说明；幂等键让请求重试时服务端返回同一份草稿。
  const attemptRef = useRef<{ key: string; idempotencyKey: string; release?: ScoreRelease } | null>(null);
  const viewedSnapshots = useRef(new Map<string, { snapshot: string; session: ReturnType<typeof getBusinessCommandSession> }>());
  const appealEnabled = Boolean(selectedExam?.appeal_enabled);
  const republish = Boolean(currentPublishedRelease);
  const examRef = useRef(selectedExamId);
  examRef.current = selectedExamId;
  useEffect(() => {
    const clearSnapshots = () => {
      viewedSnapshots.current.clear();
      attemptRef.current = null;
    };
    window.addEventListener("business-command-scope-changed", clearSnapshots);
    return () => window.removeEventListener("business-command-scope-changed", clearSnapshots);
  }, []);
  // 预览或草稿回执可能迟到。考试或会话变化后，旧操作不能再打开预览或开始发布。
  const currentGuard = () => {
    const session = getBusinessCommandSession();
    const examId = selectedExamId;
    return () => isBusinessCommandSessionCurrent(session) && examRef.current === examId;
  };

  const showDraftPreview = (release: ScoreRelease) => {
    const isCurrent = currentGuard();
    const session = getBusinessCommandSession();
    onPreview(release, { isCurrent, onSnapshot: snapshot => { if (isCurrent()) viewedSnapshots.current.set(release.id, { snapshot, session }); } });
  };

  const openPublish = () => {
    attemptRef.current = null;
    setPreset("basic");
    setVisibility(presetVisibility("basic", appealEnabled));
    const previousTopN = currentPublishedRelease?.visibility_policy.high_score_paper_top_n;
    setHighScoreTopN(typeof previousTopN === "number" && Number.isInteger(previousTopN) && previousTopN >= 1 && previousTopN <= maxHighScoreTopN ? previousTopN : defaultHighScoreTopN);
    setRankReason("");
    setCorrectionNote("");
    dispatchPublishModal({ type: "open" });
  };
  const closePublish = () => {
    attemptRef.current = null;
    dispatchPublishModal({ type: "close" });
  };
  const choosePreset = (next: PublishPreset) => {
    setPreset(next);
    if (next !== "custom") setVisibility(presetVisibility(next, appealEnabled));
  };

  const reasonText = () => {
    const base = republish ? `重新发布：${correctionNote.trim()}` : "首次发布本场成绩";
    // 义务教育学段公开排名属于例外，原因随版本保存，便于事后核对。
    return revealsRank(visibility) && educationStage !== "senior" ? `${base}；公开排名原因：${rankReason.trim()}` : base;
  };

  const validate = () => {
    if (!selectedExamId) return "请先选择考试";
    // 现有申请绑定单题发布事实；只公开总分时没有可操作的申请对象，不能开放空入口。
    if (appealEnabled && !visibility.show_question_scores) return "允许学生申请成绩复核时，必须公开逐题得分。请开启逐题得分后再发布。";
    if (visibility.show_high_score_paper && !visibility.show_question_scores) return "提供优秀答卷时，必须公开逐题得分。";
    if (visibility.show_high_score_paper && (highScoreTopN === null || !Number.isInteger(highScoreTopN) || highScoreTopN < 1 || highScoreTopN > maxHighScoreTopN)) return `请填写 1 至 ${maxHighScoreTopN} 之间的整数，设置优秀答卷的名次范围。`;
    if (republish && !correctionNote.trim()) return "请写明本次重新发布的原因";
    if (revealsRank(visibility) && educationStage !== "senior" && !rankReason.trim()) return "义务教育学段考试结果默认不排名、不公布；确需公开排名时，请填写原因";
    return "";
  };

  const ensureDraft = async (reason: string) => {
    const key = draftKey(selectedExamId, visibility, appealEnabled, reason, highScoreTopN);
    if (attemptRef.current?.key !== key) attemptRef.current = { key, idempotencyKey: crypto.randomUUID() };
    const attempt = attemptRef.current;
    if (attempt.release) return attempt.release;
    const response = await createScoreRelease(selectedExamId, {
      // 保留兼容字段并固定为true；即使旧表单值仍为false，也不能再次生成隐藏答案的版本。
      reason, idempotency_key: attempt.idempotencyKey, visibility_policy: { ...visibility, show_answers: true, ...(visibility.show_high_score_paper ? { high_score_paper_top_n: highScoreTopN ?? defaultHighScoreTopN } : {}) }, appeal_window: { enabled: appealEnabled }
    });
    attempt.release = response.score_release;
    return response.score_release;
  };

  // 身份验证被取消时草稿已经存在：先重读页面，再把取消交给调用方静默结束，不当作发布失败。
  const publishWithStepUp = async (release: ScoreRelease, reason: string, isCurrent: () => boolean, onPublished?: () => void, confirmedSnapshot?: string) => {
    if (!isCurrent()) return;
    let attemptedSnapshot = confirmedSnapshot;
    try {
      // 用已经展示的快照，或者固定本次确认的草稿；身份验证重试也必须携带原摘要。
      const viewed = viewedSnapshots.current.get(release.id);
      const snapshot = confirmedSnapshot ?? (viewed && isBusinessCommandSessionCurrent(viewed.session) ? viewed.snapshot : undefined)
        ?? (await getScoreReleaseDetail(release.id, undefined, "draft")).publication_snapshot;
      if (!snapshot) throw new ApiClientError(409, "score_release_confirmation_unavailable", "待确认的成绩快照不可用，请刷新后重试。");
      attemptedSnapshot = snapshot;
      if (!isCurrent()) return;
      await runWithStepUp({ reason, action: () => {
        if (!isCurrent()) throw new ApiClientError(409, "score_release_context_changed", "已切换考试或账号，请重新确认发布。");
        return publishScoreRelease(release.id, snapshot);
      } });
      if (isCurrent()) {
        viewedSnapshots.current.delete(release.id);
        onPublished?.();
      }
    } catch (error) {
      if (isCurrent() && isStepUpCancelledError(error)) await reload();
      if (isCurrent() && error instanceof ApiClientError && error.code === "score_release_snapshot_changed") {
        // 仅重新展示可发布候选。没有新的显式确认，即使已完成近期身份验证也不能重发。
        onPreview(release, {
          retrySnapshot: attemptedSnapshot,
          isCurrent,
          onConfirm: latest => {
            if (!isCurrent()) return;
            void runAction("release-publish", () => publishWithStepUp(release, reason, isCurrent, () => {
              onPublished?.();
              onPreview({ ...release, status: "published" });
            }, latest), "成绩版本已发布", isCurrent);
          }
        });
      }
      if (isCurrent() && error instanceof ApiClientError && error.code === "score_release_draft_refresh_required" && attemptRef.current?.release?.id === release.id) {
        // 候选优秀答卷变化时不能复用既有匿名资产；下一次预览或发布从当前正式成绩另建草稿。
        attemptRef.current = null;
        viewedSnapshots.current.delete(release.id);
      }
      throw error;
    }
  };

  const submitPublish = () => {
    const invalid = validate();
    if (invalid) { message.error(invalid); return; }
    const reason = reasonText();
    // 生成草稿期间也可能切换账号；旧请求的回执不能触发后来会话的预览或发布。
    const isCurrent = currentGuard();
    void runAction("release-publish", async () => {
      const release = await ensureDraft(reason);
      await publishWithStepUp(release, `正式发布成绩版本 V${release.version}`, isCurrent, closePublish);
    }, "成绩已发布，学生现在可以查看", isCurrent);
  };

  const previewPublish = () => {
    const invalid = validate();
    if (invalid) { message.error(invalid); return; }
    const reason = reasonText();
    const ready = attemptRef.current?.key === draftKey(selectedExamId, visibility, appealEnabled, reason, highScoreTopN) ? attemptRef.current.release : undefined;
    if (ready) { showDraftPreview(ready); return; }
    // 学生视角只能读取已冻结的草稿；预览会先生成草稿，学生在正式发布前看不到它。
    const isCurrent = currentGuard();
    void runAction("release-preview", async () => {
      const draft = await ensureDraft(reason);
      if (isCurrent()) showDraftPreview(draft);
    }, "已按当前设置生成草稿，学生暂时看不到", isCurrent);
  };

  const openCorrection = (item: PendingCorrection) => setCorrection(item);
  const closeCorrection = () => setCorrection(null);

  // 服务端按复评任务只保留一份结果草稿；重复点击或回执丢失都返回同一份，不会复制版本。
  const ensureCorrectionDraft = async (item: PendingCorrection) => item.draft ?? (await createRegradeScoreRelease(item.job.id, {
    reason: `题目复评完成：${item.job.reason_text}`, idempotency_key: crypto.randomUUID()
  })).score_release;

  const publishCorrection = () => {
    const item = correction;
    if (!item) return;
    const isCurrent = currentGuard();
    void runAction("release-publish", async () => {
      const release = await ensureCorrectionDraft(item);
      if (!isCurrent()) return;
      // 服务端按复评任务复用结果版本；另一位管理员或丢失回执的上一次操作可能已经发布，不能再次发布同一版本。
      if (release.status !== "published") await publishWithStepUp(release, `发布成绩更正 V${release.version}`, isCurrent, closeCorrection);
      else closeCorrection();
    }, "更正已发布，学生现在看到新成绩", isCurrent);
  };

  const previewCorrection = () => {
    const item = correction;
    if (!item) return;
    if (item.draft) { showDraftPreview(item.draft); return; }
    const isCurrent = currentGuard();
    void runAction("release-preview", async () => {
      const draft = await ensureCorrectionDraft(item);
      if (!isCurrent()) return;
      // 记住已生成的草稿，确认发布时直接使用，不再重复请求生成。
      setCorrection(current => current?.job.id === item.job.id ? { ...current, draft } : current);
      showDraftPreview(draft);
    }, "已生成更正草稿，学生暂时看不到", isCurrent);
  };

  // 发布记录里已有的草稿：校验冻结的策略，不读取仍可编辑的发布表单。
  const publishRelease = (release: ScoreRelease) => {
    if (release.appeal_window.enabled && !release.visibility_policy.show_question_scores) {
      message.error("此草稿开放复核但未公开逐题得分，学生无法申请。请重新发布并公开逐题得分。");
      return;
    }
    const visibleItems = visibleReleaseContent(release.visibility_policy).join("、") || "总分";
    const isCurrent = currentGuard();
    modal.confirm({
      title: `发布 V${release.version}`,
      content: (
        <div>
          <p>考试：{selectedExam?.name} · 发布内容以此草稿冻结的成绩为准</p>
          <p>向学生公开：{visibleItems}</p>
          <p>成绩复核：{release.appeal_window.enabled ? "此版本启用逐题复核；学生可在开放时段内从逐题详情申请。" : "此版本未开放学生复核申请。"}</p>
          {currentPublishedRelease && release.version < currentPublishedRelease.version
            ? <p role="alert">学生端当前为 V{currentPublishedRelease.version}；此次发布较早生成的 V{release.version}，学生看到的版本号会变小。请核对草稿内容。</p>
            : null}
          {release.visibility_policy.show_high_score_paper ? <p>{release.visibility_policy.high_score_paper_top_n === undefined
            ? "此草稿沿用旧版规则，仅选取本校一份最高分答卷。"
            : `选取本场本学科本校前 ${release.visibility_policy.high_score_paper_top_n} 名的优秀答卷，截止名次同分全部纳入。`}
            发布前生成匿名副本。如答卷缺少已确认的版面和身份区域，发布会被阻断。</p> : null}
          <p>发布后学生立即看到此版本；之后需要更正时，再发布新版本。</p>
        </div>
      ),
      okText: "发布到学生端",
      cancelText: "取消",
      onOk: () => runAction("release-publish", async () => {
        await publishWithStepUp(release, `正式发布成绩版本 V${release.version}`, isCurrent);
      }, "成绩版本已发布", isCurrent)
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

  return {
    publishOpen: publishModal.status === "open", openPublish, closePublish, republish, awaitingAuthentication,
    preset, choosePreset, visibility, setVisibility, highScoreTopN, setHighScoreTopN, rankReason, setRankReason,
    correctionNote, setCorrectionNote, submitPublish, previewPublish,
    correction, openCorrection, closeCorrection, publishCorrection, previewCorrection,
    publishRelease, showDraftPreview, revokeSharedPaper
  };
}
