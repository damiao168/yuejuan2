import { getUserErrorMessage } from "../../api/client";
import type { SchoolChatMessage } from "../../api/schoolAiChat";
import { interruptedMessage } from "../../pages/schoolAiChatMessages";
import { interruptedConversationEvidence, type ConversationEvidence } from "./conversationState";
import type { PendingConversation } from "./conversationPresentation";

type ConversationRecovery =
  | { kind: "reply"; reply: SchoolChatMessage; evidence: ConversationEvidence }
  | { kind: "failure"; message: string; evidence: ConversationEvidence };

/** 调用方先校验挂载和 controller 身份；这里只决定该请求中断后保留哪些内容。 */
export function conversationRecovery(error: unknown, aborted: boolean, completed: boolean, partial: PendingConversation): ConversationRecovery | null {
  // done 已接收后再停止不生成第二条回复；非停止错误仍按原流程显示失败。
  if (aborted && completed) return null;
  const evidence = interruptedConversationEvidence(partial.evidence);
  if (!aborted) return { kind: "failure", message: getUserErrorMessage(error, "发送失败，请重试"), evidence };
  const reply = interruptedMessage(partial);
  if (reply) return { kind: "reply", reply, evidence };
  return { kind: "failure", message: "已停止回复", evidence };
}
