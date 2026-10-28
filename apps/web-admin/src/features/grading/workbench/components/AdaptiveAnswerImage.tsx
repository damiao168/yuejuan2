import { useCallback, useLayoutEffect, useMemo, useRef, useState, useEffect, type CSSProperties, type ReactNode } from "react";
import { Button, Segmented, Space, Tooltip } from "antd";
import { RotateCcw, ZoomIn, ZoomOut } from "lucide-react";
import { planImageReadingLayout } from "../imageReadingLayout";
import type { AnswerEvidencePaneProps } from "./AnswerEvidencePane";
import type { ReviewAnnotationCanvasControls } from "../../../../components/features/review-annotation";
import { AnswerImageAnnotationLayer } from "./AnswerImageAnnotationLayer";
import "./answer-image-reading.css";

type Props = Pick<AnswerEvidencePaneProps, "scale" | "autoFit" | "imageSize" | "rotation" | "offset" | "dragging" | "viewportRef" | "onZoom" | "onFit" | "onPointerDown" | "onPointerMove" | "onPointerUp" | "onImageLoad" | "mathSelection"> & {
  source: string; label: string; helpId: string; onResetView?: () => void; controls?: ReactNode; annotations?: ReviewAnnotationCanvasControls;
};

function rotatedImage(size: NonNullable<Props["imageSize"]>, rotation: number) {
  const angle = ((rotation % 360) + 360) % 360;
  const quarter = angle === 90 || angle === 270;
  const transform = angle === 90 ? `translate(${size.height} 0) rotate(90)`
    : angle === 180 ? `translate(${size.width} ${size.height}) rotate(180)`
    : angle === 270 ? `translate(0 ${size.width}) rotate(270)` : undefined;
  return { width: quarter ? size.height : size.width, height: quarter ? size.width : size.height, transform, angle };
}

