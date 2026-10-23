import { useCallback, useEffect, useRef, useState } from "react";
import { getCaptureCollectionSummary, type CaptureCollectionSummary } from "../../api/captureCollection";
import { getUserErrorMessage } from "../../api/client";

export function useCaptureCollection(examId: string, refreshKey: string) {
  const [summary, setSummary] = useState<CaptureCollectionSummary>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const scope = useRef(examId);
  const request = useRef(0);
  if (scope.current !== examId) { scope.current = examId; request.current += 1; }

  const load = useCallback(async () => {
    const sequence = ++request.current;
    setLoading(true);
    try {
      const result = await getCaptureCollectionSummary(examId);
      if (sequence !== request.current || scope.current !== examId) return;
      if (result.summary.exam_id !== examId) throw new Error("返回的导入核对信息不属于当前考试，请重试");
      setSummary(result.summary);
      setError(undefined);
    } catch (cause) {
      if (sequence === request.current && scope.current === examId) setError(getUserErrorMessage(cause, "导入核对信息读取失败，请重试"));
    } finally {
      if (sequence === request.current && scope.current === examId) setLoading(false);
    }
  }, [examId]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 批次修订号或本页写操作变化后重读全场事实，不能只等轮询。
  useEffect(() => {
    void load();
    return () => { request.current += 1; };
  }, [load, refreshKey]);

  const current = summary?.exam_id === examId ? summary : undefined;
  const processing = current?.entries.some(entry => entry.status === "processing") || current?.issues.some(issue => issue.kind === "upload" || ["file_processing", "task_processing"].includes(issue.code));
  useEffect(() => {
    if (!processing) return;
    const timer = window.setInterval(() => void load(), 3000);
    return () => window.clearInterval(timer);
  }, [load, processing]);

  const acceptSummary = useCallback((value: CaptureCollectionSummary) => {
    if (scope.current !== examId || value.exam_id !== examId) return;
    // 完成响应是新的领域事实；此前发出的轮询不能再把旧的 collecting 状态回填。
    request.current += 1;
    setSummary(value);
    setLoading(false);
    setError(undefined);
  }, [examId]);

  return { summary: current, loading, error, load, acceptSummary };
}
