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
    ensemble: "综合判断",
    omr: "填涂识别",
    ocr: "文字识别",
    manual: "人工录入",
    imported: "导入答案"
  };
  return labels[value] ?? "智能评分候选";
}

function candidateDecisionLabel(value: string | undefined): string {
  if (!value) return "待人工确认";
  const labels: Record<string, string> = {
    accept: "建议通过",
    reject: "建议不通过",
    review: "建议人工复核",
    abstain: "已放弃自动判断",
    selected: "已识别选项",
    confirmed: "识别结果已确认",
    ambiguous: "识别结果不明确",
    blank: "未识别到作答",
    multiple: "填涂数量与本题要求不符",
    parse_failed: "识别结果解析失败"
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

function AICandidatePanel({ candidates, hasMetadata }: Readonly<{
  candidates: ReviewTaskContext["ai_candidates"];
  hasMetadata: boolean;
}>) {
  if (candidates.length) {
    return (
      <Collapse
        ghost
        size="small"
        items={candidates.map((candidate) => ({
          key: candidate.id,
          label: `${candidateSourceLabel(candidate.source)} · ${candidate.engine_version || "版本未知"}`,
          children: (
            <Descriptions size="small" column={1} colon={false}>
              {candidate.display_text ? <Descriptions.Item label="识别内容">{candidate.display_text}</Descriptions.Item> : null}
              <Descriptions.Item label="结论">{candidateDecisionLabel(candidate.decision)}</Descriptions.Item>
              <Descriptions.Item label="配置版本">{candidate.profile_version || "—"}</Descriptions.Item>
              <Descriptions.Item label="置信度">{candidate.confidence ?? "—"}</Descriptions.Item>
              <Descriptions.Item label="当前候选">{candidate.is_current ? "是" : "否"}</Descriptions.Item>
              <Descriptions.Item label="证据">{Object.keys(candidate.evidence).length ? `${Object.keys(candidate.evidence).length} 项` : "无"}</Descriptions.Item>
            </Descriptions>
          )
        }))}
      />
    );
  }
  // 元数据可独立于候选存在；只有两者都缺失时才显示无候选提示。
  if (hasMetadata) return null;
  return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无 AI 候选，按评分细则人工评分" />;
}

export function AIContextPanel({
  context,
  visible,
  onReveal
}: Readonly<{
  context: ReviewTaskContext;
  visible: boolean;
  onReveal: () => void;
}>) {
  // 失败投影属于当前冻结版本；主工作区已给人工提示，此处不再展示旧候选或半截模型内容。
  if (context.ai_failure) return null;
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
  const candidates = context.ai_candidates.filter((candidate) => candidate.is_current);
  const history = context.ai_candidates.filter((candidate) => !candidate.is_current);
  return (
    <details className="grading-reference-details"><summary>{explicit ? "AI 第二意见详情" : "识别与评分证据详情"}</summary>
    <section className="evidence-panel" aria-label="评分候选与证据">
      <div className="panel-head">
        <div>
          <h2>{explicit ? "AI 第二意见" : "当前识别与评分依据"}</h2>
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
      <AICandidatePanel candidates={candidates} hasMetadata={Boolean(metadata)} />
      {history.length ? <details className="grading-reference-details"><summary>历史识别与建议</summary><AICandidatePanel candidates={history} hasMetadata /></details> : null}
      {context.scoring_evidence.length ? (
        <div className="structured-evidence-list">
          {context.scoring_evidence.map((item) => (
            <Tag key={item.id}>评分证据{typeof item.quality === "number" ? ` · ${Math.round(item.quality * 100)}%` : ""}</Tag>
          ))}
        </div>
      ) : null}
    </section>
    </details>
  );
}
