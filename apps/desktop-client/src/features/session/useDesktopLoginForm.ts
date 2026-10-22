import { useCallback, useState } from "react";
import { normalizeBaseUrl } from "../../api/client";

// 地址草稿可以暂时无效；网络客户端只使用确认后的地址，账号字段不承担会话身份。
export function useDesktopLoginForm(defaultServer: string) {
  const [serverUrl, setServerUrl] = useState(() => {
    // 地址记忆只是可选便利；storage getter或读取失败时仍允许使用部署默认地址登录。
    try { return window.sessionStorage.getItem("edugrade.desktop.server_url") ?? defaultServer; }
    catch { return defaultServer; }
  });
  const [confirmedServerUrl, setConfirmedServerUrl] = useState(() => {
    try { return normalizeBaseUrl(window.sessionStorage.getItem("edugrade.desktop.server_url") ?? defaultServer); }
    catch { return normalizeBaseUrl(defaultServer); }
  });
  const [tenantCode, setTenantCode] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const confirmServerUrl = useCallback((value: string) => {
    const normalized = normalizeBaseUrl(value);
    setConfirmedServerUrl(normalized); setServerUrl(normalized);
  }, []);

  return { serverUrl, setServerUrl, confirmedServerUrl, setConfirmedServerUrl, tenantCode, setTenantCode,
    username, setUsername, password, setPassword, confirmServerUrl };
}
