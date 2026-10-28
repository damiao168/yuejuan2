import { useEffect, useRef, useState } from "react";
import { Button, Space } from "antd";
import { CapturePdfOrderPreview } from "./CapturePdfOrderPreview";
import { countCapturePdfPages } from "./capturePdfPreview";
import { inspectCaptureTiff } from "./captureTiffPreview";
import { capturePageGroups } from "./capturePageGrouping";

export function captureSelectionError(files: readonly File[], pages?: number) {
  if (!files.length) return "请先选择答卷文件";
  if (!pages || !Number.isInteger(pages) || pages < 1 || pages > 20) return "请选择每人页数（1–20页）";
  if (files.every(isSinglePageImage) && files.length % pages) return `本次选择 ${files.length} 页，每人 ${pages} 页，还缺 ${pages - files.length % pages} 页`;
  return "";
}

function isSinglePageImage(file: File) {
  return /\.(png|jpe?g)$/i.test(file.name) || ["image/png", "image/jpeg"].includes(file.type);
}

function LocalPageThumbnail({ file }: Readonly<{ file: File }>) {
  const [url, setUrl] = useState("");
  useEffect(() => {
    if (!isSinglePageImage(file) || typeof URL.createObjectURL !== "function") return;
    // 预检只在本地读取所选图片；取消或重排后释放地址，确认前不会登记文件。
    const value = URL.createObjectURL(file); setUrl(value);
    return () => URL.revokeObjectURL(value);
  }, [file]);
  return url ? <img src={url} alt={`${file.name} 页序预览`} style={{ width: 96, height: 128, objectFit: "contain" }} /> : null;
}

