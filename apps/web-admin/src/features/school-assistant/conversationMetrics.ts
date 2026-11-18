import type { SchoolConversationEvent } from "../../api/schoolAiChat";

export type ConversationContextBudget = Extract<SchoolConversationEvent, { type: "context" }>["context_budget"];
export type ConversationGenerationMetrics = NonNullable<Extract<SchoolConversationEvent, { type: "done" }>["generation_metrics"]>;

// 指标只描述当前对话最近一次请求，不参与模型历史或跨刷新恢复。
export interface ConversationTurnMetrics {
  modelConfigID?: string;
  modelName?: string;
  contextBudget?: ConversationContextBudget;
  generation?: ConversationGenerationMetrics;
}
