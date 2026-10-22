import { useDesktopSessionVerification } from "./useDesktopSessionVerification";
import { useDesktopLoginForm } from "./useDesktopLoginForm";
import { useDesktopCredentialStore, loginCredentialPersistence, type CredentialPersistence } from "./useDesktopCredentialStore";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { login, logout } from "../../api/auth";
import { DesktopApiClient, normalizeBaseUrl } from "../../api/client";
import { getUserErrorMessage } from "../../api/userError";
import { authorizeOfflineAccess, bindDurableSession, clearDurableSession, readOfflineAccess, revokeOfflineAccess, unlockOfflineAccess, type OfflineAccess } from "../../lib/durableStore";
import {
  deleteStoredCredentials,
  isTauriRuntime,
  loadStoredCredentials,
  type StoredDesktopCredentials
} from "../../lib/localRuntime";
import type { AuthUser, LocalLogEntry, LoginResult } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;
function logoutErrorMessage(clearError: string | null, revokeError: string | null): string | null {
  // 本机清理失败优先显示；退出流程仍用原有日志记录未完整完成。
  if (clearError) return "本地保存的会话清理失败，请检查本机凭据存储。";
  if (revokeError) return "本机已退出，但服务端会话吊销未确认；请在账户安全页退出所有设备。";
  return null;
}

