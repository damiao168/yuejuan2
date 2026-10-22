import type { SchoolChatModel } from "../../api/schoolAiChat";
import type { ConversationEvidence } from "./conversationState";

export interface PendingConversation {
  readonly modelConfigID?: string;
  readonly content: string;
  readonly reasoning: string;
  readonly evidence: ConversationEvidence;
}

export function conversationModelStatus(model: SchoolChatModel | null, loading: boolean): string {
  if (loading) return "正在读取模型";
  if (model?.available) return `${model.display_name} · ${model.model_name}`;
  return "模型未就绪";
}

export function conversationPlaceholder(available: boolean, atLimit: boolean, pending: boolean): string {
  if (!available) return "等待学校模型就绪";
  if (atLimit && !pending) return "已达到 5 个并行对话上限";
  return "问考试、阅卷和发布问题，或描述你需要的帮助…";
}
