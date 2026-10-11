import { apiClient } from "./client";
import type { SchoolConversationContext as ApiSchoolConversationContext, SchoolConversationEvent as ApiSchoolConversationEvent } from "@edugrade/sdk";

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
}

export interface SchoolChatModel {
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
  return apiClient.request<{ model: SchoolChatModel }>("/api/v1/ai-chat/model");
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

export function streamSchoolConversation(messages: SchoolChatMessage[], context: SchoolConversationContext | undefined, signal: AbortSignal, onEvent: (event: SchoolConversationEvent) => void) {
  // 名称只供界面展示，模型传入的考试 ID 由服务端重新授权；不把前端标签当成业务事实。
  const requestContext = context ? { exam_id: context.exam_id, origin_module: context.origin_module, origin_section: context.origin_section } : undefined;
  return apiClient.requestEventStream<SchoolConversationEvent>("/api/v1/school-assistant/conversations/stream", {
    method: "POST", body: JSON.stringify({ messages, context: requestContext }), signal
  }, onEvent);
}
