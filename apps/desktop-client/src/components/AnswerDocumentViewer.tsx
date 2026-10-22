import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Alert, Button, Space } from "antd";

export interface AnswerDocument { url: string; contentType: string; filename?: string }

interface DocumentSize { width: number; height: number }

function calculateImageGeometry(size: DocumentSize, viewportSize: DocumentSize, rotation: number, zoom: number) {
  const quarterTurn = rotation % 180 !== 0;
  const rotatedWidth = quarterTurn ? size.height : size.width;
  const rotatedHeight = quarterTurn ? size.width : size.height;
  // 先按旋转后的外框适配视口，再叠加手动缩放；尺寸未就绪时保留原有比例。
  const fit = rotatedWidth && rotatedHeight && viewportSize.width && viewportSize.height
    ? Math.min(viewportSize.width / rotatedWidth, viewportSize.height / rotatedHeight) : 1;
  const imageWidth = size.width * fit * zoom;
  const imageHeight = size.height * fit * zoom;
  return { imageWidth, imageHeight, canvasWidth: quarterTurn ? imageHeight : imageWidth, canvasHeight: quarterTurn ? imageWidth : imageHeight };
}

export function AnswerDocumentViewer({ document, onReadableChange }: Readonly<{
  document: AnswerDocument;
  /** 仅浏览器成功解码且具有有效尺寸的图片可报告为可核对；PDF 阅读器载入不提供这项保证。 */
  onReadableChange?: (readable: boolean) => void;
}>) {
  const [zoom, setZoom] = useState(1);
  const [rotation, setRotation] = useState(0);
  const [failed, setFailed] = useState(false);
  const [imageSize, setImageSize] = useState({ url: "", width: 0, height: 0 });
  const [viewportSize, setViewportSize] = useState({ width: 0, height: 0 });
  const viewport = useRef<HTMLDivElement>(null);
  const dragging = useRef<{ x: number; y: number; left: number; top: number } | null>(null);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 更换答卷原件时重新初始化视图，缩放和失败状态不能沿用到另一份文件。
  useEffect(() => { setZoom(1); setRotation(0); setFailed(false); }, [document.url]);
  const pdf = document.contentType.toLowerCase().includes("pdf");
  const image = document.contentType.toLowerCase().startsWith("image/");
  const tiff = /tiff?/i.test(document.contentType) || /\.tiff?$/i.test(document.filename ?? "");
  const unsupported = !pdf && (!image || tiff);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 更换文档或从失败状态恢复时可能重新挂载视口，必须重新测量并绑定观察器。
  useLayoutEffect(() => {
    const element = viewport.current;
    if (!element) return;
    const measure = () => setViewportSize({ width: element.clientWidth, height: element.clientHeight });
    measure();
    if (typeof ResizeObserver !== "undefined") {
      const observer = new ResizeObserver(measure); observer.observe(element);
      return () => observer.disconnect();
    }
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, [document.url, unsupported, failed]);
  const size = imageSize.url === document.url ? imageSize : { width: 0, height: 0 };
  // biome-ignore lint/correctness/useExhaustiveDependencies: 新 URL 即使与旧图片尺寸相同也须先撤回可读回执，防止新原件解码前放行评分。
  useEffect(() => {
    // 下载到 Blob 不代表图片可显示；损坏、TIFF 和 PDF 均不能作为成功解码的图片放行评分。
    onReadableChange?.(!pdf && !unsupported && !failed && size.width > 0 && size.height > 0);
    return () => onReadableChange?.(false);
  }, [document.url, pdf, unsupported, failed, size.width, size.height, onReadableChange]);
  const { imageWidth, imageHeight, canvasWidth, canvasHeight } = calculateImageGeometry(size, viewportSize, rotation, zoom);
  return <div className="answer-document-viewer">
    <Space wrap className="answer-document-tools">
      <Button disabled={unsupported || failed} onClick={() => setZoom((value) => Math.min(4, value + .25))}>放大</Button>
      <Button disabled={unsupported || failed} onClick={() => setZoom((value) => Math.max(.5, value - .25))}>缩小</Button>
      <Button disabled={unsupported || failed || pdf} onClick={() => setRotation((value) => (value + 90) % 360)}>旋转 90°</Button>
      <Button disabled={unsupported || failed} onClick={() => { setZoom(1); if (viewport.current) { viewport.current.scrollLeft = 0; viewport.current.scrollTop = 0; } }}>适合窗口</Button>
      <span>{Math.round(zoom * 100)}%</span>
      <a href={document.url} download={document.filename ?? "答卷原件"}>下载答卷原件</a>
    </Space>
    {unsupported || failed ? <Alert type="warning" showIcon message={tiff ? "当前答卷为 TIFF，本机查看器无法解码" : "答卷预览未能显示"} description={onReadableChange ? "请重新读取本题可显示图片或答卷文字；仍缺失时联系负责人补核材料。原件和评分输入均保留。" : "请下载原件，用支持该格式的阅读器核对后评分。原件和评分输入均保留。"} /> :
      <div ref={viewport} className="answer-document-viewport" onPointerDown={(event) => {
        if (pdf || event.button !== 0) return;
        const element = event.currentTarget; dragging.current = { x: event.clientX, y: event.clientY, left: element.scrollLeft, top: element.scrollTop }; element.setPointerCapture?.(event.pointerId);
      }} onPointerMove={(event) => {
        const start = dragging.current; if (!start) return;
        event.currentTarget.scrollLeft = start.left - (event.clientX - start.x); event.currentTarget.scrollTop = start.top - (event.clientY - start.y);
      }} onPointerUp={() => { dragging.current = null; }} onPointerCancel={() => { dragging.current = null; }}>
        {/* PDF 交给 WebView 的文档阅读器显示分页与局部缩放；不能将 PDF/TIFF 当作 img 假装已预览。 */}
        {pdf ? <iframe key={`${document.url}:${zoom}`} src={`${document.url}#toolbar=1&zoom=${Math.round(zoom * 100)}`} title={document.filename ?? "答卷 PDF"} onError={() => setFailed(true)} /> :
          <div className="answer-document-canvas" style={{ width: canvasWidth || "100%", height: canvasHeight || undefined }}>
            {/* 画布占据旋转后的宽高；图片绕中心旋转后落在正坐标，四边都可通过滚动到达。 */}
            <img src={document.url} alt={document.filename ?? "答卷图片"} draggable={false} onError={() => setFailed(true)}
              onLoad={(event) => setImageSize({ url: document.url, width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })}
              style={{ width: imageWidth || "100%", height: imageHeight || undefined, left: (canvasWidth - imageWidth) / 2,
                top: (canvasHeight - imageHeight) / 2, transform: `rotate(${rotation}deg)` }} />
          </div>}
      </div>}
    {!unsupported && !failed && <p className="muted">图片可拖动查看局部；PDF 可使用阅读器页码、缩放与旋转工具。显示失败时可下载原件。</p>}
    {pdf && onReadableChange ? <p className="muted">PDF 阅读器载入不能确认内容已显示；请重新读取本题可显示图片或答卷文字，仍缺失时联系负责人补核材料后提交。</p> : null}
  </div>;
}
