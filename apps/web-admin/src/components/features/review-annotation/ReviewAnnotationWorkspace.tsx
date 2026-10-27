import { useEffect, useRef, useState, type ReactNode } from "react";
import { Alert, Button, Tooltip } from "antd";
import { Check, MousePointer2, Square, Trash2, X } from "lucide-react";
import type { CanonicalImageGeometry, ReviewAnnotation } from "@edugrade/sdk";
import { canonicalPoint, canonicalRectangle, hasDrawableArea, type ClientPoint } from "./geometry";
import { useReviewAnnotations } from "./useReviewAnnotations";
import "./reviewAnnotations.css";

type MarkTool = "select" | "correct" | "incorrect" | "rectangle";

export interface ReviewAnnotationCanvasControls {
  tool: "select" | "note" | "rectangle";
  annotations: readonly ReviewAnnotation[];
  selectedId?: string;
  disabled: boolean;
  toolbar?: ReactNode;
  // 切分窗口先换算回整张原图的 0～1 坐标，保存时不能使用分段内的局部比例。
  onPointerDown: (point: ClientPoint) => void;
  onPointerUp: (point: ClientPoint) => void;
  onSelect: (annotation: ReviewAnnotation) => void;
}

export interface ReviewAnnotationWorkspaceProps {
  reviewTaskId: string;
  disabled?: boolean;
  drawingAvailable?: boolean;
  renderCanvas: (controls: ReviewAnnotationCanvasControls) => ReactNode;
  viewTools?: ReactNode;
}

/** 常驻图标只写图上标记；分数、评语和图像布局均不由批注工具改变。 */
export function ReviewAnnotationWorkspace({ reviewTaskId, disabled = false, drawingAvailable = true, renderCanvas, viewTools }: Readonly<ReviewAnnotationWorkspaceProps>) {
  const workspace = useReviewAnnotations(reviewTaskId);
  const [tool, setTool] = useState<MarkTool>("select");
  const [selectedId, setSelectedId] = useState<string>();
  const [saving, setSaving] = useState(false);
  const drawingStart = useRef<ClientPoint | null>(null);
  const actionLock = useRef(false);
  const scope = useRef({ taskId: reviewTaskId, active: true });
  scope.current.taskId = reviewTaskId;
  useEffect(() => { scope.current.active = true; return () => { scope.current.active = false; }; }, []);
  // 切题只重置工具和选择；旧写入回执不得选中新答卷上的标记。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 任务身份变化时需清空上一份的工具选择。
  useEffect(() => { setTool("select"); setSelectedId(undefined); drawingStart.current = null; }, [reviewTaskId]);
  const selected = workspace.annotations.find(annotation => annotation.id === selectedId);
  const unavailable = disabled || !drawingAvailable || saving || workspace.loading;
  const changeTool = (next: MarkTool) => { drawingStart.current = null; setTool(next); setSelectedId(undefined); };
  const isCurrent = (taskId: string) => scope.current.active && scope.current.taskId === taskId;

  const saveMark = async (type: "note" | "rectangle", geometry: CanonicalImageGeometry, content: string) => {
    if (unavailable || actionLock.current) return;
    const taskId = reviewTaskId;
    actionLock.current = true;
    setSaving(true);
    try {
      // 图标批注本身不改分；沿用个人可见边界，不把标记默认为已发布给学生。
      const annotation = await workspace.saveAnnotation({ type, geometry, content, visibility: "private", payload: {} });
      if (isCurrent(taskId)) setSelectedId(annotation.id);
    } catch {
      // 共享 hook 展示真实写入错误，不能用本地点位冒充已经保存的批注。
    } finally {
      actionLock.current = false;
      if (isCurrent(taskId)) setSaving(false);
    }
  };
  const removeSelected = async () => {
    if (disabled || saving || actionLock.current || !selected) return;
    const taskId = reviewTaskId;
    actionLock.current = true;
    setSaving(true);
    try {
      await workspace.removeAnnotation(selected);
      if (isCurrent(taskId)) setSelectedId(undefined);
    } catch {
      // 删除失败保留选中项，教师可以重试，不能先把服务端仍存在的标记藏掉。
    } finally {
      actionLock.current = false;
      if (isCurrent(taskId)) setSaving(false);
    }
  };
  const start = (point: ClientPoint) => {
    if (unavailable || actionLock.current || tool === "select" || !Number.isFinite(point.x) || !Number.isFinite(point.y)) return;
    const normalized = canonicalPoint(point);
    if (tool === "rectangle") { drawingStart.current = normalized; return; }
    void saveMark("note", normalized, tool === "correct" ? "✓" : "×");
  };
  const finish = (point: ClientPoint) => {
    const from = drawingStart.current;
    drawingStart.current = null;
    if (unavailable || actionLock.current || tool !== "rectangle" || !from || !Number.isFinite(point.x) || !Number.isFinite(point.y)) return;
    const geometry = canonicalRectangle(from, point);
    if (hasDrawableArea(geometry)) void saveMark("rectangle", geometry, "框选");
  };
  const icon = (key: MarkTool, label: string, graphic: ReactNode) => <Tooltip key={key} title={!drawingAvailable && key !== "select" ? "请切换到单题答题区域" : label} placement="right">
    <Button type={tool === key ? "primary" : "text"} icon={graphic} aria-label={label} aria-pressed={tool === key}
      disabled={key === "select" ? saving : unavailable} onClick={() => changeTool(key)} />
  </Tooltip>;

  return <section className="review-annotation-workspace review-annotation-workspace--inline" aria-label="答题卡批注">
    {workspace.error ? <Alert type={workspace.saveState === "conflict" ? "warning" : "error"} showIcon message={workspace.error} /> : null}
    <div className="review-annotation-layout">
      <aside className="review-annotation-rail" role="toolbar" aria-label="批注工具" aria-orientation="vertical">
        {icon("select", "选择批注", <MousePointer2 size={18} />)}
        {icon("correct", "打勾", <Check size={18} />)}
        {icon("incorrect", "打叉", <X size={18} />)}
        {icon("rectangle", "框选批注", <Square size={18} />)}
        <Tooltip title="删除选中批注" placement="right"><Button type="text" icon={<Trash2 size={18} />} aria-label="删除选中批注" disabled={disabled || saving || !drawingAvailable || !selected} onClick={() => void removeSelected()} /></Tooltip>
        {viewTools ? <div className="review-annotation-view-tools">{viewTools}</div> : null}
      </aside>
      <div className="review-annotation-custom-canvas">{renderCanvas({
        tool: !drawingAvailable || disabled ? "select" : tool === "correct" || tool === "incorrect" ? "note" : tool,
        // 单题坐标不能直接套到整区图或原始页面；切换视图时隐藏这组标记。
        annotations: drawingAvailable ? workspace.annotations : [], selectedId: drawingAvailable ? selectedId : undefined,
        disabled: unavailable, onPointerDown: start, onPointerUp: finish,
        onSelect: annotation => { if (saving) return; drawingStart.current = null; setTool("select"); setSelectedId(annotation.id); }
      })}</div>
    </div>
  </section>;
}
