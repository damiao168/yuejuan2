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
