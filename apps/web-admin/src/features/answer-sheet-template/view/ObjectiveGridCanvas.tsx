import { useLayoutEffect, useRef, useState, type CSSProperties, type PointerEvent } from "react";
import type { AnalysisBox } from "../../../api/answerSheetTemplateAnalysis";
import type { ObjectiveGridGroup, ObjectiveOptionOrientation } from "../../../api/answerSheetObjectiveGrid";
import { orderObjectiveOptions, validObjectiveBox } from "../editor/objectiveGridGeometry";
import { MAX_TEMPLATE_ZOOM, MIN_TEMPLATE_ZOOM, normalizeTemplateZoom, useTemplateCanvasZoom } from "../editor/useTemplateCanvasZoom";
import { useTemplateCanvasPan } from "../editor/useTemplateCanvasPan";

const overlay = (box: AnalysisBox): CSSProperties => ({ position: "absolute", left: `${box.x * 100}%`, top: `${box.y * 100}%`,
  width: `${box.width * 100}%`, height: `${box.height * 100}%`, boxSizing: "border-box", pointerEvents: "none" });

function releasePointer(element: HTMLDivElement, pointerId: number) {
  // 切页卸载时浏览器可能已取消捕获，不让释放旧指针中断页面恢复。
  try { if (element.hasPointerCapture?.(pointerId)) element.releasePointerCapture(pointerId); }
  catch { /* 捕获已由浏览器释放。 */ }
}

