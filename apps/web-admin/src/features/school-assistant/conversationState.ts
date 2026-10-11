import type { SchoolConversationContext, SchoolConversationEvent } from "../../api/schoolAiChat";
import type { SchoolAssistantAction, SchoolAssistantScope, SchoolAssistantSource } from "../../api/schoolAssistant";
import type { SessionUser } from "../../auth/session";

export interface ConversationTool {
  call_id: string;
  tool_name: string;
  label: string;
  status: "running" | "completed" | "failed";
  message?: string;
}
export interface ConversationEvidence {
  tools: ConversationTool[];
  sources: SchoolAssistantSource[];
  actions: SchoolAssistantAction[];
  scope?: SchoolAssistantScope;
}
export interface ConversationStreamIdentity { requestId?: string; sequence: number }

export function emptyConversationEvidence(): ConversationEvidence { return { tools: [], sources: [], actions: [] }; }

export function interruptedConversationEvidence(evidence: ConversationEvidence): ConversationEvidence {
  return { ...evidence, actions: [], tools: evidence.tools.map((tool) => tool.status === "running" ? { ...tool, status: "failed", message: "已停止" } : tool) };
}

export function conversationAccountKey(user: SessionUser) {
  const compareLabels = (left: string, right: string) => left.localeCompare(right);
  return JSON.stringify([
    user.tenant,
    user.id,
    [...user.roles].sort(compareLabels),
    [...user.permissions].sort(compareLabels),
    user.organizationScope,
    user.publicComputer,
  ]);
}

export function acceptConversationEvent(identity: ConversationStreamIdentity, event: SchoolConversationEvent) {
  // 同一 SSE 请求只接收其自身的单调事件；重放或串入另一个请求不能覆盖当前回复。
  if (!event.request_id || !Number.isInteger(event.seq) || event.seq <= identity.sequence ||
    (identity.requestId && identity.requestId !== event.request_id)) return false;
  identity.requestId = event.request_id;
  identity.sequence = event.seq;
  return true;
}

export function mergeConversationEvidence(evidence: ConversationEvidence, event: SchoolConversationEvent): ConversationEvidence {
  if (event.type === "sources") return { ...evidence, sources: event.sources, actions: event.actions, scope: event.scope };
  if (event.type !== "tool_started" && event.type !== "tool_finished") return evidence;
  const tool: ConversationTool = {
    call_id: event.call_id, tool_name: event.tool_name, label: event.label,
    status: event.type === "tool_started" ? "running" : event.status,
    ...(event.type === "tool_finished" && event.message ? { message: event.message } : {})
  };
  const existing = evidence.tools.some((item) => item.call_id === tool.call_id);
  return { ...evidence, tools: existing ? evidence.tools.map((item) => item.call_id === tool.call_id ? tool : item) : [...evidence.tools, tool] };
}

export function contextFromEvidence(evidence: ConversationEvidence, current?: SchoolConversationContext): SchoolConversationContext | undefined {
  // 多场考试结果没有单一范围，后续追问需要重新确认，不能沿用最后读到的考试。
  if (!evidence.scope?.exam_id) return evidence.sources.length ? undefined : current;
  return { ...current, exam_id: evidence.scope.exam_id, exam_name: evidence.scope.exam_name ?? evidence.scope.scope_label };
}
