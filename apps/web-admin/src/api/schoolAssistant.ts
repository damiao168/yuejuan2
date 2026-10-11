import { apiClient } from "./client";
import type {
  SchoolAssistantAction as ApiSchoolAssistantAction,
  SchoolAssistantBlock as ApiSchoolAssistantBlock,
  SchoolAssistantCapabilities as ApiSchoolAssistantCapabilities,
  SchoolAssistantCapability as ApiSchoolAssistantCapability,
  SchoolAssistantContextHint as ApiSchoolAssistantContextHint,
  SchoolAssistantEvent as ApiSchoolAssistantEvent,
  SchoolAssistantFact as ApiSchoolAssistantFact,
  SchoolAssistantIntent as ApiSchoolAssistantIntent,
  SchoolAssistantIssue as ApiSchoolAssistantIssue,
  SchoolAssistantQueryRequest as ApiSchoolAssistantQuery,
  SchoolAssistantResult as ApiSchoolAssistantResult,
  SchoolAssistantScope as ApiSchoolAssistantScope,
  SchoolAssistantSource as ApiSchoolAssistantSource,
} from "@edugrade/sdk";

// 业务 DTO 直接复用生成 SDK；页面上下文只在 hook 层扩展 exam_name，避免左侧模块改名后维护两套契约。
export type SchoolAssistantIntent = ApiSchoolAssistantIntent;
export type SchoolAssistantCapability = ApiSchoolAssistantCapability;
export type SchoolAssistantCapabilities = ApiSchoolAssistantCapabilities;
export type SchoolAssistantContext = ApiSchoolAssistantContextHint;
export type SchoolAssistantScope = ApiSchoolAssistantScope;
export type SchoolAssistantFact = ApiSchoolAssistantFact;
export type SchoolAssistantSource = ApiSchoolAssistantSource;
export type SchoolAssistantIssue = ApiSchoolAssistantIssue;
export type SchoolAssistantBlock = ApiSchoolAssistantBlock;
export type SchoolAssistantAction = ApiSchoolAssistantAction;
export type SchoolAssistantResult = ApiSchoolAssistantResult;
export type SchoolAssistantQuery = ApiSchoolAssistantQuery;
export type SchoolAssistantEvent = ApiSchoolAssistantEvent;

export function getSchoolAssistantCapabilities(signal?: AbortSignal) {
  return apiClient.request<SchoolAssistantCapabilities>("/api/v1/school-assistant/capabilities", { signal });
}

// 业务查询沿用现有 SSE 读取器；此接口不在客户端保存问题、事实或来源。
export function streamSchoolAssistantQuery(query: SchoolAssistantQuery, signal: AbortSignal, onEvent: (event: SchoolAssistantEvent) => void) {
  return apiClient.requestEventStream<SchoolAssistantEvent>("/api/v1/school-assistant/queries/stream", {
    method: "POST", body: JSON.stringify(query), signal
  }, onEvent);
}
