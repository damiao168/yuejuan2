import { useCallback, useEffect, useState, type Dispatch, type SetStateAction } from "react";
import { normalizeBaseUrl, type DesktopApiClient } from "../../api/client";
import { getUserErrorMessage } from "../../api/userError";
import { getCapabilityStatuses, getRuntimeDiagnostics, scanLocalCacheSecurity } from "../../lib/localRuntime";
import type { CapabilityProbe, LocalCacheSecurityStatus, LocalLogEntry, RuntimeDiagnostics, SystemStatus } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useRuntimeDiagnostics(
  client: DesktopApiClient,
  serverUrl: string,
  durableScopeKey: string,
  setServerUrl: Dispatch<SetStateAction<string>>,
  logEvent: LogEvent
) {
  const [capabilities, setCapabilities] = useState<CapabilityProbe[]>([]);
  const [diagnostics, setDiagnostics] = useState<RuntimeDiagnostics | null>(null);
  const [localCacheSecurity, setLocalCacheSecurity] = useState<LocalCacheSecurityStatus>(() => scanLocalCacheSecurity());
  const [serviceStatus, setServiceStatus] = useState<SystemStatus | null>(null);
  const [isCheckingServiceStatus, setIsCheckingServiceStatus] = useState(false);
  const [diagnosticError, setDiagnosticError] = useState<string | null>(null);

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
      setServerUrl(normalizedServerUrl);
      // 此处只保存当前浏览器会话的地址；长期登录凭据由系统凭据库单独管理。
      window.sessionStorage.setItem("edugrade.desktop.server_url", normalizedServerUrl);
      await logEvent("info", "server url saved for current session", normalizedServerUrl);
    } catch (error) {
      setDiagnosticError(getUserErrorMessage(error, "服务器地址无效"));
    }
  }, [logEvent, serverUrl, setServerUrl]);

  const handleHealthCheck = useCallback(async () => {
    setDiagnosticError(null);
    try {
      const result = await client.health();
      await logEvent("info", "server health checked", JSON.stringify(result));
    } catch (error) {
      const message = getUserErrorMessage(error, "服务端健康检查失败");
      setDiagnosticError(message);
      await logEvent("warning", "server health check failed", message);
    }
  }, [client, logEvent]);

  const handleSystemStatusCheck = useCallback(async () => {
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
  }, [client, logEvent]);

  return {
    capabilities,
    diagnostics,
    localCacheSecurity,
    serviceStatus,
    isCheckingServiceStatus,
    diagnosticError,
    setDiagnosticError,
    refreshCapabilities,
    saveServerForSession,
    handleHealthCheck,
    handleSystemStatusCheck
  };
}
