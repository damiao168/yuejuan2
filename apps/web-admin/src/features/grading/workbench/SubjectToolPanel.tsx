import { Button, Descriptions, Space, Tag } from "antd";
import type { ReviewTaskContext } from "../../../api/review";
import { isChoiceReview, requiresExplicitSecondOpinion, subjectToolDescriptor } from "./reviewContext";

const subjectLabels: Record<string, string> = {
  chinese: "语文",
  mathematics: "数学",
  english: "英语",
  physics: "物理",
  chemistry: "化学",
  biology: "生物",
  history: "历史",
  geography: "地理",
  ethics_politics: "道德与法治"
};

const claimLabels: Record<string, string> = {
  unclaimed: "未领取",
  assigned: "已分配",
  claimed: "已领取",
  expired: "领取已过期"
};

export function SubjectToolPanel({
  context,
  secondOpinionVisible,
  onRevealSecondOpinion
}: Readonly<{
  context: ReviewTaskContext;
  secondOpinionVisible: boolean;
  onRevealSecondOpinion: () => void;
}>) {
  const tool = subjectToolDescriptor(context);
  const needsReveal = requiresExplicitSecondOpinion(context) && !secondOpinionVisible;
  if (isChoiceReview(context)) return null;
  return (
    <section className="subject-tool-panel" aria-label="学科阅卷工具">
      <div className="panel-head">
        <div>
          <h2>{tool.label}</h2>
          <p>
            {subjectLabels[context.subject_tool_hints.subject_code] ?? "其他学科"}
          </p>
        </div>
        {needsReveal && !context.ai_failure && context.ai_second_opinion?.available ? (
          <Button onClick={onRevealSecondOpinion}>查看 AI 第二意见</Button>
        ) : null}
      </div>
      <Space wrap size={[4, 6]}>
        {tool.capabilities.map((item) => <Tag key={item}>{item}</Tag>)}
      </Space>
      <details className="grading-reference-details"><summary>任务与评分版本详情</summary>
      <Descriptions size="small" column={1} colon={false} className="subject-tool-facts">
        <Descriptions.Item label="任务领取">
          {claimLabels[context.claim.state] ?? context.claim.state}
          {context.claim.expires_at ? ` · ${new Date(context.claim.expires_at).toLocaleTimeString()} 到期` : ""}
        </Descriptions.Item>
        <Descriptions.Item label="评分版本">
          {`细则 ${context.frozen_rubric.version} · 快照 ${context.question_snapshot.snapshot_version}`}
        </Descriptions.Item>
        <Descriptions.Item label="证据类型">
          {context.subject_tool_hints.allowed_evidence_types.join("、") || "未配置"}
        </Descriptions.Item>
      </Descriptions>
      </details>
    </section>
  );
}
