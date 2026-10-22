import { useLayoutEffect, useRef, type Dispatch, type RefObject, type SetStateAction } from "react";

export const MIN_TEMPLATE_ZOOM = 0.5;
export const MAX_TEMPLATE_ZOOM = 4;

export function normalizeTemplateZoom(value: number) {
  const candidate = Number.isNaN(value) ? 1 : value;
  return Math.round(Math.min(MAX_TEMPLATE_ZOOM, Math.max(MIN_TEMPLATE_ZOOM, candidate)) * 1000) / 1000;
}

type ZoomAnchor = { scopeKey: string; x: number; y: number; clientX: number; clientY: number };

export function useTemplateCanvasZoom({ zoom, setZoom, canvasRef, scopeKey }: {
  zoom: number;
  setZoom: Dispatch<SetStateAction<number>>;
  canvasRef: RefObject<HTMLDivElement | null>;
  scopeKey: string;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const latestZoomRef = useRef(normalizeTemplateZoom(zoom));
  const committedZoomRef = useRef(normalizeTemplateZoom(zoom));
  const scopeRef = useRef(scopeKey);
  const pendingAnchorRef = useRef<ZoomAnchor | undefined>(undefined);

  useLayoutEffect(() => {
    // 翻页或切换来源后，旧页面尚未应用的光标锚点不能移动新页面。
    if (scopeRef.current !== scopeKey) pendingAnchorRef.current = undefined;
    scopeRef.current = scopeKey;
    latestZoomRef.current = normalizeTemplateZoom(zoom);
    committedZoomRef.current = normalizeTemplateZoom(zoom);
    const anchor = pendingAnchorRef.current;
    pendingAnchorRef.current = undefined;
    const scroll = scrollRef.current;
    const bounds = canvasRef.current?.getBoundingClientRect();
    if (!anchor || anchor.scopeKey !== scopeKey || !scroll || !bounds) return;
    // 重新读取放大后的矩形，兼容画布从居中显示变为横向溢出；只移动视口，不改保存坐标。
    scroll.scrollLeft += bounds.left + anchor.x * bounds.width - anchor.clientX;
    scroll.scrollTop += bounds.top + anchor.y * bounds.height - anchor.clientY;
  }, [canvasRef, scopeKey, zoom]);

  useLayoutEffect(() => {
    const scroll = scrollRef.current;
    if (!scroll) return;
    const onWheel = (event: WheelEvent) => {
      if (!event.ctrlKey && !event.metaKey) return;
      // 浏览器默认把 Ctrl + 滚轮用于整页缩放；到画布缩放边界时也必须拦截。
      event.preventDefault();
      if (!Number.isFinite(event.deltaY) || event.deltaY === 0) return;
      const bounds = canvasRef.current?.getBoundingClientRect();
      if (!bounds || bounds.width <= 0 || bounds.height <= 0) return;
      const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? scroll.clientHeight || 600 : 1;
      // 各设备滚轮单位不同；限制单次步幅，避免触摸板或翻页滚轮突然跳到极限。
      const pixels = Math.min(120, Math.max(-120, event.deltaY * unit));
      const nextZoom = normalizeTemplateZoom(latestZoomRef.current * Math.exp(-pixels * 0.002));
      if (nextZoom === latestZoomRef.current) return;
      const x = Math.min(1, Math.max(0, (event.clientX - bounds.left) / bounds.width));
      const y = Math.min(1, Math.max(0, (event.clientY - bounds.top) / bounds.height));
      pendingAnchorRef.current = nextZoom === committedZoomRef.current ? undefined : {
        scopeKey, x, y, clientX: bounds.left + x * bounds.width, clientY: bounds.top + y * bounds.height
      };
      // 连续滚轮可能先于 React 提交，后续事件须基于刚请求的倍率继续计算。
      latestZoomRef.current = nextZoom;
      setZoom(nextZoom);
    };
    // React 的被动 wheel 监听无法可靠阻止浏览器缩放，因此只在画布滚动区注册原生监听。
    scroll.addEventListener("wheel", onWheel, { passive: false });
    return () => {
      scroll.removeEventListener("wheel", onWheel);
      pendingAnchorRef.current = undefined;
    };
  }, [canvasRef, scopeKey, setZoom]);

  return scrollRef;
}
