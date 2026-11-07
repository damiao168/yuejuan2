import { ApiClientError, apiClient } from "./client";
import type { SchoolConversationContext as ApiSchoolConversationContext, SchoolConversationEvent as ApiSchoolConversationEvent, SchoolAssistantSource } from "@edugrade/sdk";

export interface SchoolChatAttachment {
  name: string;
  media_type: string;
  content: string;
  size: number;
}

export interface SchoolChatMessage {
  role: "user" | "assistant";
  content: string;
  reasoning_content?: string;
  attachments?: SchoolChatAttachment[];
  // 以下字段供界面、公开问答范围选择和历史恢复；发送前会剥离，不能作为模型已核实的来源。
  web_search?: boolean;
  web_sources?: SchoolAssistantSource[];
}

export interface SchoolChatModel {
  id?: string;
  available: boolean;
  display_name?: string;
  model_name?: string;
  provider_key?: string;
  message: string;
}

export interface SchoolChatCompletion {
  message: SchoolChatMessage;
  model_name: string;
  display_name: string;
  provider_key: string;
  finish_reason?: string;
  usage: { input_tokens: number; output_tokens: number; total_tokens: number };
}

export function getSchoolChatModel() {
  return apiClient.request<{ model: SchoolChatModel; models?: SchoolChatModel[] }>("/api/v1/ai-chat/model");
}

export function sendSchoolChat(messages: SchoolChatMessage[]) {
  return apiClient.request<{ completion: SchoolChatCompletion }>("/api/v1/ai-chat/completions", {
    method: "POST",
    body: JSON.stringify({ messages })
  });
}

export interface SchoolChatStreamEvent {
  type: "reasoning" | "content" | "done" | "error";
  delta?: string;
  completion?: SchoolChatCompletion;
}

export function streamSchoolChat(messages: SchoolChatMessage[], signal: AbortSignal, onEvent: (event: SchoolChatStreamEvent) => void) {
  return apiClient.requestEventStream<SchoolChatStreamEvent>("/api/v1/ai-chat/completions/stream", {
    method: "POST", body: JSON.stringify({ messages }), signal
  }, onEvent);
}

export interface SchoolConversationContext extends ApiSchoolConversationContext {
  exam_name?: string;
}

// SSE 仍由专用传输解析，消息契约直接引用权威 OpenAPI 生成的类型。
export type SchoolConversationEvent = ApiSchoolConversationEvent;

/** 只承接连续的公开搜索问答；普通聊天、校内工具或附件轮次不能被后续追问带入公开模式。 */
export function publicSearchMessages(messages: SchoolChatMessage[]): SchoolChatMessage[] {
  const last = messages[messages.length - 1];
  if (!last) return [];
  // 保留本轮附件让服务端明确拒绝，不静默丢弃后搜索一个与用户原意不同的问题。
  const current = { role: last.role, content: last.content, ...(last.attachments?.length ? { attachments: last.attachments } : {}) };
  const history: SchoolChatMessage[] = [];
  let remaining = 12_000;
  for (let index = messages.length - 2, pairs = 0; index >= 1 && pairs < 6; index -= 2, pairs++) {
    const reply = messages[index];
    const question = messages[index - 1];
    if (reply.role !== "assistant" || question.role !== "user" || reply.web_search !== true || question.web_search !== true
      || reply.attachments?.length || question.attachments?.length) break;
    const answer = Array.from(reply.content).slice(0, 4_000).join("");
    const size = Array.from(question.content).length + Array.from(answer).length;
    if (!question.content.trim() || !answer.trim() || Array.from(question.content).length > 500 || size > remaining) break;
    history.unshift({ role: "user", content: question.content }, { role: "assistant", content: answer });
    remaining -= size;
  }
  return [...history, current];
}

// 服务端最多处理三分钟，浏览器额外留十秒接收收尾；代理悬空时也必须释放本条对话的在途锁。
export const CONVERSATION_REQUEST_TIMEOUT_MS = 190_000;

export async function streamSchoolConversation(messages: SchoolChatMessage[], context: SchoolConversationContext | undefined, signal: AbortSignal, onEvent: (event: SchoolConversationEvent) => void, modelConfigID?: string, webSearch = false) {
  // 名称只供界面展示，模型传入的考试 ID 由服务端重新授权；不把前端标签当成业务事实。
  // 联网开启时仍可选校内分支；页面提示只交服务端保管，服务端在该分支选定后才验证读取，公开规划/检索不使用它。
  const requestContext = context ? { exam_id: context.exam_id, origin_module: context.origin_module, origin_section: context.origin_section } : undefined;
  // 网页来源只用于本地展示；即使调用方未先规范化历史，也不把缓存证据或模式字段当作用户消息发送。
  const requestMessages = (webSearch ? publicSearchMessages(messages) : messages).map(({ role, content, reasoning_content, attachments }) => ({ role, content, ...(!webSearch && reasoning_content ? { reasoning_content } : {}), ...(attachments?.length ? { attachments } : {}) }));
  const controller = new AbortController();
  const stop = () => controller.abort();
  if (signal.aborted) controller.abort();
  else signal.addEventListener("abort", stop, { once: true });
  let timeout: ReturnType<typeof setTimeout> | undefined;
  let finish: (() => void) | undefined;
  let fail: ((error: unknown) => void) | undefined;
  const finished = new Promise<void>((resolve, reject) => { finish = resolve; fail = reject; });
  const deadline = new Promise<never>((_resolve, reject) => {
    timeout = setTimeout(() => {
      reject(new ApiClientError(0, "ai_chat_timeout", "对话处理超时"));
      controller.abort();
    }, CONVERSATION_REQUEST_TIMEOUT_MS);
  });
  try {
    await Promise.race([apiClient.requestEventStream<SchoolConversationEvent>("/api/v1/school-assistant/conversations/stream", {
      method: "POST", body: JSON.stringify({ messages: requestMessages, context: requestContext, model_config_id: modelConfigID || undefined, ...(webSearch ? { web_search: true } : {}) }), signal: controller.signal
    }, (event) => {
      if (event.type === "error") {
        try { onEvent(event); } finally {
          // 错误帧也是终态；即使调用方只记录错误，也不能继续等 HTTP 连接自然断开。
          fail?.(new ApiClientError(0, event.code, event.message));
          controller.abort();
        }
        return;
      }
      onEvent(event);
      if (event.type === "done") {
        // done 已提交最终回复，不再等代理关闭连接；只停止本请求的传输，不把成功回复标成用户取消。
        finish?.();
        controller.abort();
      }
    }), finished, deadline]);
  } finally {
    clearTimeout(timeout);
    signal.removeEventListener("abort", stop);
    controller.abort();
  }
}
