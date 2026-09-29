import type { PointerEvent, RefObject } from "react";
import { Alert, Button, Segmented, Space, Tooltip } from "antd";
import { Maximize2, RotateCcw, ZoomIn, ZoomOut } from "lucide-react";
import type { RubricPoint } from "../../../../api/papers";
import { EmptyState, LoadingState } from "../../../../components/PageState";
import { ReviewAnnotationWorkspace } from "../../../../components/features/review-annotation";
import { pointLabel, sourceLabels, taskStatusLabels } from "../gradingWorkbench.model";
import type { PreviewState, ViewerMode, WorkbenchContext } from "../gradingWorkbench.types";
import type { MathStepSelection } from "../mathWorkbenchEvidence";

export interface AnswerEvidencePaneProps {
  context: WorkbenchContext;
  preview: PreviewState | null;
  previewLoading: boolean;
  viewerMode: ViewerMode;
  scale: number;
  autoFit: boolean;
  imageSize: { width: number; height: number } | null;
  rotation: number;
  offset: { x: number; y: number };
  dragging: boolean;
  viewportRef: RefObject<HTMLDivElement | null>;
  canViewOriginalImage: boolean;
  canEditDraft: boolean;
  rubricPoints: RubricPoint[];
  onChangeViewerMode: (mode: ViewerMode) => void;
  onZoom: (direction: -1 | 1) => void;
  onRotate: () => void;
  onFit: () => void;
  onPointerDown: (event: PointerEvent<HTMLDivElement>) => void;
  onPointerMove: (event: PointerEvent<HTMLDivElement>) => void;
  onPointerUp: (event: PointerEvent<HTMLDivElement>) => void;
  onImageLoad: (size: { width: number; height: number }) => void;
  mathSelection?: MathStepSelection | null;
}

