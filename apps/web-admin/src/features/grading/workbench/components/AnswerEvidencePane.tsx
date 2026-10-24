import { useId, type KeyboardEvent, type PointerEvent, type RefObject } from "react";
import { Alert, Button, Segmented, Space, Tooltip } from "antd";
import { RotateCcw, ZoomIn, ZoomOut } from "lucide-react";
import type { RubricPoint } from "../../../../api/papers";
import { EmptyState, LoadingState } from "../../../../components/PageState";
import { ReviewAnnotationWorkspace } from "../../../../components/features/review-annotation";
import { pointLabel, taskStatusLabels } from "../gradingWorkbench.model";
import type { PreviewState, ViewerMode, WorkbenchContext } from "../gradingWorkbench.types";
import type { MathStepSelection } from "../mathWorkbenchEvidence";
import { GradingMathMarkdown } from "./GradingMathMarkdown";
import { AdaptiveAnswerImage } from "./AdaptiveAnswerImage";

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
  viewportRef: RefObject<HTMLElement | null>;
  canViewOriginalImage: boolean;
  canEditDraft: boolean;
  rubricPoints: RubricPoint[];
  onChangeViewerMode: (mode: ViewerMode) => void;
  onZoom: (direction: -1 | 1) => void;
  onRotate: () => void;
  onFit: () => void;
  onResetView?: () => void;
  onPointerDown: (event: PointerEvent<HTMLElement>) => void;
  onPointerMove: (event: PointerEvent<HTMLElement>) => void;
  onPointerUp: (event: PointerEvent<HTMLElement>) => void;
  onKeyDown?: (event: KeyboardEvent<HTMLElement>) => void;
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
  onResetView,
  onPointerDown,
  onPointerMove,
  onPointerUp,
  onKeyDown,
  onImageLoad,
  mathSelection
}: Readonly<AnswerEvidencePaneProps>) {
  const viewerHelpId = useId();
  const viewer = () => {
    if (previewLoading) return <LoadingState label="正在读取答卷页面" />;
    if (viewerMode !== "ocr" && !preview) return <EmptyState title="暂无答卷页面" description="当前任务没有可下载的页面文件。" />;
    const isImage = Boolean(preview?.contentType.startsWith("image/"));
    if (isImage && viewerMode !== "ocr") return <AdaptiveAnswerImage key={`${context.task.id}:${viewerMode}:${preview!.url}`} source={preview!.url}
      label={`${context.task.question_no} 答题图查看区`} helpId={viewerHelpId} imageSize={imageSize} scale={scale} autoFit={autoFit} rotation={rotation} offset={offset} dragging={dragging} viewportRef={viewportRef}
      onZoom={onZoom} onFit={onFit} onResetView={onResetView} onImageLoad={onImageLoad} onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerUp}
      mathSelection={viewerMode === "segment" && context.answerScope !== "grading_group" && mathSelection?.segmentId === context.task.answer_segment_id ? mathSelection : null} />;
    // 图片通过实际宽高缩放，让百分比证据框同步缩放；其他预览才使用 transform 的 scale。
    const viewerTransform = isImage
      ? `translate(${offset.x}px, ${offset.y}px) rotate(${rotation}deg)`
      : `translate(${offset.x}px, ${offset.y}px) scale(${scale}) rotate(${rotation}deg)`;
    const imageStyle = isImage && imageSize
      ? { width: imageSize.width * scale, height: imageSize.height * scale }
      : undefined;
    // 查看区保留 OCR、PDF 和子控件的阅读边界；键盘平移由独立原生按钮接管。
    return (
      <section
        ref={viewportRef}
        className={dragging ? "answer-viewer dragging" : "answer-viewer"}
        aria-label={`${context.task.question_no} 答题图查看区`}
        aria-describedby={viewerHelpId}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
      >
        <div className={isImage ? "answer-viewer-stage image-stage" : "answer-viewer-stage"} style={{ transform: viewerTransform, ...imageStyle }}>
          {viewerMode === "ocr" ? (
            <AnswerOcrPreview context={context} />
          ) : (
            <AnswerFilePreview preview={preview!} onImageLoad={onImageLoad} />
          )}
          {viewerMode === "segment" && context.answerScope !== "grading_group" && isImage && mathSelection?.segmentId === context.task.answer_segment_id ? <span className="math-step-highlight" role="img" aria-label={`步骤 ${mathSelection.stepId} 的答题位置`} style={{ left: `${mathSelection.bbox.x * 100}%`, top: `${mathSelection.bbox.y * 100}%`, width: `${mathSelection.bbox.width * 100}%`, height: `${mathSelection.bbox.height * 100}%` }}><span>{mathSelection.stepId}</span></span> : null}
        </div>
      </section>
    );
  };

  return (
    <section className="answer-panel">
      <div className="panel-head">
        <div>
          <h2>{context.task.question_no} · 学生答案</h2>
          <p>{taskStatusLabels[context.task.status] ?? "待核对"}</p>
        </div>
        <Space wrap>
          <Segmented<ViewerMode>
            size="small"
            value={viewerMode}
            options={[
              ...(context.gradingGroupImageUrl ? [{ label: "整区", value: "group" as const }] : []),
              ...(context.answerScope !== "grading_group" ? [{ label: context.gradingGroupImageUrl ? "单题" : "答题区域", value: "segment" as const }] : []),
              ...(canViewOriginalImage ? [{ label: "原图", value: "original" as const }] : []),
              { label: "识别文本", value: "ocr" as const }
            ]}
            onChange={onChangeViewerMode}
          />
        </Space>
      </div>
      {viewerMode === "group" ? <p className="muted">正在查看整区，当前录入第 {context.task.question_no} 题得分。</p> : null}
      {context.answerScope === "grading_group" ? <p className="muted">此题使用人工确认的整区作答图，尚无单题框；各题仍分别计分。</p> : null}
      {mathSelection?.segmentId === context.task.answer_segment_id && viewerMode === "segment" ? <output className="math-image-selection" style={{ display: "block" }}>已定位 {mathSelection.stepId} · {mathSelection.label}</output> : null}
      {viewer()}
      <details className="grading-more-fields">
        <summary>图像工具与键盘帮助</summary>
        <div className="grading-more-fields-body"><Space wrap>
          {!preview?.contentType.startsWith("image/") || viewerMode === "ocr" ? <><Tooltip title="缩小"><Button icon={<ZoomOut size={14} />} onClick={() => onZoom(-1)} aria-label="缩小答题图" /></Tooltip>
          <span className="viewer-scale">{Math.round(scale * 100)}%</span>
          <Tooltip title="放大"><Button icon={<ZoomIn size={14} />} onClick={() => onZoom(1)} aria-label="放大答题图" /></Tooltip></> : null}
          <Tooltip title="顺时针旋转"><Button icon={<RotateCcw size={14} />} onClick={onRotate} aria-label="旋转答题图" /></Tooltip>
          <Button aria-label="键盘平移答题图" aria-describedby={viewerHelpId} onKeyDown={onKeyDown}>键盘平移</Button>
        </Space><p id={viewerHelpId} className="muted">聚焦“键盘平移”按钮后，放大时可用方向键平移，Shift + 方向键加速，Home 适配窗口，Esc 结束拖拽。</p>
        {context.task.anonymous_code ? <p className="muted">答卷标识：{context.task.anonymous_code}</p> : null}</div>
      </details>
      {context.answerScope !== "grading_group" && context.segmentImageUrl ? (
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

function AnswerOcrPreview({ context }: Readonly<{ context: WorkbenchContext }>) {
  const reason = context.task.reason_code;
  const missingText = ["visual_model_unavailable", "visual_model_not_configured"].includes(reason ?? "")
    ? "本轮尚未生成识别文本：图像阅卷模型当时不可用，请联系管理员恢复后继续识别。"
    : context.reviewContext?.ai_failure ? "本轮识别未完成，请先查看答题区域。"
    : "本题尚无识别文本，请查看答题区域。";
  const ocrText = context.ocrText || context.ocrResults.map((item) => item.text).filter(Boolean).join("\n") || missingText;
  return (
    <>
      <GradingMathMarkdown className="ocr-overlay-text">{ocrText}</GradingMathMarkdown>
      {/* 显示上限按原结果顺序计算，先截取再跳过无效框；坐标仍是原像素，最小边长为 12px。 */}
      {context.ocrResults.slice(0, 12).map((result) =>
        result.bbox?.length === 4 ? (
          <span
            className="ocr-bbox"
            key={result.id}
            style={{ left: result.bbox[0], top: result.bbox[1], width: Math.max(12, result.bbox[2] - result.bbox[0]), height: Math.max(12, result.bbox[3] - result.bbox[1]) }}
            title={result.text}
          />
        ) : null
      )}
    </>
  );
}

function AnswerFilePreview({ preview, onImageLoad }: Readonly<{
  preview: PreviewState;
  onImageLoad: AnswerEvidencePaneProps["onImageLoad"];
}>) {
  if (preview.contentType.startsWith("image/")) {
    return (
      <img
        src={preview.url}
        alt={preview.filename ?? "答卷页面"}
        onLoad={(event) => onImageLoad({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })}
      />
    );
  }
  if (preview.contentType === "application/pdf") {
    return <iframe title={preview.filename ?? "答卷 PDF"} src={preview.url} />;
  }
  return (
    <Alert
      type="info"
      showIcon
      message="该文件类型不支持内嵌预览"
      description="可下载后用本机程序查看。"
      action={<Button size="small" href={preview.url} target="_blank" download={preview.filename ?? "答卷文件"}>下载查看</Button>}
    />
  );
}
