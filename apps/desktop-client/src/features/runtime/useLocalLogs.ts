import { useCallback, useState } from "react";
import { appendLocalLog, clearLocalLogs, readLocalLogs } from "../../lib/localRuntime";
import type { LocalLogEntry } from "../../types";

export function useLocalLogs() {
  const [logs, setLogs] = useState<LocalLogEntry[]>(() => readLocalLogs());
  const refreshLogs = useCallback(() => setLogs(readLocalLogs()), []);
  const logEvent = useCallback(async (level: LocalLogEntry["level"], message: string, context?: string) => {
    try {
      await appendLocalLog({ level, message, context });
    } catch (error) {
      // 日志是附加信息，写入失败不能把已完成的上传或登录变成业务失败。
      console.warn("local log write failed", error);
    } finally {
      refreshLogs();
    }
  }, [refreshLogs]);
  const clearLogs = useCallback(async () => {
    await clearLocalLogs();
    refreshLogs();
  }, [refreshLogs]);

  return { logs, logEvent, clearLogs };
}