export function AnswerEvidencePane({
  context,
  preview,
  previewLoading,
  viewerMode,
  scale,
  autoFit,
  imageSize,
  rotation,
  offset,
  dragging,
  viewportRef,
  canViewOriginalImage,
  canEditDraft,
  rubricPoints,
  onChangeViewerMode,
  onZoom,
  onRotate,
  onFit,
  onPointerDown,
  onPointerMove,
  onPointerUp,
  onImageLoad,
  mathSelection
}: AnswerEvidencePaneProps) {
  const viewer = () => {
    if (previewLoading) return <LoadingState label="正在读取答卷页面" />;
    const ocrText = context.ocrText || context.ocrResults.map((item) => item.text).filter(Boolean).join("\n") || "当前页面暂无识别文本。";
    if (viewerMode !== "ocr" && !preview) return <EmptyState title="暂无答卷页面" description="当前任务没有可下载的页面文件。" />;
    const isImage = Boolean(preview?.contentType.startsWith("image/"));
    const isPDF = preview?.contentType === "application/pdf";
    // 图片通过实际宽高缩放，让百分比证据框同步缩放；其他预览才使用 transform 的 scale。
    const viewerTransform = isImage
      ? `translate(${offset.x}px, ${offset.y}px) rotate(${rotation}deg)`
      : `translate(${offset.x}px, ${offset.y}px) scale(${scale}) rotate(${rotation}deg)`;
    const imageStyle = isImage && imageSize
      ? { width: imageSize.width * scale, height: imageSize.height * scale }
      : undefined;
    return (
      <div
        ref={viewportRef}
        className={dragging ? "answer-viewer dragging" : "answer-viewer"}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
      >
        <div className={isImage ? "answer-viewer-stage image-stage" : "answer-viewer-stage"} style={{ transform: viewerTransform, ...imageStyle }}>
          {viewerMode === "ocr" ? (
            <pre className="ocr-overlay-text">{ocrText}</pre>
          ) : isImage ? (
            <img
              src={preview!.url}
              alt={preview!.filename ?? "答卷页面"}
              onLoad={(event) => onImageLoad({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })}
            />
          ) : isPDF ? (
            <iframe title={preview!.filename ?? "答卷 PDF"} src={preview!.url} />
          ) : (
            <Alert
              type="info"
              showIcon
              message="该文件类型不支持内嵌预览"
              description="可下载后用本机程序查看。"
              action={<Button size="small" href={preview!.url} target="_blank" download={preview!.filename ?? "答卷文件"}>下载查看</Button>}
            />
          )}
          {viewerMode === "ocr"
            ? context.ocrResults.slice(0, 12).map((result) =>
                result.bbox?.length === 4 ? (
                  <span
                    className="ocr-bbox"
                    key={result.id}
                    style={{ left: result.bbox[0], top: result.bbox[1], width: Math.max(12, result.bbox[2] - result.bbox[0]), height: Math.max(12, result.bbox[3] - result.bbox[1]) }}
                    title={result.text}
                  />
                ) : null
              )
            : null}
          {viewerMode === "segment" && isImage && mathSelection?.segmentId === context.task.answer_segment_id ? <span className="math-step-highlight" role="img" aria-label={`步骤 ${mathSelection.stepId} 的答题位置`} style={{ left: `${mathSelection.bbox.x * 100}%`, top: `${mathSelection.bbox.y * 100}%`, width: `${mathSelection.bbox.width * 100}%`, height: `${mathSelection.bbox.height * 100}%` }}><span>{mathSelection.stepId}</span></span> : null}
        </div>
      </div>
    );
  };

  return (
    <section className="answer-panel">
      <div className="panel-head">
        <div>
          <h2>{context.task.question_no} · 学生答案</h2>
          <p title={[sourceLabels[context.task.source] ? "" : context.task.source, taskStatusLabels[context.task.status] ? "" : context.task.status].filter(Boolean).join(" / ") || undefined}>{context.task.anonymous_code || "暂无匿名码"} · {sourceLabels[context.task.source] ?? "其他来源"} · {taskStatusLabels[context.task.status] ?? "未知状态"}</p>
        </div>
        <Space wrap>
          <Segmented<ViewerMode>
            size="small"
            value={viewerMode}
            options={canViewOriginalImage
              ? [{ label: "答题区域", value: "segment" }, { label: "原图", value: "original" }, { label: "识别文本", value: "ocr" }]
              : [{ label: "答题区域", value: "segment" }, { label: "识别文本", value: "ocr" }]}
            onChange={onChangeViewerMode}
          />
          <Tooltip title="缩小"><Button icon={<ZoomOut size={14} />} onClick={() => onZoom(-1)} aria-label="缩小答题图" /></Tooltip>
          <span className="viewer-scale">{Math.round(scale * 100)}%</span>
          <Tooltip title="放大"><Button icon={<ZoomIn size={14} />} onClick={() => onZoom(1)} aria-label="放大答题图" /></Tooltip>
          <Tooltip title="顺时针旋转"><Button icon={<RotateCcw size={14} />} onClick={onRotate} aria-label="旋转答题图" /></Tooltip>
          <Tooltip title="适配窗口"><Button type={autoFit ? "primary" : "default"} icon={<Maximize2 size={14} />} onClick={onFit} aria-label="适配窗口" /></Tooltip>
        </Space>
      </div>
      {mathSelection?.segmentId === context.task.answer_segment_id && viewerMode === "segment" ? <p className="math-image-selection" role="status">已定位 {mathSelection.stepId} · {mathSelection.label}</p> : null}
      {viewer()}
      {context.segmentImageUrl ? (
        <details className="grading-more-fields">
          <summary>答题区域批注与常用评语</summary>
          <div className="grading-more-fields-body">
            <ReviewAnnotationWorkspace
              key={context.task.id}
              reviewTaskId={context.task.id}
              imageUrl={context.segmentImageUrl}
              imageAlt={`${context.task.question_no} 答题区域`}
              disabled={!canEditDraft}
              rubricCriteria={rubricPoints.map((point) => ({ id: point.id, label: pointLabel(point) }))}
            />
          </div>
        </details>
      ) : null}
    </section>
  );
}