export function useDesktopSession(defaultServer: string, logEvent: LogEvent) {
  const { serverUrl, setServerUrl, confirmedServerUrl, setConfirmedServerUrl, tenantCode, setTenantCode,
    username, setUsername, password, setPassword, confirmServerUrl } = useDesktopLoginForm(defaultServer);
  const { rememberLogin, setRememberLogin, credentialStoreMessage, setCredentialStoreMessage,
    credentialStoreReady, setCredentialStoreReady, persistLoginCredentials } = useDesktopCredentialStore();
  const [token, setToken] = useState<string | null>(null);
  const [durableSessionKey, setDurableSessionKey] = useState<string | null>(null);
  const [authenticatedServerUrl, setAuthenticatedServerUrl] = useState<string | null>(null);
  const [expiresAt, setExpiresAt] = useState<string | null>(null);
  const [user, setUser] = useState<AuthUser | null>(null);
  const [authError, setAuthError] = useState<string | null>(null);
  const [isLoggingIn, setIsLoggingIn] = useState(false);
  const [isLoggingOut, setIsLoggingOut] = useState(false);
  const [enableOfflineAccess, setEnableOfflineAccess] = useState(false);
  const [offlineAccess, setOfflineAccess] = useState<OfflineAccess | null>(null);
  const [offlineAccessMessage, setOfflineAccessMessage] = useState<string | null>(null);
  const [offlineUnlocked, setOfflineUnlocked] = useState(false);
  const autoLoginStartedRef = useRef(false);
  const loginAttemptRef = useRef(0);
  // 退出也会递增登录序号，让退出前尚未完成的登录和身份检查结果失效。
  const logoutInProgressRef = useRef(false);
  const renewalInProgressRef = useRef(false);
  const renewalIdentityRef = useRef({ server: authenticatedServerUrl, tenantId: user?.tenant_id, actorId: user?.id, lease: durableSessionKey });
  renewalIdentityRef.current = { server: authenticatedServerUrl, tenantId: user?.tenant_id, actorId: user?.id, lease: durableSessionKey };

  // 地址输入允许暂时为空或不完整；只有显式校验通过的地址才能改变网络客户端，避免渲染时抛错。
  const client = useMemo(() => new DesktopApiClient({ baseUrl: authenticatedServerUrl ?? confirmedServerUrl, getToken: () => token }), [authenticatedServerUrl, confirmedServerUrl, token]);
  const syncLoginOfflineAccess = useCallback(async (
    credentials: StoredDesktopCredentials,
    result: LoginResult,
    sessionKey: string,
    allowOffline: boolean,
    isCurrent: () => boolean
  ) => {
    if (!isTauriRuntime()) return true;
    try {
      // 离线授权归属刚绑定的账号租约；保存失败只提示，保留已经建立的在线会话。
      // 密码自动登录与本机离线解密是两个独立选择，未启用离线解锁时撤销旧授权。
      if (allowOffline) {
        const access = await authorizeOfflineAccess({ server: credentials.server_url, tenantId: result.user.tenant_id, actorId: result.user.id,
          tenantCode: result.user.tenant_code, username: result.user.username, displayName: result.user.display_name,
          authorizedAt: new Date().toISOString(), expiresAt: result.expires_at }, sessionKey);
        if (!isCurrent()) return false;
        setOfflineAccess(access);
        setOfflineAccessMessage(`已允许本机离线解锁至 ${new Date(access.expiresAt).toLocaleString("zh-CN")}。授权只保护本机草稿，上传和获取任务仍须在线登录。`);
      } else {
        await revokeOfflineAccess(sessionKey);
        if (!isCurrent()) return false;
        setOfflineAccess(null);
        setOfflineAccessMessage("本次未启用跨重启离线解锁；可勾选后重新登录建立授权。");
      }
    } catch (error) {
      // 退出或另一次登录会使本次授权结果失效，迟到的成功与失败都不更新新账号提示。
      if (!isCurrent()) return false;
      setOfflineAccessMessage(getUserErrorMessage(error, "本机离线授权保存失败；重启后需联网登录。"));
    }
    return true;
  }, []);

  const performLogin = useCallback(async (
    credentials: StoredDesktopCredentials,
    persistence: CredentialPersistence,
    allowOffline = enableOfflineAccess
  ) => {
    try { credentials = { ...credentials, server_url: normalizeBaseUrl(credentials.server_url) }; }
    catch (error) { setAuthError(getUserErrorMessage(error, "学校服务器地址无效，请修改后重试。")); return false; }
    const attempt = ++loginAttemptRef.current;
    setAuthError(null);
    setIsLoggingIn(true);
    setOfflineUnlocked(false);
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
      // 在线认证与本机账号绑定都完成后才发布身份，旧登录的结果不能安装到新账号。
      const sessionKey = await bindDurableSession(credentials.server_url, result.user.tenant_id, result.user.id);
      if (attempt !== loginAttemptRef.current) return false;
      setServerUrl(credentials.server_url);
      setConfirmedServerUrl(credentials.server_url);
      setAuthenticatedServerUrl(credentials.server_url);
      setTenantCode(credentials.tenant_code.trim());
      setUsername(credentials.username.trim());
      setToken(result.access_token);
      setDurableSessionKey(sessionKey);
      setExpiresAt(result.expires_at);
      setUser(result.user);
      if (!await syncLoginOfflineAccess(credentials, result, sessionKey, allowOffline, () => attempt === loginAttemptRef.current)) return false;
      await persistLoginCredentials(credentials, persistence);
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
  }, [enableOfflineAccess, logEvent, persistLoginCredentials, syncLoginOfflineAccess, setConfirmedServerUrl, setPassword, setServerUrl, setTenantCode, setUsername]);

  const handleLogin = useCallback(() => logoutInProgressRef.current ? Promise.resolve(false) : performLogin(
    { server_url: serverUrl, tenant_code: tenantCode, username, password },
    loginCredentialPersistence(rememberLogin, credentialStoreReady)
  ), [credentialStoreReady, password, performLogin, rememberLogin, serverUrl, tenantCode, username]);

  useEffect(() => {
    if (autoLoginStartedRef.current) return;
    autoLoginStartedRef.current = true;
    if (!isTauriRuntime()) {
      setCredentialStoreReady(false);
      setCredentialStoreMessage("浏览器开发模式不保存密码；请使用 Windows 桌面客户端测试自动登录。");
      return;
    }
    const initialAttempt = loginAttemptRef.current;
    void Promise.all([loadStoredCredentials(), readOfflineAccess().catch((error) => {
      setOfflineAccessMessage(getUserErrorMessage(error, "本机离线授权读取失败，请联网登录。")); return null;
    })])
      .then(async ([stored, access]) => {
        // 启动读取可能慢于教师手动登录；迟到的旧凭据不能把新账号改回启动时保存的账号。
        if (initialAttempt !== loginAttemptRef.current) return;
        setOfflineAccess(access);
        setEnableOfflineAccess(Boolean(access));
        if (access) setOfflineAccessMessage(`本机可解锁 ${access.username}（${access.tenantCode}）的已保存草稿，有效至 ${new Date(access.expiresAt).toLocaleString("zh-CN")}。`);
        setCredentialStoreReady(true);
        if (!stored) {
          setCredentialStoreMessage("Windows 系统凭据库可用，当前没有保存的登录信息。");
          return;
        }
        setServerUrl(stored.server_url);
        setTenantCode(stored.tenant_code);
        setUsername(stored.username);
        setRememberLogin(true);
        if (!navigator.onLine) {
          setCredentialStoreMessage("当前离线，自动登录已暂停。可使用有效本机授权解锁已保存草稿。"); return;
        }
        setCredentialStoreMessage("已从 Windows 系统凭据库读取登录信息，正在自动登录。");
        if (await performLogin(stored, "none", Boolean(access))) {
          setCredentialStoreMessage("已使用 Windows 系统凭据库自动登录。");
        }
      })
      .catch((error) => {
        if (initialAttempt !== loginAttemptRef.current) return;
        setCredentialStoreReady(false);
        setCredentialStoreMessage(getUserErrorMessage(error, "Windows 系统凭据库不可用；已停止自动登录。"));
      });
  }, [performLogin, setCredentialStoreMessage, setCredentialStoreReady, setRememberLogin, setServerUrl, setTenantCode, setUsername]);

  const handleUnlockOffline = useCallback(async () => {
    if (!offlineAccess || isLoggingIn || logoutInProgressRef.current) return false;
    const attempt = ++loginAttemptRef.current;
    setAuthError(null); setIsLoggingIn(true);
    try {
      const result = await unlockOfflineAccess(offlineAccess);
      if (attempt !== loginAttemptRef.current) return false;
      const access = result.access;
      setServerUrl(access.server); setAuthenticatedServerUrl(access.server);
      setTenantCode(access.tenantCode); setUsername(access.username);
      // 本机身份只用于账号隔离，不携带服务端权限或 bearer；网络操作仍由 token 门禁拒绝。
      setToken(null); setExpiresAt(access.expiresAt); setDurableSessionKey(result.sessionId); setOfflineUnlocked(true);
      setUser({ id: access.actorId, tenant_id: access.tenantId, tenant_code: access.tenantCode, username: access.username, display_name: access.displayName,
        status: "offline_local", roles: [], permissions: [], data_scope: {} });
      setOfflineAccessMessage("本机草稿已解锁。可进入离线阅卷加载已保存草稿；联网登录后才能恢复图片、获取任务或同步提交。");
      return true;
    } catch (error) {
      if (attempt !== loginAttemptRef.current) return false;
      setAuthError(getUserErrorMessage(error, "本机解锁失败，请联网登录后续期。")); return false;
    } finally { if (attempt === loginAttemptRef.current) setIsLoggingIn(false); }
  }, [isLoggingIn, offlineAccess, setServerUrl, setTenantCode, setUsername]);

  useEffect(() => {
    if (!offlineUnlocked || !offlineAccess) return;
    const timeout = window.setTimeout(() => {
      // 原生租约自行拒绝过期读写；编辑器保留未保存输入，不能因到期通知而卸载丢掉人工评分。
      setOfflineAccessMessage("本机离线授权已过期，本地读写已锁定，当前未保存输入仍保留；可在当前阅卷页联网续期本账号。");
    }, Math.max(0, new Date(offlineAccess.expiresAt).getTime() - Date.now()));
    return () => window.clearTimeout(timeout);
  }, [offlineAccess, offlineUnlocked]);

  const handleRenewOfflineAccess = useCallback(async (renewalPassword: string) => {
    if (renewalInProgressRef.current || isLoggingIn || logoutInProgressRef.current) return false;
    const identity = renewalIdentityRef.current;
    if (!offlineAccess || !identity.server || !identity.actorId || !identity.tenantId || !identity.lease
      || offlineAccess.server !== identity.server || offlineAccess.actorId !== identity.actorId || offlineAccess.tenantId !== identity.tenantId) {
      setAuthError("当前本机身份与离线授权不一致，不能在此续期；请保存或核对输入后重新登录。"); return false;
    }
    if (!navigator.onLine || !renewalPassword) { setAuthError("请联网并填写当前学校账号的密码，再在本页续期。"); return false; }
    const attempt = ++loginAttemptRef.current;
    renewalInProgressRef.current = true; setAuthError(null); setIsLoggingIn(true);
    const isCurrent = () => {
      const latest = renewalIdentityRef.current;
      return attempt === loginAttemptRef.current && latest.server === identity.server && latest.tenantId === identity.tenantId
        && latest.actorId === identity.actorId && latest.lease === identity.lease;
    };
    try {
      // 续期只能验证当前已解锁身份，失败前不清空账号或租约，避免卸载尚未保存的评分。
      const result = await login(new DesktopApiClient({ baseUrl: identity.server }), {
        tenant_code: offlineAccess.tenantCode, username: offlineAccess.username, password: renewalPassword
      });
      if (!isCurrent()) return false;
      if (result.user.id !== identity.actorId || result.user.tenant_id !== identity.tenantId) {
        void logout(new DesktopApiClient({ baseUrl: identity.server, getToken: () => result.access_token })).catch(() => undefined);
        throw new Error("续期返回的学校或账号与当前草稿不一致；当前输入保留，未切换本机身份。");
      }
      // 只有在线认证确认同一身份后才安装新租约；原生层仍拒绝旧租约的过期读写。
      const sessionKey = await bindDurableSession(identity.server, identity.tenantId, identity.actorId);
      if (!isCurrent()) return false;
      let access: OfflineAccess | null = null;
      let accessError: string | null = null;
      try {
        access = await authorizeOfflineAccess({ server: identity.server, tenantId: identity.tenantId, actorId: identity.actorId,
          tenantCode: result.user.tenant_code, username: result.user.username, displayName: result.user.display_name,
          authorizedAt: new Date().toISOString(), expiresAt: result.expires_at }, sessionKey);
      } catch (error) { accessError = getUserErrorMessage(error, "本机离线授权续期失败"); }
      if (!isCurrent()) return false;
      setToken(result.access_token); setDurableSessionKey(sessionKey); setExpiresAt(result.expires_at); setUser(result.user); setOfflineUnlocked(false);
      if (access) setOfflineAccess(access);
      setOfflineAccessMessage(accessError ? `已重新在线登录，可加密保存当前输入；${accessError}，重启离线恢复前请重试续期。`
        : `本账号已续期至 ${new Date(access!.expiresAt).toLocaleString("zh-CN")}；当前评分输入保留，请加密保存草稿。`);
      return true;
    } catch (error) {
      if (isCurrent()) setAuthError(getUserErrorMessage(error, "续期失败，当前输入保留；请核对网络和密码后重试。"));
      return false;
    } finally {
      renewalInProgressRef.current = false;
      if (attempt === loginAttemptRef.current) { setPassword(""); setIsLoggingIn(false); }
    }
  }, [isLoggingIn, offlineAccess, setPassword]);

  const forgetStoredLogin = useCallback(async () => {
    try {
      await deleteStoredCredentials();
      if (isTauriRuntime()) { await revokeOfflineAccess(); setOfflineAccess(null); }
      if (offlineUnlocked) {
        await clearDurableSession(); setOfflineUnlocked(false); setUser(null); setDurableSessionKey(null); setAuthenticatedServerUrl(null); setExpiresAt(null);
      }
      setRememberLogin(false);
      setCredentialStoreMessage("已从 Windows 系统凭据库清除保存的登录信息。");
    } catch (error) {
      setCredentialStoreMessage(getUserErrorMessage(error, "系统凭据清除失败。"));
    }
  }, [offlineUnlocked, setCredentialStoreMessage, setRememberLogin]);

  const handleLogout = useCallback(async () => {
    if (logoutInProgressRef.current) return;
    logoutInProgressRef.current = true;
    setIsLoggingOut(true);
    ++loginAttemptRef.current;
    const currentToken = token;
    const currentServerUrl = authenticatedServerUrl ?? serverUrl;
    let revokeError: string | null = null;
    let clearError: string | null = null;
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 5000);
    try {
      if (currentToken) {
        await logout(new DesktopApiClient({ baseUrl: currentServerUrl, getToken: () => currentToken }), controller.signal);
      }
    } catch (error) {
      revokeError = getUserErrorMessage(error, "服务端会话吊销未确认");
    } finally {
      window.clearTimeout(timeout);
      // 服务端吊销超时也必须清空本机身份；吊销是否成功单独提示，不能阻止用户退出本机。
      setToken(null);
      setOfflineUnlocked(false);
      setUser(null);
      setDurableSessionKey(null);
      setAuthenticatedServerUrl(null);
      setExpiresAt(null);
      setIsLoggingIn(false);
      try {
        await clearDurableSession();
      } catch (error) {
        clearError = getUserErrorMessage(error, "本地会话清理失败");
      }
      // 会话清理失败也继续撤销跨重启授权，避免下一次启动仍能解锁刚退出的账号。
      if (isTauriRuntime()) {
        try { await revokeOfflineAccess(); setOfflineAccess(null); }
        catch (error) { clearError = getUserErrorMessage(error, "本机离线授权撤销失败"); }
      }
      setAuthError(logoutErrorMessage(clearError, revokeError));
      await logEvent(revokeError || clearError ? "warning" : "info", revokeError || clearError ? "session logout incomplete" : "session logged out", clearError ?? revokeError ?? undefined).catch(() => undefined);
      setIsLoggingOut(false);
      logoutInProgressRef.current = false;
    }
  }, [authenticatedServerUrl, logEvent, serverUrl, token]);

  const checkSession = useDesktopSessionVerification({ client, token, user, loginAttemptRef, setAuthError,
    setToken, setUser, setDurableSessionKey, setAuthenticatedServerUrl, logEvent });

  return {
    client, serverUrl, setServerUrl, confirmServerUrl, tenantCode, setTenantCode, username, setUsername,
    password, setPassword, rememberLogin, setRememberLogin, credentialStoreMessage,
    credentialStoreReady, token, expiresAt, user, authError, isLoggingIn, isLoggingOut,
    enableOfflineAccess, setEnableOfflineAccess, offlineAccess, offlineAccessMessage, offlineUnlocked, handleUnlockOffline,
    handleRenewOfflineAccess,
    // 编辑器按已认证账号挂载，租约续期只替换读写凭证；换账号仍会创建新的编辑器。
    editorAccountKey: user && authenticatedServerUrl ? JSON.stringify([authenticatedServerUrl, user.tenant_id, user.id]) : "",
    durableScopeKey: (token || offlineUnlocked) && user && authenticatedServerUrl ? durableSessionKey ?? "" : "",
    handleLogin, handleLogout, handleForgetStoredLogin: forgetStoredLogin, handleCheckSession: checkSession
  };
}
