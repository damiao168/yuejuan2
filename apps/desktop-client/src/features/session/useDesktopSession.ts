import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getCurrentUser, login } from "../../api/auth";
import { DesktopApiClient } from "../../api/client";
import { getUserErrorMessage } from "../../api/userError";
import { bindDurableSession, clearDurableSession } from "../../lib/durableStore";
import {
  deleteStoredCredentials,
  isTauriRuntime,
  loadStoredCredentials,
  saveStoredCredentials,
  type StoredDesktopCredentials
} from "../../lib/localRuntime";
import type { AuthUser, LocalLogEntry } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useDesktopSession(defaultServer: string, logEvent: LogEvent) {
  const [serverUrl, setServerUrl] = useState(() => window.sessionStorage.getItem("edugrade.desktop.server_url") ?? defaultServer);
  const [tenantCode, setTenantCode] = useState("demo");
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [rememberLogin, setRememberLogin] = useState(false);
  const [credentialStoreMessage, setCredentialStoreMessage] = useState<string | null>(null);
  const [credentialStoreReady, setCredentialStoreReady] = useState(false);
  const [token, setToken] = useState<string | null>(null);
  const [durableSessionKey, setDurableSessionKey] = useState<string | null>(null);
  const [authenticatedServerUrl, setAuthenticatedServerUrl] = useState<string | null>(null);
  const [expiresAt, setExpiresAt] = useState<string | null>(null);
  const [user, setUser] = useState<AuthUser | null>(null);
  const [authError, setAuthError] = useState<string | null>(null);
  const [isLoggingIn, setIsLoggingIn] = useState(false);
  const autoLoginStartedRef = useRef(false);
  const loginAttemptRef = useRef(0);

  const client = useMemo(() => new DesktopApiClient({ baseUrl: authenticatedServerUrl ?? serverUrl, getToken: () => token }), [authenticatedServerUrl, serverUrl, token]);

  const performLogin = useCallback(async (
    credentials: StoredDesktopCredentials,
    persistence: "save" | "delete" | "none"
  ) => {
    const attempt = ++loginAttemptRef.current;
    setAuthError(null);
    setIsLoggingIn(true);
    setToken(null);
    setUser(null);
    setDurableSessionKey(null);
    setAuthenticatedServerUrl(null);
    try {
      await clearDurableSession();
      if (attempt !== loginAttemptRef.current) return false;
      const result = await login(new DesktopApiClient({ baseUrl: credentials.server_url }), {
        tenant_code: credentials.tenant_code.trim(),
        username: credentials.username.trim(),
        password: credentials.password
      });
      if (attempt !== loginAttemptRef.current) return false;
      const sessionKey = await bindDurableSession(credentials.server_url, result.user.tenant_id, result.user.id);
      if (attempt !== loginAttemptRef.current) return false;
      setServerUrl(credentials.server_url);
      setAuthenticatedServerUrl(credentials.server_url);
      setTenantCode(credentials.tenant_code.trim());
      setUsername(credentials.username.trim());
      setToken(result.access_token);
      setDurableSessionKey(sessionKey);
      setExpiresAt(result.expires_at);
      setUser(result.user);
      if (persistence === "save") {
        try {
          await saveStoredCredentials(credentials);
          setCredentialStoreReady(true);
          setCredentialStoreMessage("登录信息已保存到当前 Windows 用户的系统凭据库。");
        } catch (error) {
          setCredentialStoreReady(false);
          setCredentialStoreMessage(getUserErrorMessage(error, "系统凭据库保存失败；未写入其他本地存储。"));
        }
      } else if (persistence === "delete") {
        try {
          await deleteStoredCredentials();
          setCredentialStoreMessage("未保存登录信息，已有系统凭据已清除。");
        } catch (error) {
          setCredentialStoreMessage(getUserErrorMessage(error, "系统凭据清除失败。"));
        }
      }
      await logEvent("info", "login succeeded", `${result.user.username}@${result.user.tenant_code}`);
      return true;
    } catch (error) {
      if (attempt !== loginAttemptRef.current) return false;
      await clearDurableSession().catch(() => undefined);
      const message = getUserErrorMessage(error, "登录请求失败");
      setAuthError(message);
      await logEvent("error", "login failed", message);
      return false;
    } finally {
      if (attempt === loginAttemptRef.current) {
        setPassword("");
        setIsLoggingIn(false);
      }
    }
  }, [logEvent]);

  const handleLogin = useCallback(() => performLogin(
    { server_url: serverUrl, tenant_code: tenantCode, username, password },
    rememberLogin ? "save" : credentialStoreReady ? "delete" : "none"
  ), [credentialStoreReady, password, performLogin, rememberLogin, serverUrl, tenantCode, username]);

  useEffect(() => {
    if (autoLoginStartedRef.current) return;
    autoLoginStartedRef.current = true;
    if (!isTauriRuntime()) {
      setCredentialStoreReady(false);
      setCredentialStoreMessage("浏览器开发模式不保存密码；请使用 Windows 桌面客户端测试自动登录。");
      return;
    }
    void loadStoredCredentials()
      .then(async (stored) => {
        setCredentialStoreReady(true);
        if (!stored) {
          setCredentialStoreMessage("Windows 系统凭据库可用，当前没有保存的登录信息。");
          return;
        }
        setServerUrl(stored.server_url);
        setTenantCode(stored.tenant_code);
        setUsername(stored.username);
        setRememberLogin(true);
        setCredentialStoreMessage("已从 Windows 系统凭据库读取登录信息，正在自动登录。");
        if (await performLogin(stored, "none")) {
          setCredentialStoreMessage("已使用 Windows 系统凭据库自动登录。");
        }
      })
      .catch((error) => {
        setCredentialStoreReady(false);
        setCredentialStoreMessage(getUserErrorMessage(error, "Windows 系统凭据库不可用；已停止自动登录。"));
      });
  }, [performLogin]);

  const forgetStoredLogin = useCallback(async () => {
    try {
      await deleteStoredCredentials();
      setRememberLogin(false);
      setCredentialStoreMessage("已从 Windows 系统凭据库清除保存的登录信息。");
    } catch (error) {
      setCredentialStoreMessage(getUserErrorMessage(error, "系统凭据清除失败。"));
    }
  }, []);

  const handleLogout = useCallback(async () => {
    ++loginAttemptRef.current;
    setToken(null);
    setUser(null);
    setDurableSessionKey(null);
    setAuthenticatedServerUrl(null);
    setExpiresAt(null);
    setAuthError(null);
    setIsLoggingIn(false);
    await clearDurableSession();
    await logEvent("info", "session logged out");
  }, [logEvent]);

  const checkSession = useCallback(async () => {
    const attempt = loginAttemptRef.current;
    setAuthError(null);
    try {
      const result = await getCurrentUser(client);
      if (attempt !== loginAttemptRef.current) return;
      if (user && (result.user.id !== user.id || result.user.tenant_id !== user.tenant_id)) {
        await clearDurableSession();
        setToken(null);
        setUser(null);
        setDurableSessionKey(null);
        setAuthenticatedServerUrl(null);
        throw new Error("服务端身份已变化，本地账号数据已锁定；请重新登录。");
      }
      setUser(result.user);
      await logEvent("info", "session verified", result.user.username);
    } catch (error) {
      if (attempt !== loginAttemptRef.current) return;
      const message = getUserErrorMessage(error, "登录状态校验失败");
      setAuthError(message);
      await logEvent("warning", "session verification failed", message);
    }
  }, [client, logEvent, user]);

  return {
    client, serverUrl, setServerUrl, tenantCode, setTenantCode, username, setUsername,
    password, setPassword, rememberLogin, setRememberLogin, credentialStoreMessage,
    credentialStoreReady, token, expiresAt, user, authError, isLoggingIn,
    durableScopeKey: token && user && authenticatedServerUrl ? durableSessionKey ?? "" : "",
    handleLogin, handleLogout, handleForgetStoredLogin: forgetStoredLogin, handleCheckSession: checkSession
  };
}
