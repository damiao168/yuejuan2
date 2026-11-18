import type { SchoolChatModel } from "../../api/schoolAiChat";
import type { ConversationTurnMetrics } from "./conversationMetrics";
import { formatConversationTokens } from "./tokenFormat";

function validTokenCount(value: number | undefined): value is number {
  return value !== undefined && Number.isSafeInteger(value) && value >= 0;
}

function metricsMatchModel(metrics: ConversationTurnMetrics | undefined, model: SchoolChatModel | null) {
  if (!metrics || !model?.available) return false;
  if (metrics.modelConfigID && model.id) return metrics.modelConfigID === model.id;
  return Boolean(metrics.modelName && model.model_name && metrics.modelName === model.model_name);
}

interface Props {
  model: SchoolChatModel | null;
  metrics?: ConversationTurnMetrics;
  pending?: boolean;
}

export function ConversationMetricsView({ model, metrics, pending = false }: Readonly<Props>) {
  // 切换模型后，旧请求的窗口与速率不能冒充新模型的观测结果。
  const current = metricsMatchModel(metrics, model) ? metrics : undefined;
  const budget = current?.contextBudget?.accepted ? current.contextBudget : undefined;
  const estimated = validTokenCount(budget?.estimated_input_tokens) ? budget.estimated_input_tokens : undefined;
  // 上游接口实报只对应这次模型请求；收到后替代调用前估算，不能用多轮累计用量补齐。
  const actual = validTokenCount(budget?.actual_input_tokens) && budget.actual_input_tokens > 0
    ? budget.actual_input_tokens : undefined;
  const input = actual ?? estimated;
  const basis = actual !== undefined ? "实报" : "估算";
  const candidateWindow = validTokenCount(budget?.context_window_tokens) && budget.context_window_tokens > 0
    ? budget.context_window_tokens : model?.context_window_tokens;
  const window = validTokenCount(candidateWindow) && candidateWindow > 0 ? candidateWindow : undefined;
  // 实报超过应用上限可能意味着容量配置不符，不能通过截到 100% 掩盖这项偏差。
  const percent = input !== undefined && window !== undefined ? (input / window * 100).toFixed(1) : undefined;
  const contextLabel = input !== undefined && window !== undefined
    ? `${actual === undefined ? "约 " : ""}${formatConversationTokens(input)} / 应用上限 ${formatConversationTokens(window)} Token（${percent}% · ${basis}）`
    : window !== undefined ? `— / 应用上限 ${formatConversationTokens(window)} Token` : "—";
  const exact = (value: number) => value.toLocaleString("zh-CN");
  const estimateTitle = estimated !== undefined ? `调用前估算输入 ${exact(estimated)} Token。` : "";
  const inputTitle = actual !== undefined
    ? `最近完成的单次模型调用：上游接口实报输入 ${exact(actual)} Token（含缓存输入）。`
    : "最近一次已接受的单次模型请求：";
  const contextTitle = budget && input !== undefined && window !== undefined
    ? `${inputTitle}${estimateTitle}应用上限 ${exact(window)} Token。输入预算 ${exact(budget.input_budget_tokens)} Token，预留输出 ${exact(budget.max_output_tokens)} Token。应用上限采用生效配置，并非自动识别的模型最大窗口。本次输入不是整段对话的累计用量或下一轮预计占用，不含草稿及随后生成的输出。${input > window ? "实报或估算超过应用上限，容量配置待校验。" : ""}`
    : "应用上限来自当前生效配置，不代表供应商模型的物理最大窗口。发送后显示单次模型请求的输入用量，优先采用上游接口实报，缺少实报时显示估算，不使用多轮累计用量。";

  const generation = current?.generation;
  const matchesGeneration = !generation?.model_config_id || !model?.id || generation.model_config_id === model.id;
  const rate = generation && matchesGeneration && validTokenCount(generation.output_tokens) && generation.output_tokens > 0
    && generation.duration_ms > 0 && Number.isFinite(generation.duration_ms)
    && generation.tokens_per_second > 0 && Number.isFinite(generation.tokens_per_second)
    ? generation.tokens_per_second : undefined;
  // 速率直接使用服务端观测；浏览器收到的字符数或 SSE 分块数都不代表 Token 数。
  const speedLabel = rate !== undefined ? `${rate < 0.1 ? "<0.1" : rate.toFixed(1)} Token/s` : pending ? "统计中" : "—";
  const speedTitle = "应用侧最终回答单次调用的平均速率：上游接口输出 Token 数除以服务端调用总耗时，包含请求准备、首 Token 等待、网络及 SSE 传输，不是纯模型解码速率。输出 Token 采用上游接口口径，可能包含推理 Token；排除此前的搜索和工具耗时。完成回答且上游接口提供用量后显示，缺少计量时不估造速率。";

  return <div className="school-chat-metrics" aria-label="对话用量">
    <span className="school-chat-metrics__item" title={contextTitle}>本次输入 <strong>{contextLabel}</strong></span>
    <span className="school-chat-metrics__item" title={speedTitle}>平均速率（含等待） <strong>{speedLabel}</strong></span>
  </div>;
}
