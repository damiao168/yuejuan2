import { Button } from "antd";
import type { ReviewTaskContext } from "../../../api/review";
import { requiresExplicitSecondOpinion, secondOpinionMetadata } from "./reviewContext";

export function AIContextPanel({ context, visible, onReveal }: Readonly<{
  context: ReviewTaskContext;
  visible: boolean;
  onReveal: () => void;
}>) {
  // 普通任务已有评分依据；这里只保留独立评分题主动查看参考分的入口。
  // 当前失败、影子结果和显式查看前都不能透露 AI 分数，也不回填教师草稿。
  if (context.ai_failure || !requiresExplicitSecondOpinion(context) || !context.ai_second_opinion?.available) return null;
  const metadata = secondOpinionMetadata(context, visible);
  if (metadata?.delivery_mode === "shadow_only" || metadata?.status === "failed") return null;
  const score = metadata?.suggested_score;
  const maxScore = metadata?.max_score;
  const hasScore = typeof score === "number" && Number.isFinite(score) && typeof maxScore === "number" && Number.isFinite(maxScore) && maxScore > 0 && score >= 0 && score <= maxScore;
  return <section className="evidence-panel" aria-label="AI 参考">
    <p className="muted">本题请独立评分，AI 结果仅供参考。</p>
    {!visible ? <Button size="small" onClick={onReveal}>查看 AI 参考</Button>
      : <p>{hasScore ? `AI 参考分：${score} / ${maxScore}` : "暂无可用的 AI 参考分。"}</p>}
  </section>;
}
