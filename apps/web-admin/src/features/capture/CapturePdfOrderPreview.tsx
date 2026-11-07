import { useEffect, useRef, useState } from "react";
import { Button, Space } from "antd";
import { previewCapturePdf, reorderCapturePdf } from "./capturePdfPreview";
import { previewCaptureTiff, reorderCaptureTiff } from "./captureTiffPreview";
import { getUserErrorMessage } from "../../api/client";
import { capturePagePosition } from "./capturePageGrouping";

export function CapturePdfOrderPreview({ file, disabled, onChange, onPendingChange, onPreviewFailureChange, pagesPerStudent, pageOffset }: Readonly<{ file: File; disabled: boolean; onChange: (file: File) => void; onPendingChange: (pending: boolean) => void; onPreviewFailureChange?: (reason: string) => void; pagesPerStudent?: number; pageOffset?: number }>) {
  const format = /\.tiff?$/i.test(file.name) || file.type === "image/tiff" ? "TIFF" : "PDF";
  const [images, setImages] = useState<string[]>([]);
  const [pageCount, setPageCount] = useState(0); const [order, setOrder] = useState<number[]>([]);
  const [reading, setReading] = useState(false); const [saving, setSaving] = useState(false); const [error, setError] = useState("");
  const [previewError, setPreviewError] = useState("");
  const current = useRef(file); current.current = file;
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const notifyPending = useRef(onPendingChange); notifyPending.current = onPendingChange;
  const notifyFailure = useRef(onPreviewFailureChange); notifyFailure.current = onPreviewFailureChange;
  const changed = order.some((page, index) => page !== index);
  useEffect(() => { notifyPending.current(changed || saving || reading); }, [changed, saving, reading]);
  useEffect(() => { notifyFailure.current?.(previewError); }, [previewError]);
  useEffect(() => () => notifyPending.current(false), []);
  useEffect(() => {
    const abort = new AbortController(); setReading(true); setImages([]); setError(""); setPreviewError(""); setSaving(false); setPageCount(0); setOrder([]);
    // 选入文件后直接显示本地预览；离开预览或换文件时销毁解码任务，迟到页面不能混入新选择。
    const preview = format === "TIFF" ? previewCaptureTiff : previewCapturePdf;
    void preview(file, abort.signal, (image, count) => {
      if (abort.signal.aborted) return; setPageCount(count); setImages(previous => [...previous, image]);
    }).then(() => { if (!abort.signal.aborted) setReading(false); }).catch(cause => { if (!abort.signal.aborted) { setReading(false); setPreviewError(getUserErrorMessage(cause, `${format}页面预览失败，请核对原件后再确认导入。`)); } });
    return () => abort.abort();
  }, [file, format]);
  useEffect(() => { if (pageCount && images.length === pageCount && !reading) setOrder(Array.from({ length: pageCount }, (_, index) => index)); }, [pageCount, images.length, reading]);
  const visible = order.length ? order : images.map((_, index) => index);
  async function apply() {
    if (disabled || saving || reading || !changed) return; setSaving(true); setError("");
    try { const result = await (format === "TIFF" ? reorderCaptureTiff : reorderCapturePdf)(file, order); if (mounted.current && current.current === file) onChange(result); }
    catch (cause) { if (mounted.current && current.current === file) setError(getUserErrorMessage(cause, "页序生成失败，原文件仍保留。")); }
    finally { if (mounted.current && current.current === file) setSaving(false); }
  }
  function move(index: number, delta: number) {
    if (disabled || reading || saving || order.length !== pageCount || index + delta < 0 || index + delta >= order.length) return;
    const next = [...order]; [next[index], next[index + delta]] = [next[index + delta], next[index]]; setOrder(next);
  }
  return <div>
    <h4>{format}页面与页序</h4>
      {reading ? <p role="status">正在本地读取{format}页面：{images.length}{pageCount ? ` / ${pageCount}` : ""}</p> : null}
      {error || previewError ? <p role="alert">{error || previewError}</p> : null}
      <ol aria-label={`${file.name} 内部页序`}>{visible.map((originalPage, index) => <li key={originalPage}>
        <img src={images[originalPage]} alt={`原文件第 ${originalPage + 1} 页预览`} style={{ width: 96, height: 128, objectFit: "contain" }} />
        <span>导入第 {index + 1} 页（原第 {originalPage + 1} 页）{pagesPerStudent && pageOffset !== undefined ? ` · ${capturePagePosition(pageOffset + index, pagesPerStudent)}` : ""}</span>
        <Space><Button disabled={disabled || reading || saving || order.length !== pageCount || index === 0} onClick={() => move(index, -1)}>上移此页</Button><Button disabled={disabled || reading || saving || order.length !== pageCount || index === order.length - 1} onClick={() => move(index, 1)}>下移此页</Button></Space>
      </li>)}</ol>
      {changed && <Space><Button disabled={disabled || saving} loading={saving} onClick={() => void apply()}>应用{format}页序</Button><Button disabled={saving} onClick={() => setOrder(Array.from({ length: pageCount }, (_, index) => index))}>恢复原页序</Button></Space>}
      {changed ? <p>先应用或恢复页序，再确认导入。</p> : null}
  </div>;
}
