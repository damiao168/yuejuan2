import { useEffect, useState, type ReactNode } from "react";
import { Alert, Button, Spin } from "antd";
import { getProcessingExceptionContext, getProcessingExceptionImage, type ProcessingExceptionContext } from "../api/processing";
import { getUserErrorMessage } from "../api/client";

function renderExceptionMaterial(value: ProcessingExceptionContext | undefined, error: string, retry: ReactNode, examId: string, onNavigate: ((path: string) => void) | undefined) {
  if (error) {
    return <Alert type="error" showIcon message={error} action={retry} />;
  }
  if (!value) {
    return <Spin tip="正在读取异常页面" />;
  }
  return <>
    <p>考号：{value.candidate_no || "尚未匹配"} · 第 {value.page_no} 页</p>
    <p className="muted-text">答卷：{value.submission_id} · 页面：{value.exception.page_id}</p>
    {value.capture_batch_id && onNavigate ? <Button onClick={() => onNavigate(`/exams/${encodeURIComponent(examId)}/capture?batch=${encodeURIComponent(value.capture_batch_id!)}&submission=${encodeURIComponent(value.submission_id)}&page=${encodeURIComponent(value.capture_page_id || "")}&tab=${value.exception.code === "BLOCKED_MISSING_IDENTITY" || value.exception.code === "BLOCKED_MISSING_PAGE" ? "matching" : "image-quality"}`)}>打开本页处理工作区</Button> : null}
  </>;
}

export function ProcessingExceptionEvidence({ id, examId, onNavigate }: Readonly<{ id: string; examId: string; onNavigate?: (path: string) => void }>) {
  const [attempt, setAttempt] = useState(0);
  const [value, setValue] = useState<ProcessingExceptionContext>();
  const [url, setUrl] = useState("");
  const [error, setError] = useState("");
  const [imageError, setImageError] = useState("");
  // biome-ignore lint/correctness/useExhaustiveDependencies: attempt 是用户重读同一异常材料的显式触发键；删除会使重试按钮无效。
  useEffect(() => {
    const controller = new AbortController();
    let objectURL = "";
    setValue(undefined); setUrl(""); setError(""); setImageError("");
    void getProcessingExceptionContext(id, controller.signal).then(async response => {
      if (controller.signal.aborted) return;
      const context = response.exception_context;
      if (context.exception.id !== id || context.exception.exam_id !== examId) throw new Error("异常页面已变化，请重新选择");
      setValue(context);
      try {
        const image = await getProcessingExceptionImage(id, controller.signal);
        if (controller.signal.aborted) return;
        objectURL = URL.createObjectURL(image.blob); setUrl(objectURL);
      } catch (cause) {
        if (!controller.signal.aborted) setImageError(getUserErrorMessage(cause, "异常页原图读取失败"));
      }
    }).catch(cause => { if (!controller.signal.aborted) setError(getUserErrorMessage(cause, "异常页面材料读取失败")); });
    // 切换异常或考试就清除旧图；失败状态不能继续充当新页面的证据。
    return () => { controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL); };
  }, [id, examId, attempt]);
  const retry = <Button onClick={() => setAttempt(current => current + 1)}>重试页面材料</Button>;
  return <section aria-label="异常页面材料">
    {renderExceptionMaterial(value, error, retry, examId, onNavigate)}
    {imageError ? <Alert type="error" showIcon message={imageError} action={retry} /> : null}
    {url ? <img src={url} alt={`异常页原图，第 ${value?.page_no} 页`} style={{ maxWidth: "100%", maxHeight: 540 }} onError={() => { setUrl(""); setImageError("异常页原图显示失败，请重试"); }} /> : null}
  </section>;
}
