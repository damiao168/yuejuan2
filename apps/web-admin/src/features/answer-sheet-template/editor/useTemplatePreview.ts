import { useEffect, useRef, useState } from "react";
import { GlobalWorkerOptions, getDocument, type PDFDocumentProxy } from "pdfjs-dist";
import pdfWorker from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { getUserErrorMessage } from "../../../api/client";
import { downloadFileBlob } from "../../../api/files";
import type { PaperVersion } from "../../../api/papers";

GlobalWorkerOptions.workerSrc = pdfWorker;

export interface PreviewState {
  loading: boolean;
  error?: string;
  pageCount: number;
  width: number;
  height: number;
  imageUrl?: string;
  textLayerAvailable?: boolean;
  textLayerChecking?: boolean;
}

async function inspectPdfTextLayer(pdfDocument: PDFDocumentProxy, isActive: () => boolean) {
  // 检查全部页面，避免纯封面掩盖后续可用文字；前页已有文字也继续读取后页。
  // 没有可用文字层的扫描件保留手动框选入口，不提供 OCR 操作。
  let hasText = false;
  try {
    for (let index = 1; index <= pdfDocument.numPages; index += 1) {
      const content = await (await pdfDocument.getPage(index)).getTextContent();
      // 文件已切换时停止后页检查；调用方会丢弃这个旧来源的结果。
      if (!isActive()) return false;
      hasText ||= content.items.some(item => "str" in item && String(item.str).trim().length > 0);
    }
  } catch {
    // 任一页读取失败就放弃整份文字层辅助，用户仍可手动框选。
    return false;
  }
  return hasText;
}

export function useTemplatePreview(selectedPaper: PaperVersion | undefined) {
  const pdfRef = useRef<PDFDocumentProxy | undefined>(undefined);
  const imageObjectUrlRef = useRef<string | undefined>(undefined);
  const renderedObjectUrlRef = useRef<string | undefined>(undefined);
  const [preview, setPreview] = useState<PreviewState>({ loading: false, pageCount: 1, width: 2480, height: 3508 });
  const [pdfSourceId, setPdfSourceId] = useState("");
  const [pageNo, setPageNo] = useState(1);

  useEffect(() => {
    let active = true;
    async function loadSource() {
      setPdfSourceId("");
      const previousDocument = pdfRef.current;
      pdfRef.current = undefined;
      if (imageObjectUrlRef.current) URL.revokeObjectURL(imageObjectUrlRef.current);
      imageObjectUrlRef.current = undefined;
      setPreview((current) => ({ ...current, loading: Boolean(selectedPaper), error: undefined, imageUrl: undefined, textLayerAvailable: undefined, textLayerChecking: Boolean(selectedPaper) }));
      // 先解除旧资源绑定，再等待销毁；旧文件清理迟到时不能清空新文件的文档引用和预览。
      await previousDocument?.destroy().catch(() => undefined);
      if (!active) return;
      if (!selectedPaper) {
        setPreview({ loading: false, pageCount: 1, width: 2480, height: 3508 });
        return;
      }
      setPreview((current) => ({ ...current, loading: true, error: undefined, imageUrl: undefined, textLayerAvailable: undefined, textLayerChecking: true }));
      try {
        const download = await downloadFileBlob(selectedPaper.file_asset_id);
        if (!active) return;
        if (download.contentType === "application/pdf" || selectedPaper.file.content_type === "application/pdf") {
          const pdfDocument = await getDocument({ data: await download.blob.arrayBuffer() }).promise;
          // PDF 解析可能晚于答题卡文件切换完成，过期文档必须销毁，不能再绑定给当前预览。
          if (!active) { await pdfDocument.destroy(); return; }
          pdfRef.current = pdfDocument;
          setPdfSourceId(selectedPaper.id);
          setPageNo((current) => Math.min(Math.max(1, current), pdfDocument.numPages));
          setPreview((current) => ({ ...current, loading: false, pageCount: pdfDocument.numPages }));
          const hasText = await inspectPdfTextLayer(pdfDocument, () => active);
          if (active) setPreview(current => ({ ...current, textLayerAvailable: hasText, textLayerChecking: false }));
        } else {
          const url = URL.createObjectURL(download.blob);
          imageObjectUrlRef.current = url;
          const dimensions = await new Promise<{ width: number; height: number }>((resolve, reject) => {
            const image = new Image();
            image.onload = () => resolve({ width: image.naturalWidth, height: image.naturalHeight });
            image.onerror = () => reject(new Error("无法读取答题卡图片尺寸"));
            image.src = url;
          });
          if (!active) return;
          setPageNo(1);
          setPreview((current) => ({ ...current, loading: false, pageCount: 1, imageUrl: url, textLayerAvailable: false, textLayerChecking: false, ...dimensions }));
        }
      } catch (error) {
        if (active) setPreview((current) => ({ ...current, loading: false, error: getUserErrorMessage(error, "操作失败，请稍后重试") }));
      }
    }
    void loadSource();
    return () => { active = false; };
  }, [selectedPaper]);

  useEffect(() => {
    let active = true;
    async function renderPDFPage() {
      const pdfDocument = pdfRef.current;
      if (!pdfDocument || !pdfSourceId) return;
      try {
        setPreview((current) => ({ ...current, loading: true, error: undefined }));
        const page = await pdfDocument.getPage(Math.min(pageNo, pdfDocument.numPages));
        const base = page.getViewport({ scale: 1 });
        // 渲染像素受上限控制；区域编辑仍使用原始页面尺寸，避免缩放改变保存的几何比例。
        const scale = Math.min(1.5, 1400 / base.width);
        const viewport = page.getViewport({ scale });
        const canvas = window.document.createElement("canvas");
        canvas.width = Math.ceil(viewport.width);
        canvas.height = Math.ceil(viewport.height);
        const context = canvas.getContext("2d");
        if (!context) throw new Error("浏览器无法创建 PDF 画布");
        await page.render({ canvasContext: context, viewport, canvas }).promise;
        const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/png"));
        if (!blob || !active) return;
        if (renderedObjectUrlRef.current) URL.revokeObjectURL(renderedObjectUrlRef.current);
        const url = URL.createObjectURL(blob);
        renderedObjectUrlRef.current = url;
        setPreview((current) => ({ ...current, loading: false, width: Math.round(base.width), height: Math.round(base.height), imageUrl: url }));
      } catch (error) {
        if (active) setPreview((current) => ({ ...current, loading: false, error: getUserErrorMessage(error, "操作失败，请稍后重试") }));
      }
    }
    void renderPDFPage();
    return () => { active = false; };
  }, [pageNo, pdfSourceId]);

  useEffect(() => () => {
    void pdfRef.current?.destroy();
    if (imageObjectUrlRef.current) URL.revokeObjectURL(imageObjectUrlRef.current);
    if (renderedObjectUrlRef.current) URL.revokeObjectURL(renderedObjectUrlRef.current);
  }, []);

  return { preview, pdfRef, pageNo, setPageNo };
}
