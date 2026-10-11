export interface ApiTransport {
  request<T>(path: string, init?: RequestInit): Promise<T>;
}

export function fillPath(template: string, values: Record<string, string | number>): string {
  const parts: string[] = [];
  let copied = 0;
  let cursor = 0;
  while (cursor < template.length) {
    const start = template.indexOf("{", cursor);
    if (start === -1) break;
    const end = template.indexOf("}", start + 1);
    if (end === -1) break;
    cursor = end + 1;
    if (end === start + 1) continue;
    // 首个右括号结束参数；没有右括号时保留原文，不反复尝试同一段未闭合文本。
    const key = template.slice(start + 1, end);
    const value = values[key];
    if (value === undefined || value === null || value === "") {
      throw new Error(`Missing path parameter: ${key}`);
    }
    // 只编码参数值，避免值里的斜杠变成新的路由层级。
    parts.push(template.slice(copied, start), encodeURIComponent(String(value)));
    copied = cursor;
  }
  parts.push(template.slice(copied));
  return parts.join("");
}

function queryValue(value: unknown, key: string): string {
  if (value === null) return "null";
  switch (typeof value) {
    case "string":
      return value;
    case "number":
    case "boolean":
    case "bigint":
    case "undefined":
    case "symbol":
      return String(value);
    default:
      // 当前 SDK 查询契约只传标量或标量数组；对象不能静默变成 [object Object] 发给服务端。
      throw new TypeError(`Unsupported query parameter: ${key}`);
  }
}

export function appendQuery(path: string, values: Record<string, unknown> | undefined): string {
  if (!values) return path;
  const params = new URLSearchParams();
  // 0 和 false 是有效筛选值；数组使用重复键传递，保持服务端的多值查询约定。
  for (const [key, value] of Object.entries(values)) {
    if (value === undefined || value === null || value === "") continue;
    if (Array.isArray(value)) {
      for (const item of value) params.append(key, queryValue(item, key));
    } else {
      params.set(key, queryValue(value, key));
    }
  }
  const query = params.toString();
  return query ? `${path}?${query}` : path;
}

export interface ApiErrorShape {
  code: string;
  message: string;
  request_id?: string;
  trace_id?: string;
  field_errors?: Record<string, string[]>;
  conflict_revision?: number;
}

export function normalizeApiError(payload: unknown): ApiErrorShape {
  // 错误码可位于嵌套 error 中；追踪、字段校验和冲突版本仍从响应顶层提取。
  const candidate = payload && typeof payload === "object" ? (payload as Record<string, unknown>) : {};
  const envelope = candidate.error && typeof candidate.error === "object" ? (candidate.error as Record<string, unknown>) : candidate;
  return {
    code: typeof envelope.code === "string" ? envelope.code : "request_failed",
    message: typeof envelope.message === "string" ? envelope.message : "Request failed",
    request_id: typeof candidate.request_id === "string" ? candidate.request_id : undefined,
    trace_id: typeof candidate.trace_id === "string" ? candidate.trace_id : undefined,
    field_errors: isFieldErrors(candidate.field_errors) ? candidate.field_errors : undefined,
    conflict_revision: typeof candidate.conflict_revision === "number" ? candidate.conflict_revision : undefined
  };
}

function isFieldErrors(value: unknown): value is Record<string, string[]> {
  return Boolean(
    value &&
      typeof value === "object" &&
      Object.values(value).every((messages) => Array.isArray(messages) && messages.every((message) => typeof message === "string"))
  );
}
