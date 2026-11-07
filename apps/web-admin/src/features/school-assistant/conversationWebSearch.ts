import type { SchoolAssistantSource } from "../../api/schoolAssistant";
import type { SchoolConversationEvent } from "../../api/schoolAiChat";
import { emptyConversationEvidence, type ConversationEvidence } from "./conversationState";

export const WEB_PAGE_READ_NOTE = "已读取网页正文。";
export const WEB_UNMARKED_CITATION_NOTE = "回答未逐句标注，以下为已核实资料。";
const publicSourceNotes = [WEB_PAGE_READ_NOTE, "搜索摘要，未读取网页全文。", "网页正文未能读取，仅使用搜索摘要。"];

export function publicSourceNote(value: unknown): string | undefined {
  return typeof value === "string" && publicSourceNotes.some(note => value === note || value === note + WEB_UNMARKED_CITATION_NOTE) ? value : undefined;
}

export function hasReadWebPage(source: Pick<SchoolAssistantSource, "note">): boolean {
  return source.note === WEB_PAGE_READ_NOTE || source.note === WEB_PAGE_READ_NOTE + WEB_UNMARKED_CITATION_NOTE;
}

export function isPublicWebTool(toolName: string): boolean {
  return toolName === "web_search" || toolName === "web_fetch";
}

export function publicSourceURL(value: unknown): string | undefined {
  if (typeof value !== "string" || value.length > 4096) return undefined;
  try {
    const url = new URL(value);
    if (!["https:", "http:"].includes(url.protocol) || url.username || url.password) return undefined;
    const host = url.hostname.toLowerCase().replace(/\.+$/, "");
    if (!host.includes(".") || host === "localhost" || [".localhost", ".local", ".internal", ".lan", ".home"].some(suffix => host.endsWith(suffix)) || host === "[::1]") return undefined;
    if (/^(0|10|127)\./.test(host) || /^169\.254\./.test(host) || /^192\.168\./.test(host) || /^172\.(1[6-9]|2\d|3[01])\./.test(host)) return undefined;
    url.hostname = host;
    return url.href;
  } catch { return undefined; }
}

// 浏览器历史可被手工更改；只恢复公开网页的白名单字段，绝不恢复业务范围或导航。
export function publicConversationSources(value: unknown): SchoolAssistantSource[] {
  if (!Array.isArray(value)) return [];
  return value.slice(0, 5).flatMap<SchoolAssistantSource>((entry: unknown) => {
    if (!entry || typeof entry !== "object") return [];
    const item = entry as Partial<SchoolAssistantSource>;
    const url = publicSourceURL(item.url);
    const note = publicSourceNote(item.note);
    if (item.kind !== "web" || !url || typeof item.id !== "string" || typeof item.label !== "string" || typeof item.read_at !== "string") return [];
    if (Number.isNaN(Date.parse(item.read_at))) return [];
    return [{
      id: item.id.slice(0, 100), kind: "web", status: "available", label: item.label.slice(0, 300),
      read_at: item.read_at, consistency: "source_local", scope_label: "公开网页", url,
      site_name: new URL(url).hostname,
      ...(typeof item.excerpt === "string" ? { excerpt: item.excerpt.slice(0, 2000) } : {}),
      // 历史可被修改；只恢复已知读取标记，不能把任意 note 文案当作已读正文的证据或模型指令。
      ...(note ? { note } : {}),
    }];
  });
}

export function publicConversationEvidence(sources: unknown): ConversationEvidence | undefined {
  const valid = publicConversationSources(sources);
  return valid.length ? { ...emptyConversationEvidence(), sources: valid } : undefined;
}

export function publicSourceNumber(sourceID: string, fallback: number): number {
  // 历史恢复可能过滤无效链接；编号跟随后端来源 ID，不能因列表缩短而错配正文引用。
  const match = /^web-([1-5])$/.exec(sourceID);
  return match ? Number(match[1]) : fallback;
}

export function hasBusinessConversationEvent(event: SchoolConversationEvent): boolean {
  if (event.type === "tool_started" || event.type === "tool_finished") return !isPublicWebTool(event.tool_name);
  return event.type === "sources" && (event.sources.some(source => source.kind !== "web") || event.actions.length > 0 || Boolean(event.scope?.exam_id));
}
