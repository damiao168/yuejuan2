import { useEffect, useState } from "react";
import { Alert } from "antd";
import { renderPdfAnswerPages } from "../lib/pdfAnswerPages";
import { getUserErrorMessage } from "../api/userError";

export function PdfAnswerPages({ url, zoom, onReadableChange }: Readonly<{ url: string; zoom: number; onReadableChange?: (readable: boolean) => void }>) {
  const [snapshot, setSnapshot] = useState<{ url: string; images: string[]; total: number; complete: boolean; decoded: Set<number>; error: string }>({ url: "", images: [], total: 0, complete: false, decoded: new Set(), error: "" });
  const current = snapshot.url === url ? snapshot : null;
  useEffect(() => {
    const abort = new AbortController();
    setSnapshot({ url, images: [], total: 0, complete: false, decoded: new Set(), error: "" });
    void renderPdfAnswerPages(url, abort.signal, (image, total) => {
      if (!abort.signal.aborted) setSnapshot(previous => previous.url === url ? { ...previous, total, images: [...previous.images, image] } : previous);
    }).then(() => {
      if (!abort.signal.aborted) setSnapshot(previous => previous.url === url ? { ...previous, complete: true } : previous);
    }).catch(error => {
      if (!abort.signal.aborted) setSnapshot(previous => previous.url === url ? { ...previous, error: getUserErrorMessage(error, "PDF 页面读取失败，请恢复原件后重试。") } : previous);
    });
    return () => abort.abort();
  }, [url]);
  const readable = Boolean(current?.complete && !current.error && current.total > 0 && current.images.length === current.total && current.decoded.size === current.total);
  useEffect(() => { onReadableChange?.(readable); return () => onReadableChange?.(false); }, [readable, onReadableChange]);
  return <section aria-label="PDF 逐页答卷">
    {!readable && !current?.error ? <p role="status">正在本机核对 PDF 页面：{current?.decoded.size ?? 0}{current?.total ? ` / ${current.total}` : ""}；全部页面显示成功后才可提交评分。</p> : null}
    {current?.error ? <Alert type="warning" showIcon message="PDF 答卷未能完整显示" description={`${current.error}原件与人工评分仍保留。`} /> : null}
    {current?.images.map((image, index) => <figure key={`${url}:${index}`}>
      <figcaption>第 {index + 1} / {current.total} 页</figcaption>
      <img src={image} alt={`PDF 答卷第 ${index + 1} 页`} style={{ width: `${zoom * 100}%`, maxWidth: "none", display: "block" }} onLoad={event => {
        if (event.currentTarget.naturalWidth <= 0 || event.currentTarget.naturalHeight <= 0) return;
        setSnapshot(previous => previous.url !== url || previous.decoded.has(index) ? previous : { ...previous, decoded: new Set([...previous.decoded, index]) });
      }} onError={() => setSnapshot(previous => previous.url === url ? { ...previous, error: `第 ${index + 1} 页显示失败，请恢复原件后重试。` } : previous)} />
    </figure>)}
  </section>;
}
