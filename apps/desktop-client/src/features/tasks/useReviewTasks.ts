import { useEffect, useRef, useState } from "react";
import type { DesktopApiClient } from "../../api/client";
import { listReviewTasks } from "../../api/review";
import { getUserErrorMessage } from "../../api/userError";
import type { LocalLogEntry, ReviewTask } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useReviewTasks(client: DesktopApiClient, durableScopeKey: string, logEvent: LogEvent) {
  const [tasks, setTasks] = useState<ReviewTask[]>([]);
  const [taskError, setTaskError] = useState<string | null>(null);
  const [isLoadingTasks, setIsLoadingTasks] = useState(false);
  const scopeRef = useRef(durableScopeKey);
  // 请求使用发起时的账号；回填时再检查当前账号，避免切换登录后短暂显示旧任务。
  scopeRef.current = durableScopeKey;

  useEffect(() => {
    setTasks([]);
    setTaskError(null);
    setIsLoadingTasks(false);
  }, [durableScopeKey]);

  const handleLoadTasks = async () => {
    const scope = durableScopeKey;
    if (!scope) return;
    setTaskError(null);
    setIsLoadingTasks(true);
    try {
      const result = await listReviewTasks(client, {});
      if (scopeRef.current !== scope) return;
      setTasks(result.tasks);
      await logEvent("info", "review tasks loaded", `${result.tasks.length} tasks`);
    } catch (error) {
      if (scopeRef.current !== scope) return;
      const message = getUserErrorMessage(error, "任务列表读取失败");
      setTaskError(message);
      setTasks([]);
      await logEvent("warning", "review task load failed", message);
    } finally {
      if (scopeRef.current === scope) setIsLoadingTasks(false);
    }
  };

  return { tasks, taskError, isLoadingTasks, handleLoadTasks };
}
