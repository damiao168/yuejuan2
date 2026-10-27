import { useCallback, useEffect, useRef, useState } from "react";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { createReviewAnnotation, deleteReviewAnnotation, listReviewAnnotations, type CreateReviewAnnotationRequest, type ReviewAnnotation } from "../../../api/reviewAnnotations";

export type AnnotationSaveState = "idle" | "saving" | "saved" | "conflict" | "error";

export function useReviewAnnotations(taskId: string) {
  const [annotations, setAnnotations] = useState<ReviewAnnotation[]>([]);
  const [loading, setLoading] = useState(true);
  const [saveState, setSaveState] = useState<AnnotationSaveState>("idle");
  const [error, setError] = useState<string>();
  const scope = useRef({ taskId, active: true });
  scope.current.taskId = taskId;
  const isCurrent = useCallback(() => scope.current.active && scope.current.taskId === taskId, [taskId]);
  const reloadAnnotations = useCallback(async (signal?: AbortSignal) => {
    const response = await listReviewAnnotations(taskId, signal);
    if (isCurrent() && !signal?.aborted) setAnnotations(response.annotations);
    return response.annotations;
  }, [taskId, isCurrent]);
  useEffect(() => {
    const controller = new AbortController();
    scope.current.active = true;
    setAnnotations([]); setLoading(true); setError(undefined); setSaveState("idle");
    void reloadAnnotations(controller.signal).catch(cause => {
      if (isCurrent() && !controller.signal.aborted) setError(getUserErrorMessage(cause, "批注读取失败"));
    }).finally(() => { if (isCurrent() && !controller.signal.aborted) setLoading(false); });
    return () => { scope.current.active = false; controller.abort(); };
  }, [reloadAnnotations, isCurrent]);

  const writeError = useCallback(async (cause: unknown) => {
    if (!isCurrent()) return;
    if (cause instanceof ApiClientError && cause.status === 409) {
      setSaveState("conflict"); setError("批注已更新，请重新选择后操作");
      try { await reloadAnnotations(); } catch { /* 保留原冲突说明，不能自动重试覆盖新版本。 */ }
    } else {
      setSaveState("error"); setError(getUserErrorMessage(cause, "批注保存失败，请重试"));
    }
  }, [isCurrent, reloadAnnotations]);
  const saveAnnotation = useCallback(async (input: CreateReviewAnnotationRequest) => {
    if (isCurrent()) { setSaveState("saving"); setError(undefined); }
    try {
      const response = await createReviewAnnotation(taskId, input);
      // 请求仍写入原答卷；迟到回执不能把原答卷标记带到当前答卷。
      if (isCurrent()) { setAnnotations(items => [...items.filter(item => item.id !== response.annotation.id), response.annotation]); setSaveState("saved"); }
      return response.annotation;
    } catch (cause) { await writeError(cause); throw cause; }
  }, [taskId, isCurrent, writeError]);
  const removeAnnotation = useCallback(async (annotation: ReviewAnnotation) => {
    if (isCurrent()) { setSaveState("saving"); setError(undefined); }
    try {
      await deleteReviewAnnotation(annotation.id, annotation.revision);
      if (isCurrent()) { setAnnotations(items => items.filter(item => item.id !== annotation.id)); setSaveState("saved"); }
    } catch (cause) { await writeError(cause); throw cause; }
  }, [isCurrent, writeError]);
  return { annotations, loading, saveState, error, saveAnnotation, removeAnnotation };
}
