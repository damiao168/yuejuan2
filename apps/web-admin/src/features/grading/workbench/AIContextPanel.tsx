import { Alert, Button, Collapse, Descriptions, Empty, Space, Tag } from "antd";
import type { ReviewTaskContext } from "../../../api/review";
import { requiresExplicitSecondOpinion, secondOpinionMetadata } from "./reviewContext";

function scalar(value: unknown): string {
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") return String(value);
  return "—";
}

function flagList(source: Record<string, unknown>): string[] {
  const candidate = source.risk_flags ?? source.risks ?? source.flags;
  return Array.isArray(candidate) ? candidate.filter((item): item is string => typeof item === "string") : [];
}

function calibrationStatusLabel(value: unknown): string {
  if (value === "calibrated" || value === "qualified" || value === "ready") return "已校准";
  if (value === "pending" || value === "in_progress") return "校准中";
  if (value === "failed" || value === "unqualified") return "未通过校准";
  return "未知状态";
}

function candidateSourceLabel(value: string): string {
  const labels: Record<string, string> = {
    ai: "智能评分",
    model: "评分模型",
    rule: "规则判断",
    ensemble: "综合判断"
  };
  return labels[value] ?? "智能评分候选";
}

function candidateDecisionLabel(value: string | undefined): string {
  if (!value) return "待人工确认";
  const labels: Record<string, string> = {
    accept: "建议通过",
    reject: "建议不通过",
    review: "建议人工复核",
    abstain: "已放弃自动判断"
  };
  return labels[value] ?? (/[\u3400-\u9fff]/u.test(value) ? value : "待人工确认");
}

function riskFlagLabel(value: string): string {
  const labels: Record<string, string> = {
    low_confidence: "置信度较低",
    score_anomaly: "分数异常",
    evidence_incomplete: "证据不完整",
    calibration_required: "需要校准"
  };
  return labels[value] ?? "其他风险";
}

export function AIContextPanel({
  context,
  visible,
  onReveal
}: {
  context: ReviewTaskContext;
  visible: boolean;
  onReveal: () => void;
}) {
  const explicit = requiresExplicitSecondOpinion(context);
  // 显式揭示前跳过整个候选与证据面板，避免只隐藏分数却提前透露 AI 结论。
  if (explicit && !visible) {
    return (
      <section className="evidence-panel" aria-label="AI 第二意见">
        <Alert
          type="info"
          showIcon
          message="本题由教师独立评分"
          description="高风险题不会预填 AI 分数。如确有需要，可主动查看第二意见，系统会保留这一操作的明确语义。"
          action={context.ai_second_opinion?.available ? <Button onClick={onReveal}>查看第二意见</Button> : undefined}
        />
      </section>
    );
  }

  const metadata = secondOpinionMetadata(context, visible);
  const flags = metadata ? flagList(metadata) : [];
  const candidates = context.ai_candidates;
  return (
    <section className="evidence-panel" aria-label="评分候选与证据">
      <div className="panel-head">
        <div>
          <h2>{explicit ? "AI 第二意见" : "评分候选与证据"}</h2>
          <p>{candidates.length} 个候选 · {context.scoring_evidence.length} 条结构化证据</p>
        </div>
      </div>
      {metadata ? (
        <Descriptions size="small" column={1} colon={false}>
          <Descriptions.Item label="模型版本">{scalar(metadata.model_version ?? metadata.engine_version)}</Descriptions.Item>
          <Descriptions.Item label="校准状态">{calibrationStatusLabel(metadata.calibration_status ?? metadata.status)}</Descriptions.Item>
          <Descriptions.Item label="置信度">{scalar(metadata.confidence)}</Descriptions.Item>
          <Descriptions.Item label="风险 / 自动放弃">
            <Space wrap size={[4, 4]}>
              {flags.map((item) => <Tag color="orange" key={item}>{riskFlagLabel(item)}</Tag>)}
              {metadata.abstain === true ? <Tag color="red">已放弃自动判断</Tag> : null}
              {!flags.length && metadata.abstain !== true ? "无已报告风险" : null}
            </Space>
          </Descriptions.Item>
        </Descriptions>
      ) : null}
      {candidates.length ? (
        <Collapse
          ghost
          size="small"
          items={candidates.map((candidate) => ({
            key: candidate.id,
            label: `${candidateSourceLabel(candidate.source)} · ${candidate.engine_version || "版本未知"}`,
            children: (
              <Descriptions size="small" column={1} colon={false}>
                <Descriptions.Item label="结论">{candidateDecisionLabel(candidate.display_text || candidate.decision)}</Descriptions.Item>
                <Descriptions.Item label="配置版本">{candidate.profile_version || "—"}</Descriptions.Item>
                <Descriptions.Item label="置信度">{candidate.confidence ?? "—"}</Descriptions.Item>
                <Descriptions.Item label="当前候选">{candidate.is_current ? "是" : "否"}</Descriptions.Item>
                <Descriptions.Item label="证据">{Object.keys(candidate.evidence).length ? `${Object.keys(candidate.evidence).length} 项` : "无"}</Descriptions.Item>
              </Descriptions>
            )
          }))}
        />
      ) : metadata ? null : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无 AI 候选，按评分细则人工评分" />}
      {context.scoring_evidence.length ? (
        <div className="structured-evidence-list">
          {context.scoring_evidence.map((item) => (
            <Tag key={item.id}>评分证据{typeof item.quality === "number" ? ` · ${Math.round(item.quality * 100)}%` : ""}</Tag>
          ))}
        </div>
      ) : null}
    </section>
  );
}
