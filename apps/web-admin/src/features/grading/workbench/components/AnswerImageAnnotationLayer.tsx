import { useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import type { ReviewAnnotationCanvasControls } from "../../../../components/features/review-annotation";

type Point = { x: number; y: number };
interface Props {
  controls: ReviewAnnotationCanvasControls;
  imageSize: { width: number; height: number };
  scale: number;
}

/** 覆盖层与图片处在同一个 SVG 坐标系；分段、缩放、旋转都通过屏幕矩阵逆变换回整张原图。 */
export function AnswerImageAnnotationLayer({ controls, imageSize, scale }: Readonly<Props>) {
  const pointer = useRef<number | null>(null);
  const [drawing, setDrawing] = useState<{ start: Point; end: Point } | null>(null);
  const pointFromEvent = (event: ReactPointerEvent<SVGGElement>): Point | null => {
    const matrix = event.currentTarget.getScreenCTM();
    if (!matrix || imageSize.width <= 0 || imageSize.height <= 0) return null;
    const point = new DOMPoint(event.clientX, event.clientY).matrixTransform(matrix.inverse());
    return { x: Math.max(0, Math.min(1, point.x / imageSize.width)), y: Math.max(0, Math.min(1, point.y / imageSize.height)) };
  };
  const finish = (event: ReactPointerEvent<SVGGElement>, cancelled = false) => {
    if (pointer.current !== event.pointerId) return;
    event.preventDefault(); event.stopPropagation();
    const point = cancelled ? null : pointFromEvent(event);
    pointer.current = null; setDrawing(null);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    // 取消手势也要清除共享编辑器的矩形起点，不能在下一次点击时误接上一条拖动。
    controls.onPointerUp(point ?? { x: Number.NaN, y: Number.NaN });
  };
  const radius = 6 / Math.max(.001, scale);
  return <g className={`answer-annotation-layer answer-annotation-layer--${controls.disabled ? "select" : controls.tool}`}
    onPointerDown={event => {
      if (controls.disabled || controls.tool === "select" || event.button !== 0) return;
      const point = pointFromEvent(event); if (!point) return;
      event.preventDefault(); event.stopPropagation();
      pointer.current = event.pointerId; event.currentTarget.setPointerCapture(event.pointerId);
      if (controls.tool === "rectangle") setDrawing({ start: point, end: point });
      controls.onPointerDown(point);
    }}
    onPointerMove={event => {
      if (pointer.current !== event.pointerId) return;
      event.preventDefault(); event.stopPropagation();
      const point = pointFromEvent(event); if (point) setDrawing(current => current ? { ...current, end: point } : current);
    }}
    onPointerUp={event => finish(event)} onPointerCancel={event => finish(event, true)}>
    <rect width={imageSize.width} height={imageSize.height} fill="transparent" pointerEvents={controls.disabled || controls.tool === "select" ? "none" : "all"} />
    {controls.annotations.map(annotation => {
      const geometry = annotation.geometry, point = geometry.width === 0 && geometry.height === 0;
      return <g key={annotation.id} role="button" tabIndex={0} className={`answer-annotation-mark${annotation.id === controls.selectedId ? " is-selected" : ""}`}
        aria-label={`批注：${annotation.content}`} onPointerDown={event => event.stopPropagation()}
        onClick={event => { event.stopPropagation(); controls.onSelect(annotation); }}
        onKeyDown={event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); controls.onSelect(annotation); } }}>
        <title>{annotation.content}</title>
        {point ? <circle cx={geometry.x * imageSize.width} cy={geometry.y * imageSize.height} r={radius} />
          : <rect x={geometry.x * imageSize.width} y={geometry.y * imageSize.height} width={geometry.width * imageSize.width} height={geometry.height * imageSize.height} />}
      </g>;
    })}
    {drawing ? <rect className="answer-annotation-drawing" pointerEvents="none" x={Math.min(drawing.start.x, drawing.end.x) * imageSize.width}
      y={Math.min(drawing.start.y, drawing.end.y) * imageSize.height} width={Math.abs(drawing.end.x - drawing.start.x) * imageSize.width}
      height={Math.abs(drawing.end.y - drawing.start.y) * imageSize.height} /> : null}
  </g>;
}
