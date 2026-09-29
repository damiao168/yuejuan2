import { getApiErrorMessage } from "./userError";

export interface ApiErrorPayload {
  error?: {
    code?: string;
    message?: string;
  };
  code?: string;
  message?: string;
  request_id?: string;
  trace_id?: string;
  field_errors?: Record<string, string[]>;
  conflict_revision?: number;
}

export class ApiClientError extends Error {
  status: number;
  code: string;
  requestId?: string;
  traceId?: string;
  fieldErrors?: Record<string, string[]>;
  conflictRevision?: number;

  constructor(status: number, code: string, message: string, context: Pick<ApiErrorPayload, "request_id" | "trace_id" | "field_errors" | "conflict_revision"> = {}) {
    super(message);
    this.name = "ApiClientError";
    this.status = status;
    this.code = code;
    this.requestId = context.request_id;
    this.traceId = context.trace_id;
    this.fieldErrors = context.field_errors;
    this.conflictRevision = context.conflict_revision;
  }
}

export interface DesktopApiClientOptions {
  baseUrl: string;
  getToken?: () => string | null;
}

export interface SystemStatus {
  status: "healthy" | "degraded";
  service: string;
  environment: string;
  version: string;
  started_at: string;
  generated_at: string;
  uptime_sec: number;
  dependencies: {
    name: string;
    status: "ok" | "error" | "not_configured";
    error?: string;
    detail?: string;
    duration_ms: number;
    checked_at: string;
  }[];
  observability: {
    log_format: string;
    system_log_stream: string;
    audit_log_stream: string;
    request_id_header: string;
    trace_id_header: string;
    slow_request_threshold_ms: number;
    slow_query_log: string;
    sensitive_log_policy: string;
  };
}

export class DesktopApiClient {
  private readonly baseUrl: string;
  private readonly getToken?: () => string | null;

  constructor(options: DesktopApiClientOptions) {
    this.baseUrl = normalizeBaseUrl(options.baseUrl);
    this.getToken = options.getToken;
  }

  url(path: string): string {
    if (!path.startsWith("/") || path.startsWith("//")) {
      throw new Error("API path must be a same-server absolute path");
    }
    return `${this.baseUrl}${path}`;
  }

  authorizationHeader(): string | null {
    const token = this.getToken?.();
    return token ? `Bearer ${token}` : null;
  }

  async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    const isFormData = typeof FormData !== "undefined" && init.body instanceof FormData;
    if (init.body && !headers.has("Content-Type") && !isFormData) {
      headers.set("Content-Type", "application/json");
    }
    const authorization = this.authorizationHeader();
    if (authorization) {
      headers.set("Authorization", authorization);
    }
    addIdempotencyHeader(path, init.method, headers);
    const response = await fetch(this.url(path), { ...init, headers });
    if (!response.ok) {
      throw await apiClientErrorFromResponse(response);
    }
    if (response.status === 204) {
      return undefined as T;
    }
    return (await response.json()) as T;
  }

  async health() {
    return this.request<Record<string, unknown>>("/health");
  }

  async systemStatus() {
    return this.request<SystemStatus>("/api/v1/system/status");
  }

  async requestBlob(path: string, init: RequestInit = {}) {
    const headers = new Headers(init.headers);
    const authorization = this.authorizationHeader();
    if (authorization) {
      headers.set("Authorization", authorization);
    }
    addIdempotencyHeader(path, init.method, headers);
    const response = await fetch(this.url(path), { ...init, headers });
    if (!response.ok) {
      throw await apiClientErrorFromResponse(response);
    }
    return {
      blob: await response.blob(),
      contentType: response.headers.get("Content-Type") ?? "application/octet-stream",
      filename: filenameFromDisposition(response.headers.get("Content-Disposition"))
    };
  }
}

function addIdempotencyHeader(path: string, method: string | undefined, headers: Headers) {
  // 默认键只覆盖这一次请求；跨重试保持同一业务操作时，调用方必须传入已保存的键。
  const normalizedMethod = (method ?? "GET").toUpperCase();
  if (!["POST", "PUT", "PATCH", "DELETE"].includes(normalizedMethod) || path.startsWith("/api/v1/auth/")) {
    return;
  }
  if (!headers.has("Idempotency-Key")) {
    headers.set("Idempotency-Key", crypto.randomUUID());
  }
}

export function normalizeBaseUrl(value: string) {
  const trimmed = value.trim().replace(/\/+$/, "");
  if (!trimmed) {
    throw new Error("API 服务端地址不能为空");
  }
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    throw new Error("API 服务端地址格式无效");
  }
  if (parsed.username || parsed.password) {
    throw new Error("API 服务端地址不能包含用户名或密码");
  }
  if (parsed.search || parsed.hash) {
    throw new Error("API 服务端地址不能包含查询参数或片段");
  }
  if (parsed.protocol !== "https:" && !(parsed.protocol === "http:" && isLoopbackHost(parsed.hostname))) {
    throw new Error("远程 API 必须使用 HTTPS；HTTP 仅允许本机开发地址");
  }
  return parsed.toString().replace(/\/+$/, "");
}

function isLoopbackHost(hostname: string) {
  const normalized = hostname.replace(/^\[|\]$/g, "").toLowerCase();
  return normalized === "localhost" || normalized === "::1" || normalized.startsWith("127.");
}

export async function apiClientErrorFromResponse(response: Response): Promise<ApiClientError> {
  try {
    const payload = (await response.json()) as ApiErrorPayload;
    // 页面只展示已知错误码的本地文案，追踪号和字段冲突信息另存，避免直接显示服务端内部消息。
    const code = payload.error?.code ?? payload.code ?? "request_failed";
    return new ApiClientError(response.status, code, getApiErrorMessage(code, response.status), payload);
  } catch {
    return new ApiClientError(response.status, "request_failed", getApiErrorMessage("request_failed", response.status));
  }
}

function filenameFromDisposition(disposition: string | null): string | undefined {
  if (!disposition) {
    return undefined;
  }
  const utf8Match = disposition.match(/filename\*=UTF-8''([^;]+)/i);
  if (utf8Match?.[1]) {
    try {
      return decodeURIComponent(utf8Match[1]);
    } catch {
      return utf8Match[1];
    }
  }
  const plainMatch = disposition.match(/filename="?([^";]+)"?/i);
  return plainMatch?.[1];
}
