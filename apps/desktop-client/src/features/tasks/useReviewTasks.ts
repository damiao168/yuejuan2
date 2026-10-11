import { useEffect, useRef, useState } from "react";
import type { DesktopApiClient } from "../../api/client";
import { listReviewTasks } from "../../api/review";
import { listExams } from "../../api/exams";
import { getUserErrorMessage } from "../../api/userError";
import type { LocalLogEntry, ReviewTask } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useReviewTasks(client: DesktopApiClient, durableScopeKey: string, logEvent: LogEvent, actorId?: string) {
  const [tasks, setTasks] = useState<ReviewTask[]>([]);
  const [taskError, setTaskError] = useState<string | null>(null);
  const [isLoadingTasks, setIsLoadingTasks] = useState(false);
  const [examNames, setExamNames] = useState<Record<string, string>>({});
  const scopeRef = useRef(durableScopeKey);
  // 请求使用发起时的账号；回填时再检查当前账号，避免切换登录后短暂显示旧任务。
  scopeRef.current = durableScopeKey;

  // 切换登录账号时必须清空上一账号的任务；durableScopeKey 只作为重置触发键。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 这里故意在作用域变化时清理任务。
  useEffect(() => {
    setTasks([]);
    setTaskError(null);
    setIsLoadingTasks(false);
    setExamNames({});
  }, [durableScopeKey]);

  const handleLoadTasks = async () => {
    const scope = durableScopeKey;
    if (!scope) return;
    setTaskError(null);
    setIsLoadingTasks(true);
    try {
      const [taskResult, examResult] = await Promise.allSettled([listReviewTasks(client, actorId ? { assigned_to: actorId } : {}), listExams(client)]);
      if (taskResult.status === "rejected") throw taskResult.reason;
      const result = taskResult.value;
      if (scopeRef.current !== scope) return;
      setTasks(result.tasks);
      setExamNames(examResult.status === "fulfilled" ? Object.fromEntries(examResult.value.exams.map((exam) => [exam.id, exam.name])) : {});
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

  return { tasks, examNames, taskError, isLoadingTasks, handleLoadTasks };
}
