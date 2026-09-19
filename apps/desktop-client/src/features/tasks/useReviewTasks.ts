import { useState } from "react";
import type { DesktopApiClient } from "../../api/client";
import { listReviewTasks } from "../../api/review";
import { getUserErrorMessage } from "../../api/userError";
import type { LocalLogEntry, ReviewTask } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useReviewTasks(client: DesktopApiClient, logEvent: LogEvent) {
  const [tasks, setTasks] = useState<ReviewTask[]>([]);
  const [taskError, setTaskError] = useState<string | null>(null);
  const [isLoadingTasks, setIsLoadingTasks] = useState(false);

  const handleLoadTasks = async () => {
    setTaskError(null);
    setIsLoadingTasks(true);
    try {
      const result = await listReviewTasks(client, {});
      setTasks(result.tasks);
      await logEvent("info", "review tasks loaded", `${result.tasks.length} tasks`);
    } catch (error) {
      const message = getUserErrorMessage(error, "任务列表读取失败");
      setTaskError(message);
      setTasks([]);
      await logEvent("warning", "review task load failed", message);
    } finally {
      setIsLoadingTasks(false);
    }
  };

  return { tasks, taskError, isLoadingTasks, handleLoadTasks };
}