export function CaptureFileOrderPreview({ files, pages, onChange, disabled = false, onPendingChange }: Readonly<{ files: File[]; pages?: number; onChange: (files: File[]) => void; disabled?: boolean; onPendingChange?: (pending: boolean) => void }>) {
  const imageOnly = files.every(isSinglePageImage);
  const [pendingPdfs, setPendingPdfs] = useState<Set<File>>(() => new Set());
  const [pageCounts, setPageCounts] = useState<Map<File, number>>(() => new Map());
  const [countFailures, setCountFailures] = useState<Map<File, string>>(() => new Map());
  const [inspectionWarnings, setInspectionWarnings] = useState<Map<File, string>>(() => new Map());
  const [previewFailures, setPreviewFailures] = useState<Map<File, string>>(() => new Map());
  const [originalConfirmation, setOriginalConfirmation] = useState<{ files: File[]; pages?: number; warnings: string }>();
  const [counting, setCounting] = useState(false);
  const notify = useRef(onPendingChange); notify.current = onPendingChange;
  const hasPending = files.some(file => pendingPdfs.has(file));
  const visibleCounts = new Map(files.filter(isSinglePageImage).map(file => [file, 1]));
  files.forEach(file => { const count = pageCounts.get(file); if (count) visibleCounts.set(file, count); });
  const groups = pages ? capturePageGroups(files, visibleCounts, pages) : undefined;
  const missing = groups?.length ? pages! - groups[groups.length - 1].received : 0;
  const countPending = files.some(file => !visibleCounts.has(file) && !countFailures.has(file));
  const originalWarnings = files.map(file => previewFailures.get(file) || inspectionWarnings.get(file) || countFailures.get(file) || "");
  const warningKey = JSON.stringify(originalWarnings);
  const needsOriginalConfirmation = originalWarnings.some(Boolean);
  // 确认只属于当前文件对象、文件顺序、分组和失败原因；重新选择或改序后不能沿用旧确认。
  const originalConfirmed = originalConfirmation?.files === files && originalConfirmation.pages === pages && originalConfirmation.warnings === warningKey;
  const manualPending = needsOriginalConfirmation && !originalConfirmed;
  useEffect(() => { notify.current?.(hasPending || countPending || manualPending || missing > 0); }, [hasPending, countPending, manualPending, missing]);
  useEffect(() => () => notify.current?.(false), []);
  useEffect(() => {
    const abort = new AbortController(); setCounting(true); setPageCounts(new Map()); setCountFailures(new Map()); setInspectionWarnings(new Map()); setPreviewFailures(new Map());
    // 只读取本次选择的页数；换文件或改序会废弃上一组结果，不登记服务端文件。
    void (async () => {
      for (const file of files) {
        if (abort.signal.aborted) return;
        if (isSinglePageImage(file)) continue;
        try {
          const tiff = /\.tiff?$/i.test(file.name) || file.type === "image/tiff";
          const result = tiff ? await inspectCaptureTiff(file) : { pageCount: await countCapturePdfPages(file, abort.signal), previewUnavailable: "" };
          if (abort.signal.aborted) return;
          setPageCounts(current => new Map(current).set(file, result.pageCount));
          const warning = result.previewUnavailable || (result.pageCount > 100 ? `共${result.pageCount}页，超过本地100页预览范围` : "");
          if (warning) setInspectionWarnings(current => new Map(current).set(file, warning));
        } catch {
          if (!abort.signal.aborted) setCountFailures(current => new Map(current).set(file, "页数暂不可读取，无法显示完整分组"));
        }
      }
      if (!abort.signal.aborted) setCounting(false);
    })();
    return () => abort.abort();
  }, [files]);
  const move = (index: number, delta: number) => {
    if (disabled || hasPending || counting) return;
    const next = [...files]; [next[index], next[index + delta]] = [next[index + delta], next[index]]; onChange(next);
  };
  return <>
    <p>按学生连续排列，核对页序后再导入。</p>
    {!imageOnly && <p>PDF/TIFF将按文件内页序拆分，页数和分组以服务端解码校验为准。</p>}
    {counting && !imageOnly ? <p role="status">正在本地核对页数…</p> : null}
    {groups && !imageOnly ? <details open={groups.length <= 8}><summary>预计 {groups.length} 份答卷，共 {groups.reduce((sum, group) => sum + group.received, 0)} 页</summary><ol aria-label="逐份答卷分组">{groups.slice(0, 100).map(group => <li key={group.number}>第 {group.number} 份答卷：{group.sources.map(source => `${source.file.name} 第 ${source.first}${source.last === source.first ? "" : `–${source.last}`} 页`).join("、")}{group.received < pages! ? `（还缺 ${pages! - group.received} 页）` : ""}</li>)}</ol>{groups.length > 100 ? <p>已列出前100份；其余答卷按相同页序继续分组。</p> : null}</details> : null}
    {missing > 0 && !imageOnly ? <p role="alert">本次选择还缺 {missing} 页，补齐后再导入。</p> : null}
    <ol aria-label="本次文件页序与分组预览">{files.map((file, index) => <li key={`${index}:${file.name}:${file.size}:${file.lastModified}`}>
      <LocalPageThumbnail file={file} />
      {/\.(pdf|tiff?)$/i.test(file.name) || ["application/pdf", "image/tiff"].includes(file.type) ? <CapturePdfOrderPreview file={file} disabled={disabled} pagesPerStudent={pages} pageOffset={files.slice(0, index).every(item => visibleCounts.has(item)) ? files.slice(0, index).reduce((sum, item) => sum + visibleCounts.get(item)!, 0) : undefined} onChange={next => onChange(files.map(item => item === file ? next : item))}
        onPreviewFailureChange={reason => setPreviewFailures(previous => { if (previous.get(file) === reason || (!reason && !previous.has(file))) return previous; const next = new Map(previous); if (reason) next.set(file, reason); else next.delete(file); return next; })}
        onPendingChange={pending => setPendingPdfs(previous => { if (previous.has(file) === pending) return previous; const next = new Set(previous); if (pending) next.add(file); else next.delete(file); return next; })} /> : null}
      <span>{file.name}{imageOnly && pages ? ` · 第 ${Math.floor(index / pages) + 1} 份答卷，第 ${index % pages + 1} 页` : ""}</span>
      {originalWarnings[index] ? <p>{originalWarnings[index]}</p> : null}
      <Space><Button size="small" disabled={disabled || hasPending || counting || index === 0} aria-label={`上移文件 ${index + 1}`} onClick={() => move(index, -1)}>上移</Button><Button size="small" disabled={disabled || hasPending || counting || index === files.length - 1} aria-label={`下移文件 ${index + 1}`} onClick={() => move(index, 1)}>下移</Button></Space>
    </li>)}</ol>
    {needsOriginalConfirmation ? <section aria-label="无法预览的原件确认">
      <label><input type="checkbox" checked={originalConfirmed} disabled={disabled || counting || hasPending} onChange={event => setOriginalConfirmation(event.target.checked ? { files, pages, warnings: warningKey } : undefined)} />我已核对原件页序和每人 {pages ?? "指定"} 页的分组，按原文件导入</label>
    </section> : null}
    {captureSelectionError(files, pages) ? <p role="alert">{captureSelectionError(files, pages)}</p> : null}
  </>;
}
