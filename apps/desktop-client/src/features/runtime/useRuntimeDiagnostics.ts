import { useCallback, useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { normalizeBaseUrl, DesktopApiClient } from "../../api/client";
import { getUserErrorMessage } from "../../api/userError";
import { getCapabilityStatuses, getRuntimeDiagnostics, scanLocalCacheSecurity } from "../../lib/localRuntime";
import type { CapabilityProbe, LocalCacheSecurityStatus, LocalLogEntry, RuntimeDiagnostics, SystemStatus } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useRuntimeDiagnostics(
  client: DesktopApiClient,
  serverUrl: string,
  durableScopeKey: string,
  setServerUrl: Dispatch<SetStateAction<string>>,
  logEvent: LogEvent,
  confirmServerUrl?: (value: string) => void,
  networkAllowed = true
) {
  const [capabilities, setCapabilities] = useState<CapabilityProbe[]>([]);
  const [diagnostics, setDiagnostics] = useState<RuntimeDiagnostics | null>(null);
  const [localCacheSecurity, setLocalCacheSecurity] = useState<LocalCacheSecurityStatus>(() => scanLocalCacheSecurity());
  const [serviceStatus, setServiceStatus] = useState<SystemStatus | null>(null);
  const [isCheckingServiceStatus, setIsCheckingServiceStatus] = useState(false);
  const [diagnosticError, setDiagnosticError] = useState<string | null>(null);
  const [healthResult, setHealthResult] = useState<{ ok: boolean; server: string; elapsedMs: number; message: string } | null>(null);
  const [isCheckingHealth, setIsCheckingHealth] = useState(false);
  const healthRequest = useRef(0);
  const healthBusy = useRef(false);
  const currentContext = useRef({ serverUrl, durableScopeKey });
  currentContext.current = { serverUrl, durableScopeKey };
  useEffect(() => {
    healthRequest.current += 1; healthBusy.current = false; setIsCheckingHealth(false); setHealthResult(null);
    // 地址、账号或页面已变化时，迟到的连接结果不能回填到新表单。
    return () => { healthRequest.current += 1; healthBusy.current = false; };
  }, [serverUrl, durableScopeKey]);

  const refreshCapabilities = useCallback(async () => {
    const [nextCapabilities, nextDiagnostics] = await Promise.all([getCapabilityStatuses(durableScopeKey), getRuntimeDiagnostics()]);
    setCapabilities(nextCapabilities);
    setDiagnostics(nextDiagnostics);
    setLocalCacheSecurity(scanLocalCacheSecurity());
  }, [durableScopeKey]);

  useEffect(() => {
    void refreshCapabilities();
    void logEvent("info", "desktop client shell loaded", "STORY-034");
  }, [logEvent, refreshCapabilities]);

  const saveServerForSession = useCallback(async () => {
    setDiagnosticError(null);
    try {
      const normalizedServerUrl = normalizeBaseUrl(serverUrl);
      confirmServerUrl?.(normalizedServerUrl);
      setServerUrl(normalizedServerUrl);
      // 此处只保存当前浏览器会话的地址；长期登录凭据由系统凭据库单独管理。
      window.sessionStorage.setItem("edugrade.desktop.server_url", normalizedServerUrl);
      await logEvent("info", "server url saved for current session", normalizedServerUrl);
    } catch (error) {
      setDiagnosticError(getUserErrorMessage(error, "服务器地址无效"));
    }
  }, [confirmServerUrl, logEvent, serverUrl, setServerUrl]);

  const handleHealthCheck = useCallback(async () => {
    if (!networkAllowed || !navigator.onLine) { setDiagnosticError("当前为离线本机模式；联网并在线登录后可检查服务器。"); return; }
    if (healthBusy.current) return;
    const context = currentContext.current;
    const requestId = ++healthRequest.current;
    const started = performance.now();
    healthBusy.current = true; setIsCheckingHealth(true); setHealthResult(null);
    setDiagnosticError(null);
    try {
      // 检查当前输入地址的公开健康端点；不向尚未确认的新服务器发送原账号令牌。
      const server = normalizeBaseUrl(serverUrl);
      await new DesktopApiClient({ baseUrl: server }).health();
      if (requestId !== healthRequest.current || currentContext.current.serverUrl !== context.serverUrl || currentContext.current.durableScopeKey !== context.durableScopeKey) return;
      setHealthResult({ ok: true, server, elapsedMs: Math.round(performance.now() - started), message: "连接成功，可以继续填写学校账号登录。" });
      await logEvent("info", "server health checked", server);
    } catch (error) {
      if (requestId !== healthRequest.current || currentContext.current.serverUrl !== context.serverUrl || currentContext.current.durableScopeKey !== context.durableScopeKey) return;
      const message = getUserErrorMessage(error, "连接失败，请核对学校服务器地址和网络后重试。");
      setDiagnosticError(message);
      setHealthResult({ ok: false, server: serverUrl, elapsedMs: Math.round(performance.now() - started), message });
      await logEvent("warning", "server health check failed", message);
    } finally {
      if (requestId === healthRequest.current) { healthBusy.current = false; setIsCheckingHealth(false); }
    }
  }, [serverUrl, logEvent, networkAllowed]);

  const handleSystemStatusCheck = useCallback(async () => {
    if (!networkAllowed || !navigator.onLine) { setDiagnosticError("当前为离线本机模式，不能读取服务器状态。"); return; }
    setDiagnosticError(null);
    setIsCheckingServiceStatus(true);
    try {
      const result = await client.systemStatus();
      setServiceStatus(result);
      await logEvent("info", "server system status checked", result.status);
    } catch (error) {
      const message = getUserErrorMessage(error, "系统状态检查失败");
      setDiagnosticError(message);
      await logEvent("warning", "server system status check failed", message);
    } finally {
      setIsCheckingServiceStatus(false);
    }
  }, [client, logEvent, networkAllowed]);

  return {
    capabilities,
    diagnostics,
    localCacheSecurity,
    serviceStatus,
    isCheckingServiceStatus,
    healthResult,
    isCheckingHealth,
    diagnosticError,
    setDiagnosticError,
    refreshCapabilities,
    saveServerForSession,
    handleHealthCheck,
    handleSystemStatusCheck
  };
}
