const LEGACY_STORAGE_KEY = "edugrade.remembered-login.v1";
const LEGACY_DATABASE_NAME = "edugrade-local-auth";

export const LOGIN_FIELD_LIMITS = {
  tenant_code: 128,
  identifier: 256,
  password: 1024
} as const;

export const PUBLIC_COMPUTER_IDLE_LOCK_MS = 15 * 60 * 1000;

export function isPublicComputerIdle(lastActivityAt: number, now = Date.now(), timeout = PUBLIC_COMPUTER_IDLE_LOCK_MS): boolean {
  return now - lastActivityAt >= timeout;
}

export function isWithinUtf8ByteLimit(value: string, limit: number): boolean {
  return new TextEncoder().encode(value).byteLength <= limit;
}

// 与 Go 的 rune 计数和 UTF-8 字节上限保持一致，不按 UTF-16 代码单元计算。
export function validateNewPassword(_: unknown, value?: string): Promise<void> {
  if (!value) return Promise.resolve();
  if (Array.from(value).length < 15) return Promise.reject(new Error("密码至少 15 个字符"));
  if (!isWithinUtf8ByteLimit(value, LOGIN_FIELD_LIMITS.password)) {
    return Promise.reject(new Error("密码过长，请缩短后重试"));
  }
  return Promise.resolve();
}

// 单向清理旧版本保存的可逆登录凭据。
// 这里有意忽略清理失败，避免浏览器的限制策略
// 阻止应用进入登录页面。
export function clearLegacyRememberedLogin(): void {
  try {
    window.localStorage.removeItem(LEGACY_STORAGE_KEY);
  } catch {
    // 浏览器策略可能禁用存储。
  }
  try {
    window.indexedDB?.deleteDatabase(LEGACY_DATABASE_NAME);
  } catch {
    // IndexedDB 可能不可用或被浏览器策略禁用。
  }
}

export function clearPublicComputerData(tenantId: string, userId: string): void {
  if (typeof window === "undefined") return;
  const scope = `:${tenantId}:${userId}`;
  try {
    const keys: string[] = [];
    for (let index = 0; index < window.localStorage.length; index += 1) {
      const key = window.localStorage.key(index);
      if (key?.includes(scope)) keys.push(key);
    }
    for (const key of keys) window.localStorage.removeItem(key);
  } catch {
    // 浏览器策略可能禁用存储；内存缓存仍会清除。
  }
}
