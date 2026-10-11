import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { DesktopApiClient } from "../../api/client";
import { getSubmissionImageQuality, runSubmissionQualityCheck, type SubmissionImageQuality } from "../../api/submissions";
import { getUserErrorMessage } from "../../api/userError";
import type { LocalLogEntry, SubmissionQualityResult } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useSubmissionQuality({ client, token, scanSubmissionId, durableScopeKey, selectedExamId, captureBatchId, linkedSubmissionIds, logEvent }: {
  client: DesktopApiClient; token: string | null; scanSubmissionId: string; durableScopeKey: string;
  selectedExamId: string; captureBatchId: string; linkedSubmissionIds: readonly string[]; logEvent: LogEvent;
}) {
  const submissionId = scanSubmissionId.trim();
  const associated = linkedSubmissionIds.includes(submissionId);
  const context = useMemo(() => ({ client, token, scope: durableScopeKey, examId: selectedExamId, batchId: captureBatchId, submissionId, associated }),
    [client, token, durableScopeKey, selectedExamId, captureBatchId, submissionId, associated]);
  const currentContext = useRef(context);
  currentContext.current = context;
  const requestSequence = useRef(0);
  const checkingRequest = useRef<number | null>(null);
  const [checking, setChecking] = useState<typeof context | null>(null);
  const [result, setResult] = useState<{ owner: typeof context; value: SubmissionQualityResult } | null>(null);
  const [error, setError] = useState<{ owner: typeof context; message: string } | null>(null);
  const imageRequest = useRef(0);
  const [imageQuality, setImageQuality] = useState<{ owner: typeof context; value: SubmissionImageQuality } | null>(null);
  const [imageError, setImageError] = useState<{ owner: typeof context; message: string } | null>(null);
  const [readingImage, setReadingImage] = useState<typeof context | null>(null);
  // 结果属于账号、考试、批次和答卷的完整选择；A→B→A 也不能复用第一次 A 的响应。
  // biome-ignore lint/correctness/useExhaustiveDependencies: context 变化用于重置请求锁与结果，不能删除该触发依赖。
  useEffect(() => {
    requestSequence.current += 1; checkingRequest.current = null;
    setResult(null); setError(null); setChecking(null);
    return () => { requestSequence.current += 1; };
  }, [context]);
  const canRunQualityCheck = Boolean(context.token && context.scope && context.examId && context.batchId && context.submissionId && context.associated);
  const handleRefreshImageQuality = useCallback(async () => {
    const owner = context;
    if (currentContext.current !== owner || !owner.token || !owner.scope || !owner.examId || !owner.batchId || !owner.submissionId || !owner.associated) return;
    const request = ++imageRequest.current;
    setImageQuality(null); setImageError(null); setReadingImage(owner);
    const isCurrent = () => currentContext.current === owner && imageRequest.current === request;
    try {
      const response = await getSubmissionImageQuality(owner.client, owner.submissionId);
      if (!isCurrent()) return;
      if (response.submission.id !== owner.submissionId || response.submission.exam_id !== owner.examId) throw new Error("图像检查结果与当前答卷不一致，请刷新当前批次重新选择。");
      setImageQuality({ owner, value: response.submission });
    } catch (error_) {
      if (isCurrent()) setImageError({ owner, message: getUserErrorMessage(error_, "图像质量状态暂未确认，请刷新重试；页数完整不代表图像检查通过。") });
    } finally { if (isCurrent()) setReadingImage(null); }
  }, [context]);
  useEffect(() => {
    // 与页数检查分开读取，不把 metadata valid 当作图像通过；旧答卷的迟到结论继续按完整上下文丢弃。
    void handleRefreshImageQuality();
    return () => { imageRequest.current += 1; };
  }, [handleRefreshImageQuality]);

  const handleRunQualityCheck = async () => {
    const owner = currentContext.current;
    if (!owner.token || !owner.scope || !owner.examId || !owner.batchId || !owner.submissionId || !owner.associated) {
      setError({ owner, message: "请刷新当前批次处理结果，再选择服务端已关联的答卷；未关联的答卷不能检查。" });
      return;
    }
    if (checkingRequest.current !== null) return;
    const request = ++requestSequence.current;
    checkingRequest.current = request; setError(null); setResult(null); setChecking(owner);
    const isCurrent = () => currentContext.current === owner && requestSequence.current === request;
    try {
      const response = await runSubmissionQualityCheck(owner.client, owner.submissionId);
      if (!isCurrent()) return;
      setResult({ owner, value: response.result });
      await logEvent("info", "submission quality check completed", JSON.stringify(response.result));
    } catch (error_) {
      if (!isCurrent()) return;
      const message = getUserErrorMessage(error_, "页数完整性检查失败，请稍后重试");
      setError({ owner, message });
      await logEvent("warning", "submission quality check failed", message);
    } finally {
      // 旧请求的收尾不能解除新答卷的检查锁，也不能覆盖新请求的等待状态。
      if (isCurrent()) { checkingRequest.current = null; setChecking(null); }
    }
  };

  return {
    qualityResult: result?.owner === context ? result.value : null,
    qualityError: error?.owner === context ? error.message : null,
    isCheckingQuality: checking === context, canRunQualityCheck, handleRunQualityCheck,
    imageQuality: imageQuality?.owner === context ? imageQuality.value : null,
    imageQualityError: imageError?.owner === context ? imageError.message : null,
    isReadingImageQuality: readingImage === context, handleRefreshImageQuality
  };
}
