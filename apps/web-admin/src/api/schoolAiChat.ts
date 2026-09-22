import { apiClient } from "./client";

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
