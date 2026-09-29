import { invoke } from "@tauri-apps/api/core";
import type { CapabilityProbe, LocalCacheSecurityStatus, LocalLogEntry, RuntimeDiagnostics } from "../types";

const logKey = "edugrade.desktop.logs";
const redactedValue = "[REDACTED]";
const sensitiveAssignmentPattern = /["']?\b(authorization|access[_-]?token|refresh[_-]?token|id[_-]?token|password|secret|credential)\b["']?\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s,;}]+)/gi;
const bearerPattern = /\bBearer\s+[A-Za-z0-9._~+/=-]+/gi;
let runtimeLogs: LocalLogEntry[] = [];

export interface StoredDesktopCredentials {
  server_url: string;
  tenant_code: string;
  username: string;
  password: string;
}

export function isTauriRuntime() {
  return typeof window !== "undefined" && Boolean(window.__TAURI_INTERNALS__);
}

export async function getRuntimeDiagnostics(): Promise<RuntimeDiagnostics> {
  const native = await invokeOptional<RuntimeDiagnostics>("runtime_diagnostics");
  if (native) {
    return native;
  }
  return {
    runtime: "browser-dev",
    platform: navigator.platform || "unknown",
    appVersion: "0.1.0",
    logPath: "浏览器开发模式 localStorage"
  };
}

export async function getCapabilityStatuses(sessionId?: string): Promise<CapabilityProbe[]> {
  const native = await invokeOptional<CapabilityProbe[]>("capability_statuses", { sessionId: sessionId || null });
  if (native) {
    return native;
  }
  return [
    {
      key: "secure_config",
      name: "本地加密配置存储",
      status: "unavailable",
      detail: "浏览器开发模式不提供系统凭据库；不会降级为 localStorage 保存密码。"
    },
    {
      key: "local_cache",
      name: "SQLite 本地缓存",
      status: "not_configured",
      detail: "未配置/待接入 SQLite schema、加密密钥和离线任务包缓存。"
    },
    {
      key: "device_binding",
      name: "设备绑定",
      status: "not_configured",
      detail: "未配置/待接入后端设备登记、吊销和绑定校验接口。"
    },
    {
      key: "auto_update",
      name: "自动更新",
      status: "not_configured",
      detail: "未配置/待接入内网更新源、签名校验和灰度策略。"
    }
  ];
}

export async function appendLocalLog(entry: Omit<LocalLogEntry, "id" | "at">) {
  // 写入原生日志或浏览器缓存之前统一脱敏，避免调用方漏处理令牌和密码。
  const next = redactLocalLogEntry({
    id: crypto.randomUUID(),
    at: new Date().toISOString(),
    ...entry
  });
  if (isTauriRuntime()) {
    await invoke("append_local_log", { entry: next });
    runtimeLogs = [next, ...runtimeLogs].slice(0, 120);
  } else {
    const current = readLocalLogs();
    window.localStorage.setItem(logKey, JSON.stringify([next, ...current].slice(0, 120)));
  }
  return next;
}

export function readLocalLogs(): LocalLogEntry[] {
  if (isTauriRuntime()) {
    return [...runtimeLogs];
  }
  try {
    const raw = window.localStorage.getItem(logKey);
    if (!raw) {
      return [];
    }
    const parsed = JSON.parse(raw) as LocalLogEntry[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export async function clearLocalLogs() {
  if (isTauriRuntime()) {
    await invoke("clear_local_logs");
    runtimeLogs = [];
    return;
  }
  window.localStorage.removeItem(logKey);
}

export async function loadStoredCredentials(): Promise<StoredDesktopCredentials | null> {
  requireTauriCredentialStore();
  return invoke<StoredDesktopCredentials | null>("load_desktop_credentials");
}

export async function saveStoredCredentials(credentials: StoredDesktopCredentials): Promise<void> {
  requireTauriCredentialStore();
  await invoke("save_desktop_credentials", { credentials });
}

export async function deleteStoredCredentials(): Promise<void> {
  requireTauriCredentialStore();
  await invoke("delete_desktop_credentials");
}

export function redactSensitiveText(value: string): string {
  return value
    .replace(bearerPattern, `Bearer ${redactedValue}`)
    .replace(sensitiveAssignmentPattern, (_match, key: string) => `${key}=${redactedValue}`);
}

export function redactLocalLogEntry(entry: LocalLogEntry): LocalLogEntry {
  return {
    ...entry,
    message: redactSensitiveText(entry.message),
    context: entry.context ? redactSensitiveText(entry.context) : entry.context
  };
}

export function scanLocalCacheSecurity(): LocalCacheSecurityStatus {
  const issues: LocalCacheSecurityStatus["issues"] = [];
  let scannedKeys = 0;
  const sensitiveKeyPattern = /(access[_-]?token|refresh[_-]?token|id[_-]?token|auth|session|password|secret|credential)/i;
  const sensitiveValuePattern = /"(access_token|refresh_token|id_token|password|secret|credential|authorization)"\s*:/i;
  for (let index = 0; index < window.localStorage.length; index += 1) {
    const key = window.localStorage.key(index);
    if (!key || !key.startsWith("edugrade.desktop.")) {
      continue;
    }
    scannedKeys += 1;
    if (sensitiveKeyPattern.test(key)) {
      issues.push({
        key,
        severity: "critical",
        message: "本地缓存键名疑似保存认证或密钥数据，请迁移到内存或安全存储。"
      });
    }
    const value = window.localStorage.getItem(key) ?? "";
    if (sensitiveValuePattern.test(value)) {
      issues.push({
        key,
        severity: "critical",
        message: "本地缓存内容疑似包含认证或密钥字段，诊断页已隐藏具体值。"
      });
    }
  }
  return {
    status: issues.length ? "warning" : "passed",
    checkedAt: new Date().toISOString(),
    scannedKeys,
    issues
  };
}

async function invokeOptional<T>(command: string, args?: Record<string, unknown>): Promise<T | null> {
  // 仅供可降级的诊断信息使用；凭据和草稿等必要存储不能通过这里吞掉原生调用失败。
  if (!isTauriRuntime()) {
    return null;
  }
  try {
    return await invoke<T>(command, args);
  } catch {
    return null;
  }
}

function requireTauriCredentialStore() {
  if (!isTauriRuntime()) {
    throw new Error("系统凭据库仅在 Windows 桌面客户端中可用；已拒绝不安全降级。");
  }
}