/** 独立预览只画临时大区，不触发主画布的单题关联或改变模板缩放。 */
export function ObjectiveGridCanvas({ imageURL, block, groups, analysisKey, orientation, reverse, disabled, onBlock, labels, subjectLabel = "选择题" }: {
  imageURL: string; block?: AnalysisBox; groups: ObjectiveGridGroup[]; orientation: ObjectiveOptionOrientation; reverse: boolean;
  analysisKey?: string; disabled: boolean; onBlock: (box: AnalysisBox) => void; labels: Record<string, string>; subjectLabel?: string;
}) {
  const [zoom, setZoom] = useState(1);
  const [tool, setTool] = useState<"draw" | "pan">("draw");
  const canvasRef = useRef<HTMLDivElement>(null);
  const scrollRef = useTemplateCanvasZoom({ zoom, setZoom, canvasRef, scopeKey: imageURL });
  const pan = useTemplateCanvasPan({ scrollRef, enabled: !disabled && tool === "pan", scopeKey: imageURL });
  const [focusRequest, setFocusRequest] = useState<{ scope: string; box: AnalysisBox; targetZoom?: number }>();
  const lastAutoFocus = useRef<string | undefined>(undefined);
  const focusScope = useRef(imageURL);
  const resultKey = analysisKey ?? groups.map(group => group.group_id).join("|");
  useLayoutEffect(() => {
    if (focusScope.current !== imageURL) {
      focusScope.current = imageURL; lastAutoFocus.current = resultKey; setFocusRequest(undefined); return;
    }
    // 阅读顺序和题号修改会重建 groups 数组；只有新分析结果出现时自动聚焦一次。
    if (!resultKey || !groups.length || !validObjectiveBox(block) || lastAutoFocus.current === resultKey) return;
    lastAutoFocus.current = resultKey; setFocusRequest({ scope: imageURL, box: { ...block } });
  }, [imageURL, resultKey, block, groups.length]);
  useLayoutEffect(() => {
    if (!focusRequest || focusRequest.scope !== imageURL) return;
    const scroll = scrollRef.current;
    const bounds = canvasRef.current?.getBoundingClientRect();
    if (!scroll || !bounds?.width || !bounds.height || !scroll.clientWidth || !scroll.clientHeight) return;
    const box = focusRequest.box;
    if (focusRequest.targetZoom === undefined) {
      const fit = normalizeTemplateZoom(Math.min((scroll.clientWidth - 48) / (bounds.width / zoom * box.width),
        (scroll.clientHeight - 48) / (bounds.height / zoom * box.height)));
      setFocusRequest({ ...focusRequest, targetZoom: fit });
      if (fit !== zoom) setZoom(fit);
      return;
    }
    // 先提交倍率，再在下一次布局阶段居中；该 effect 排在滚轮锚点处理之后，不会反复重置用户缩放。
    if (focusRequest.targetZoom !== zoom) { setZoom(focusRequest.targetZoom); return; }
    const viewport = scroll.getBoundingClientRect();
    const left = scroll.scrollLeft + bounds.left + (box.x + box.width / 2) * bounds.width - viewport.left - scroll.clientWidth / 2;
    const top = scroll.scrollTop + bounds.top + (box.y + box.height / 2) * bounds.height - viewport.top - scroll.clientHeight / 2;
    scroll.scrollLeft = Math.min(Math.max(0, scroll.scrollWidth - scroll.clientWidth), Math.max(0, left));
    scroll.scrollTop = Math.min(Math.max(0, scroll.scrollHeight - scroll.clientHeight), Math.max(0, top));
    setFocusRequest(undefined);
  }, [focusRequest, imageURL, zoom, scrollRef]);
  const scopeKey = `${imageURL}:${disabled}:${tool}`;
  type Drag = { pointerId: number; element: HTMLDivElement; scopeKey: string; startX: number; startY: number; x: number; y: number };
  const [drag, setDrag] = useState<Drag>();
  const dragRef = useRef<Drag | undefined>(undefined);
  // 模式、页面和权限改变后释放旧指针；旧页面的抬手不能在新页面留下大框。
  useLayoutEffect(() => {
    const previous = dragRef.current;
    if (!previous || previous.scopeKey === scopeKey) return;
    dragRef.current = undefined; setDrag(undefined);
    releasePointer(previous.element, previous.pointerId);
  }, [scopeKey]);
  useLayoutEffect(() => () => {
    const previous = dragRef.current;
    if (previous) releasePointer(previous.element, previous.pointerId);
  }, []);
  const finish = () => {
    const previous = dragRef.current;
    dragRef.current = undefined; setDrag(undefined);
    if (previous) releasePointer(previous.element, previous.pointerId);
  };
  const point = (event: PointerEvent<HTMLDivElement>) => {
    const bounds = event.currentTarget.getBoundingClientRect();
    return { x: Math.min(1, Math.max(0, (event.clientX - bounds.left) / bounds.width)),
      y: Math.min(1, Math.max(0, (event.clientY - bounds.top) / bounds.height)) };
  };
  const rectangle = drag ? { x: Math.min(drag.startX, drag.x), y: Math.min(drag.startY, drag.y),
    width: Math.abs(drag.x - drag.startX), height: Math.abs(drag.y - drag.startY) } : block;
  return <div>
    <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: 8, marginBottom: 8 }}>
      <button type="button" aria-pressed={tool === "draw"} disabled={disabled} onClick={() => setTool("draw")}>框选大区</button>
      <button type="button" aria-pressed={tool === "pan"} disabled={disabled} onClick={() => setTool("pan")}>拖动画布</button>
      <button type="button" aria-label={`缩小${subjectLabel}预览`} disabled={zoom <= MIN_TEMPLATE_ZOOM} onClick={() => setZoom(value => normalizeTemplateZoom(value - .1))}>−</button>
      <button type="button" aria-label={`恢复${subjectLabel}预览原始缩放`} onClick={() => setZoom(1)}>{Math.round(zoom * 100)}%</button>
      <button type="button" aria-label={`放大${subjectLabel}预览`} disabled={zoom >= MAX_TEMPLATE_ZOOM} onClick={() => setZoom(value => normalizeTemplateZoom(value + .1))}>＋</button>
      <button type="button" disabled={!validObjectiveBox(block)} onClick={() => { if (validObjectiveBox(block)) setFocusRequest({ scope: imageURL, box: { ...block } }); }}>聚焦大区</button>
      <span style={{ color: "#667085", fontSize: 12 }}>Ctrl + 滚轮缩放；切换拖动画布后按住左键移动</span>
    </div>
    <div ref={scrollRef} role="region" aria-label={`${subjectLabel}局部画布`} tabIndex={0}
      style={{ maxHeight: "55vh", overflow: "auto", overscrollBehavior: "contain", background: "#f3f4f6", padding: 12 }}
      onPointerDownCapture={pan.onPointerDownCapture} onPointerMoveCapture={pan.onPointerMoveCapture}
      onPointerUpCapture={pan.onPointerUpCapture} onPointerCancelCapture={pan.onPointerCancelCapture} onLostPointerCapture={pan.onLostPointerCapture}>
    <div ref={canvasRef} aria-label={`${subjectLabel}大区框选预览`} style={{ position: "relative", width: `${zoom * 100}%`, margin: "0 auto",
      cursor: disabled ? "default" : tool === "pan" ? pan.panning ? "grabbing" : "grab" : "crosshair", touchAction: "none", userSelect: "none" }}
    onPointerDown={event => {
      if (disabled || tool !== "draw" || event.button !== 0 || dragRef.current) return;
      const position = point(event);
      try { event.currentTarget.setPointerCapture(event.pointerId); }
      catch { return; }
      const next = { pointerId: event.pointerId, element: event.currentTarget, scopeKey, startX: position.x, startY: position.y, ...position };
      dragRef.current = next; setDrag(next);
    }}
    onPointerMove={event => {
      const previous = dragRef.current;
      if (!previous || event.pointerId !== previous.pointerId || disabled) return;
      const next = { ...previous, ...point(event) };
      dragRef.current = next; setDrag(next);
    }}
    onPointerUp={event => {
      const previous = dragRef.current;
      if (!previous || event.pointerId !== previous.pointerId) return;
      if (disabled) { finish(); return; }
      const position = point(event);
      const box = { x: Math.min(previous.startX, position.x), y: Math.min(previous.startY, position.y),
        width: Math.abs(position.x - previous.startX), height: Math.abs(position.y - previous.startY) };
      finish();
      if (box.width >= .01 && box.height >= .01) onBlock(box);
    }} onPointerCancel={event => { if (event.pointerId === dragRef.current?.pointerId) finish(); }}
    onLostPointerCapture={event => { if (event.pointerId === dragRef.current?.pointerId) finish(); }}>
    <img src={imageURL} alt={`当前页${subjectLabel}大区`} draggable={false} onLoad={() => setFocusRequest(value => value ? { ...value } : undefined)}
      style={{ display: "block", width: "100%", background: "white", pointerEvents: "none" }} />
    {rectangle ? <div style={{ ...overlay(rectangle), border: "2px dashed #d48806", background: "rgba(250,173,20,.05)" }} /> : null}
    {groups.map((group, index) => <div key={group.group_id}>
      <div style={{ ...overlay(group.bbox), border: "1px solid #1677ff" }}><span style={{ position: "absolute", right: "calc(100% + 4px)", top: "50%", transform: "translateY(-50%)", whiteSpace: "nowrap", color: "#0958d9", fontSize: 12 }}>{labels[group.group_id] || `组 ${index + 1}`}</span></div>
      {orderObjectiveOptions(group, orientation, reverse).map((option, optionIndex) => <div key={`${group.group_id}-${optionIndex}`}
        style={{ ...overlay(option.bbox), border: "1px solid #389e0d" }}>
        {zoom >= 2 ? <span style={{ position: "absolute", bottom: "calc(100% + 2px)", left: "50%", transform: "translateX(-50%)", color: "#237804", fontSize: 10 }}>{String.fromCharCode(65 + optionIndex)}</span> : null}
      </div>)}
    </div>)}
    </div>
    </div>
  </div>;
}
