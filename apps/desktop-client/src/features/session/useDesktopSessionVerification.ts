import { useCallback, type Dispatch, type RefObject, type SetStateAction } from "react";
import { getCurrentUser } from "../../api/auth";
import type { DesktopApiClient } from "../../api/client";
import { getUserErrorMessage } from "../../api/userError";
import { clearDurableSession } from "../../lib/durableStore";
import type { AuthUser, LocalLogEntry } from "../../types";

interface VerificationOptions {
  client: DesktopApiClient;
  token: string | null;
  user: AuthUser | null;
  loginAttemptRef: RefObject<number>;
  setAuthError: Dispatch<SetStateAction<string | null>>;
  setToken: Dispatch<SetStateAction<string | null>>;
  setUser: Dispatch<SetStateAction<AuthUser | null>>;
  setDurableSessionKey: Dispatch<SetStateAction<string | null>>;
  setAuthenticatedServerUrl: Dispatch<SetStateAction<string | null>>;
  logEvent: (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;
}

// 在线身份核对沿用登录代次；身份变化先锁定本地数据，再清空展示身份。
export function useDesktopSessionVerification({ client, token, user, loginAttemptRef, setAuthError, setToken,
  setUser, setDurableSessionKey, setAuthenticatedServerUrl, logEvent }: VerificationOptions) {
  const checkSession = useCallback(async () => {
    if (!token) { setAuthError("本机解锁不包含服务端登录权限，请联网登录后校验。"); return; }
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
  }, [client, logEvent, token, user, loginAttemptRef, setAuthError, setAuthenticatedServerUrl, setDurableSessionKey, setToken, setUser]);

  return checkSession;
}
