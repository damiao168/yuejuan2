import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getCurrentUser, login } from "../../api/auth";
import { DesktopApiClient } from "../../api/client";
import { getUserErrorMessage } from "../../api/userError";
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
  const [expiresAt, setExpiresAt] = useState<string | null>(null);
  const [user, setUser] = useState<AuthUser | null>(null);
  const [authError, setAuthError] = useState<string | null>(null);
  const [isLoggingIn, setIsLoggingIn] = useState(false);
  const autoLoginStartedRef = useRef(false);

  const client = useMemo(() => new DesktopApiClient({ baseUrl: serverUrl, getToken: () => token }), [serverUrl, token]);

  const performLogin = useCallback(async (
    credentials: StoredDesktopCredentials,
    persistence: "save" | "delete" | "none"
  ) => {
    setAuthError(null);
    setIsLoggingIn(true);
    try {
      const result = await login(new DesktopApiClient({ baseUrl: credentials.server_url }), {
        tenant_code: credentials.tenant_code.trim(),
        username: credentials.username.trim(),
        password: credentials.password
      });
      setServerUrl(credentials.server_url);
      setTenantCode(credentials.tenant_code.trim());
      setUsername(credentials.username.trim());
      setToken(result.access_token);
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
      const message = getUserErrorMessage(error, "登录请求失败");
      setAuthError(message);
      await logEvent("error", "login failed", message);
      return false;
    } finally {
      setPassword("");
      setIsLoggingIn(false);
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

  const checkSession = useCallback(async () => {
    setAuthError(null);
    try {
      const result = await getCurrentUser(client);
      setUser(result.user);
      await logEvent("info", "session verified", result.user.username);
    } catch (error) {
      const message = getUserErrorMessage(error, "登录状态校验失败");
      setAuthError(message);
      await logEvent("warning", "session verification failed", message);
    }
  }, [client, logEvent]);

  return {
    client, serverUrl, setServerUrl, tenantCode, setTenantCode, username, setUsername,
    password, setPassword, rememberLogin, setRememberLogin, credentialStoreMessage,
    credentialStoreReady, token, expiresAt, user, authError, isLoggingIn,
    handleLogin, handleForgetStoredLogin: forgetStoredLogin, handleCheckSession: checkSession
  };
}
