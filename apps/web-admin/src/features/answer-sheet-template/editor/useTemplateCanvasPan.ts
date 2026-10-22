import { useCallback, useLayoutEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type RefObject } from "react";

type PanInteraction = {
  pointerId: number;
  startX: number;
  startY: number;
  startLeft: number;
  startTop: number;
  scopeKey: string;
  element: HTMLDivElement;
};

function consumePointer(event: ReactPointerEvent<HTMLDivElement>) {
  event.preventDefault();
  event.stopPropagation();
}

export function useTemplateCanvasPan({ scrollRef, enabled, scopeKey }: {
  scrollRef: RefObject<HTMLDivElement | null>;
  enabled: boolean;
  scopeKey: string;
}) {
  const interactionRef = useRef<PanInteraction | undefined>(undefined);
  const [panning, setPanning] = useState(false);

  const clearInteraction = useCallback((releaseCapture = true, updateState = true) => {
    const interaction = interactionRef.current;
    interactionRef.current = undefined;
    if (interaction && releaseCapture) {
      const { element, pointerId } = interaction;
      // 取消或移除页面时浏览器可能已释放指针；重复释放不能打断页面切换。
      try {
        if (element.hasPointerCapture?.(pointerId)) element.releasePointerCapture(pointerId);
      } catch { /* 元素已失去指针时，无需恢复拖动。 */ }
    }
    if (updateState) setPanning(false);
  }, []);

  useLayoutEffect(() => {
    // 切换页面或退出拖动模式后，旧鼠标位移不能再移动新页面，也不能留下抓取光标。
    if (interactionRef.current && (!enabled || interactionRef.current.scopeKey !== scopeKey)) clearInteraction();
  }, [clearInteraction, enabled, scopeKey]);

  useLayoutEffect(() => () => clearInteraction(true, false), [clearInteraction]);

  const onPointerDownCapture = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!enabled || event.button !== 0 || interactionRef.current) return;
    const element = scrollRef.current;
    if (!element) return;
    // 在捕获阶段拦住画布子元素，拖动视口时不能同时创建或移动题区。
    consumePointer(event);
    interactionRef.current = {
      pointerId: event.pointerId, startX: event.clientX, startY: event.clientY,
      startLeft: element.scrollLeft, startTop: element.scrollTop, element, scopeKey
    };
    try { element.setPointerCapture(event.pointerId); }
    catch { interactionRef.current = undefined; return; }
    setPanning(true);
  };

  const onPointerMoveCapture = (event: ReactPointerEvent<HTMLDivElement>) => {
    const interaction = interactionRef.current;
    if (!interaction || event.pointerId !== interaction.pointerId || !enabled || interaction.scopeKey !== scopeKey) return;
    consumePointer(event);
    const { element } = interaction;
    // 位移使用视口像素，只改变滚动位置；小于视口的纸张没有可拖出的额外空白。
    const maxLeft = Math.max(0, element.scrollWidth - element.clientWidth);
    const maxTop = Math.max(0, element.scrollHeight - element.clientHeight);
    element.scrollLeft = Math.min(maxLeft, Math.max(0, interaction.startLeft + interaction.startX - event.clientX));
    element.scrollTop = Math.min(maxTop, Math.max(0, interaction.startTop + interaction.startY - event.clientY));
  };

  const finishPointer = (event: ReactPointerEvent<HTMLDivElement>, releaseCapture = true) => {
    if (event.pointerId !== interactionRef.current?.pointerId) return;
    consumePointer(event);
    clearInteraction(releaseCapture);
  };

  return {
    panning, onPointerDownCapture, onPointerMoveCapture,
    onPointerUpCapture: (event: ReactPointerEvent<HTMLDivElement>) => finishPointer(event),
    onPointerCancelCapture: (event: ReactPointerEvent<HTMLDivElement>) => finishPointer(event),
    onLostPointerCapture: (event: ReactPointerEvent<HTMLDivElement>) => finishPointer(event, false)
  };
}
