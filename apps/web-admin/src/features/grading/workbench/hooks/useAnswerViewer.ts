import { useCallback, useEffect, useRef, useState, type MutableRefObject, type PointerEvent } from "react";
import { App } from "antd";
import { downloadReviewWorkspaceImage } from "../../../../api/review";
import { formatError } from "../gradingWorkbench.model";
import type { DraftFallbackSnapshot, PreviewState, ViewerMode, WorkbenchContext } from "../gradingWorkbench.types";

type PreviewPromise = Promise<Awaited<ReturnType<typeof downloadReviewWorkspaceImage>> | null>;

export interface UseAnswerViewerOptions {
  context: WorkbenchContext | null;
  canViewOriginalImage: boolean;
  prefetchedPreviewRef: MutableRefObject<Map<string, PreviewPromise>>;
  contentRevision?: string;
}

export function useAnswerViewer({ context, canViewOriginalImage, prefetchedPreviewRef, contentRevision }: UseAnswerViewerOptions) {
  const { message } = App.useApp();
  const [preview, setPreview] = useState<PreviewState | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [mode, setMode] = useState<ViewerMode>("segment");
  const [scale, setScale] = useState(1);
  const [fitScale, setFitScale] = useState(1);
  const [autoFit, setAutoFit] = useState(true);
  const [imageSize, setImageSize] = useState<{ width: number; height: number } | null>(null);
  const [rotation, setRotation] = useState(0);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [dragging, setDragging] = useState(false);
  const [dragStart, setDragStart] = useState({ x: 0, y: 0 });
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const requestRef = useRef(0);

  const invalidateContent = useCallback(() => {
    requestRef.current += 1;
    setPreview(null);
    setImageSize(null);
  }, []);

  const prepareContext = useCallback(() => {
    invalidateContent();
    setAutoFit(true);
    setFitScale(1);
    setScale(1);
    setRotation(0);
    setOffset({ x: 0, y: 0 });
  }, [invalidateContent]);

  const restore = useCallback((viewer: DraftFallbackSnapshot["viewer"]) => {
    setMode(viewer.mode);
    setScale(viewer.scale);
    setAutoFit(viewer.fit);
    setRotation(viewer.rotation);
    setOffset(viewer.offset);
  }, []);

  const calculateFitScale = useCallback((size: { width: number; height: number }, angle = rotation) => {
    const viewport = viewportRef.current;
    if (!viewport || size.width <= 0 || size.height <= 0) return 1;
    const quarterTurn = Math.abs(angle % 180) === 90;
    const contentWidth = quarterTurn ? size.height : size.width;
    const contentHeight = quarterTurn ? size.width : size.height;
    const availableWidth = Math.max(120, viewport.clientWidth - 32);
    const availableHeight = Math.max(80, viewport.clientHeight - 32);
    return Math.min(4, Math.max(0.1, Math.min(availableWidth / contentWidth, availableHeight / contentHeight)));
  }, [rotation]);

  const fit = useCallback((size = imageSize, force = false) => {
    if (!size) return;
    const nextScale = calculateFitScale(size);
    setFitScale(nextScale);
    if (autoFit || force) {
      setAutoFit(true);
      setScale(nextScale);
      setOffset({ x: 0, y: 0 });
    }
  }, [autoFit, calculateFitScale, imageSize]);

  const changeMode = useCallback((nextMode: ViewerMode) => {
    if (nextMode === "original" && !canViewOriginalImage) {
      message.warning("完整原图仅限管理员查看");
      return;
    }
    requestRef.current += 1;
    setMode(nextMode);
    setPreview(null);
    setImageSize(null);
    setAutoFit(true);
    setFitScale(1);
    setScale(1);
    setRotation(0);
    setOffset({ x: 0, y: 0 });
  }, [canViewOriginalImage, message]);

  const zoom = useCallback((direction: -1 | 1) => {
    setAutoFit(false);
    const step = Math.max(0.05, fitScale * 0.15);
    setScale((value) => Math.min(4, Math.max(0.1, Number((value + direction * step).toFixed(3)))));
  }, [fitScale]);

  const rotate = useCallback(() => {
    setRotation((value) => (value + 90) % 360);
    setOffset({ x: 0, y: 0 });
  }, []);

  const onPointerDown = useCallback((event: PointerEvent<HTMLDivElement>) => {
    if (scale <= fitScale * 1.001) return;
    setDragging(true);
    setDragStart({ x: event.clientX - offset.x, y: event.clientY - offset.y });
    viewportRef.current?.setPointerCapture(event.pointerId);
  }, [fitScale, offset.x, offset.y, scale]);

  const onPointerMove = useCallback((event: PointerEvent<HTMLDivElement>) => {
    if (!dragging) return;
    setOffset({ x: event.clientX - dragStart.x, y: event.clientY - dragStart.y });
  }, [dragStart.x, dragStart.y, dragging]);

  const onPointerUp = useCallback((event: PointerEvent<HTMLDivElement>) => {
    setDragging(false);
    viewportRef.current?.releasePointerCapture(event.pointerId);
  }, []);

  const onImageLoad = useCallback((size: { width: number; height: number }) => {
    setImageSize(size);
    window.requestAnimationFrame(() => fit(size));
  }, [fit]);

  const loadPreview = useCallback(async () => {
    const requestId = ++requestRef.current;
    if (!context || mode === "ocr" || (mode === "original" && (!canViewOriginalImage || !context.originalImageUrl)) || (mode === "segment" && !context.segmentImageUrl)) {
      setPreview(null);
      setPreviewLoading(false);
      setImageSize(null);
      return;
    }
    setPreviewLoading(true);
    setPreview((current) => {
      if (current?.url) URL.revokeObjectURL(current.url);
      return null;
    });
    setImageSize(null);
    setAutoFit(true);
    try {
      const imagePath = mode === "segment" ? context.segmentImageUrl : context.originalImageUrl!;
      // 标注内容已有修订时必须重取图片；按任务预取的旧图不含这个修订。
      const cachedPreview = mode === "segment" && !contentRevision ? prefetchedPreviewRef.current.get(context.task.id) : undefined;
      if (mode === "segment") prefetchedPreviewRef.current.delete(context.task.id);
      const file = (cachedPreview ? await cachedPreview : null) ?? await downloadReviewWorkspaceImage(imagePath);
      if (requestId !== requestRef.current) return;
      setPreview((current) => {
        if (current?.url) URL.revokeObjectURL(current.url);
        return { url: URL.createObjectURL(file.blob), contentType: file.contentType, filename: file.filename, contentRevision: mode === "segment" ? contentRevision : undefined };
      });
    } catch (error) {
      if (requestId === requestRef.current) message.error(formatError(error));
    } finally {
      if (requestId === requestRef.current) setPreviewLoading(false);
    }
  }, [canViewOriginalImage, contentRevision, context, message, mode, prefetchedPreviewRef]);

  useEffect(() => {
    if (context && mode !== "ocr") void loadPreview();
  }, [context, loadPreview, mode]);

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || !imageSize) return;
    const update = () => fit(imageSize);
    update();
    const observer = new ResizeObserver(update);
    observer.observe(viewport);
    return () => observer.disconnect();
  }, [fit, imageSize]);

  useEffect(() => () => {
    if (preview?.url) URL.revokeObjectURL(preview.url);
  }, [preview?.url]);

  return {
    preview: mode !== "segment" || preview?.contentRevision === contentRevision ? preview : null,
    previewLoading,
    mode,
    scale,
    fitScale,
    autoFit,
    imageSize,
    rotation,
    offset,
    dragging,
    viewportRef,
    invalidateContent,
    prepareContext,
    restore,
    fit,
    changeMode,
    zoom,
    rotate,
    onPointerDown,
    onPointerMove,
    onPointerUp,
    onImageLoad
  };
}