/** 分段只改变当前受授权图片的显示窗口；所有窗口仍引用同一张原图，不能据此生成评分裁图或改写坐标。 */
export function AdaptiveAnswerImage(props: Readonly<Props>) {
  const { source, label, helpId, imageSize, rotation, scale, autoFit, offset, dragging, viewportRef, onZoom, onFit, onImageLoad, mathSelection } = props;
  const viewport = useRef<HTMLElement | null>(null);
  const [frame, setFrame] = useState({ width: 800, height: 520 });
  const [layout, setLayout] = useState<"smart" | "whole">("smart");
  const manualSelection = useRef("");
  const selectionKey = mathSelection ? JSON.stringify(mathSelection) : "";
  const attachViewport = useCallback((element: HTMLElement | null) => { viewport.current = element; viewportRef.current = element; }, [viewportRef]);
  useLayoutEffect(() => {
    const element = viewport.current;
    if (!element) return;
    const measure = () => {
      const width = element.clientWidth, height = element.clientHeight;
      if (!width || !height) return;
      // 工具栏在窄窗口可能换行；文档流下按实际顶部位置留出余高，不能继续减固定工具栏高度。
      element.style.setProperty("--answer-viewer-available-height", `${Math.max(240, window.innerHeight - Math.max(0, element.getBoundingClientRect().top) - 8)}px`);
      // 这里只扣画布内边距；每段标题由规划器按实际行数扣除。
      setFrame(current => current.width === width - 16 && current.height === height - 16 ? current : { width: Math.max(1, width - 16), height: Math.max(1, height - 16) });
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    window.addEventListener("resize", measure);
    return () => { observer.disconnect(); window.removeEventListener("resize", measure); };
  }, []);
  const oriented = useMemo(() => imageSize ? rotatedImage(imageSize, rotation) : null, [imageSize, rotation]);
  const plan = useMemo(() => planImageReadingLayout({ imageWidth: oriented?.width ?? 0, imageHeight: oriented?.height ?? 0, viewportWidth: frame.width, viewportHeight: frame.height, gap: 8, captionHeight: 20 }), [oriented, frame]);
  const segmented = layout === "smart" && plan.tiles.length > 1;
  const tiles = plan.tiles;
  const wholeFit = oriented ? Math.min(4, Math.max(.1, Math.min((frame.width - 16) / oriented.width, (frame.height - 16) / oriented.height))) : 1;
  const relativeZoom = autoFit ? 1 : scale / wholeFit;
  const tileScale = plan.scale * relativeZoom;
  const displayedScale = segmented ? tileScale : scale;

  useEffect(() => {
    if (!mathSelection) { manualSelection.current = ""; return; }
    // 重置后旧步骤不能覆盖阅读位置；新选中的步骤在放大后仍可定位到对应分段。
    if (manualSelection.current === selectionKey) return;
    if (!mathSelection || !imageSize || !oriented || !segmented) return;
    const x = (mathSelection.bbox.x + mathSelection.bbox.width / 2) * imageSize.width;
    const y = (mathSelection.bbox.y + mathSelection.bbox.height / 2) * imageSize.height;
    const point = oriented.angle === 90 ? { x: imageSize.height - y, y: x } : oriented.angle === 180 ? { x: imageSize.width - x, y: imageSize.height - y }
      : oriented.angle === 270 ? { x: y, y: imageSize.width - x } : { x, y };
    const index = plan.tiles.findIndex(tile => point.x >= tile.x && point.x <= tile.x + tile.width && point.y >= tile.y && point.y <= tile.y + tile.height);
    const tile = viewport.current?.querySelectorAll<HTMLElement>(".answer-reading-tile")[index];
    if (tile && viewport.current) {
      const bounds = tile.getBoundingClientRect(), visible = viewport.current.getBoundingClientRect();
      if (bounds.left >= visible.left && bounds.right <= visible.right && bounds.top >= visible.top && bounds.bottom <= visible.bottom) return;
      viewport.current.scrollTo?.({ left: Math.max(0, viewport.current.scrollLeft + bounds.left - visible.left - 8), top: Math.max(0, viewport.current.scrollTop + bounds.top - visible.top - 8) });
    }
  }, [mathSelection, selectionKey, imageSize, oriented, plan, segmented]);

  const changeLayout = (next: "smart" | "whole") => { manualSelection.current = selectionKey; setLayout(next); onFit(); viewport.current?.scrollTo?.({ top: 0, left: 0 }); };
  const resetView = () => { manualSelection.current = selectionKey; setLayout("smart"); (props.onResetView ?? onFit)(); viewport.current?.scrollTo?.({ top: 0, left: 0 }); };
  const imageStyle: CSSProperties | undefined = imageSize ? { width: imageSize.width * scale, height: imageSize.height * scale } : undefined;
  return <div className="answer-image-reading">
    <div className="answer-reading-toolbar">
      <Space size={6} wrap>
        {props.controls}
        {plan.tiles.length > 1 ? <Tooltip title="按图像和窗口比例自动分段；整图内容完整保留"><Segmented value={layout} onChange={value => changeLayout(value as "smart" | "whole")} options={[{ label: "智能分段", value: "smart" }, { label: "整图查看", value: "whole" }]} size="small" aria-label="答题图阅读布局" /></Tooltip> : null}
        <Button size="small" icon={<ZoomOut size={15} />} aria-label="缩小答题图" onClick={() => onZoom(-1)} />
        <output className="answer-reading-scale" aria-label="答题图缩放比例">{Math.round(displayedScale * 100)}%</output>
        <Button size="small" icon={<ZoomIn size={15} />} aria-label="放大答题图" onClick={() => onZoom(1)} />
        <Button size="small" icon={<RotateCcw size={15} />} onClick={resetView}>重置视图</Button>
      </Space>
      {segmented ? <span className="answer-reading-count">共 {tiles.length} 段 · 全部显示</span> : null}
    </div>
    <section ref={attachViewport} className={`answer-viewer answer-image-viewport${segmented ? " answer-viewer-tiled" : ""}${dragging ? " dragging" : ""}`} aria-label={label} aria-describedby={helpId}
      onPointerDown={props.onPointerDown} onPointerMove={props.onPointerMove} onPointerUp={props.onPointerUp} onPointerCancel={props.onPointerUp}>
      {segmented && imageSize && oriented ? <div className="answer-reading-tiles" style={{ gridTemplateColumns: `repeat(${plan.columns}, max-content)`, transform: `translate(${offset.x}px, ${offset.y}px)` }} data-reading-axis={plan.axis}>
        {tiles.map(tile => <figure className="answer-reading-tile" key={tile.index} data-source-rect={`${tile.x},${tile.y},${tile.width},${tile.height}`}>
          <figcaption>第 {tile.index + 1} 段{tile.index > 0 ? <span>与前段有重叠</span> : null}</figcaption>
          <svg role="img" aria-label={`第 ${tile.index + 1} 段 · ${label}`} width={tile.width * tileScale} height={tile.height * tileScale} viewBox={`${tile.x} ${tile.y} ${tile.width} ${tile.height}`}>
            <g transform={oriented.transform}><image href={source} width={imageSize.width} height={imageSize.height} />
              {mathSelection ? <rect className="answer-reading-highlight" role="img" aria-label={`步骤 ${mathSelection.stepId} 的答题位置`} x={mathSelection.bbox.x * imageSize.width} y={mathSelection.bbox.y * imageSize.height} width={mathSelection.bbox.width * imageSize.width} height={mathSelection.bbox.height * imageSize.height}><title>{mathSelection.stepId}</title></rect> : null}
              {props.annotations ? <AnswerImageAnnotationLayer controls={props.annotations} imageSize={imageSize} scale={tileScale} /> : null}
            </g>
          </svg>
        </figure>)}
      </div> : <div className="answer-viewer-stage image-stage" style={{ transform: `translate(${offset.x}px, ${offset.y}px) rotate(${rotation}deg)`, ...imageStyle }}>
        <img src={source} alt={label.replace("查看区", "")} onLoad={event => onImageLoad({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })} />
        {props.annotations && imageSize ? <svg className="answer-image-annotation-overlay" viewBox={`0 0 ${imageSize.width} ${imageSize.height}`}>
          <AnswerImageAnnotationLayer controls={props.annotations} imageSize={imageSize} scale={scale} />
        </svg> : null}
        {mathSelection ? <span className="math-step-highlight" role="img" aria-label={`步骤 ${mathSelection.stepId} 的答题位置`} style={{ left: `${mathSelection.bbox.x * 100}%`, top: `${mathSelection.bbox.y * 100}%`, width: `${mathSelection.bbox.width * 100}%`, height: `${mathSelection.bbox.height * 100}%` }}><span>{mathSelection.stepId}</span></span> : null}
      </div>}
    </section>
  </div>;
}
