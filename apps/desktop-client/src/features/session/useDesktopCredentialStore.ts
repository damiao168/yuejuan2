import { useCallback, useState } from "react";
import { deleteStoredCredentials, saveStoredCredentials, type StoredDesktopCredentials } from "../../lib/localRuntime";
import { getUserErrorMessage } from "../../api/userError";

export type CredentialPersistence = "save" | "delete" | "none";

export function loginCredentialPersistence(rememberLogin: boolean, credentialStoreReady: boolean): CredentialPersistence {
  // 选择保存优先于凭据库就绪标记；未选择保存时，仅凭据库可用才请求删除。
  if (rememberLogin) return "save";
  if (credentialStoreReady) return "delete";
  return "none";
}

// 系统凭据持久化独立于在线会话；写入失败只反馈本机保存状态，不撤销已认证身份。
export function useDesktopCredentialStore() {
  const [rememberLogin, setRememberLogin] = useState(false);
  const [credentialStoreMessage, setCredentialStoreMessage] = useState<string | null>(null);
  const [credentialStoreReady, setCredentialStoreReady] = useState(false);
  const persistLoginCredentials = useCallback(async (credentials: StoredDesktopCredentials, persistence: CredentialPersistence) => {
    // 系统凭据保存或删除失败不回退到其他存储，也不撤销已经成功的在线登录。
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
  }, []);

  return { rememberLogin, setRememberLogin, credentialStoreMessage, setCredentialStoreMessage,
    credentialStoreReady, setCredentialStoreReady, persistLoginCredentials };
}
