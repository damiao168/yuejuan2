import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { Drawer, type DrawerProps } from "antd";

/** 模型列表使用 html 滚动；侧栏锁定这个滚动源，并保留 Drawer 原有的焦点管理。 */
export function ModelConfigDrawer({ open, afterOpenChange, rootStyle, styles, ...props }: Omit<DrawerProps, "getContainer">) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null);
  const unlockRef = useRef<((restorePosition: boolean) => void) | null>(null);

  useLayoutEffect(() => {
    // rc-drawer 默认只锁 body；专用 portal 避免它修改 body 的 overflow 和宽度，由下方锁实际滚动源。
    const node = document.createElement("div");
    document.body.appendChild(node);
    setContainer(node);
    return () => {
      // 离开页面只解锁，不能把旧列表的位置写回接下来的页面。
      unlockRef.current?.(false);
      unlockRef.current = null;
      node.remove();
    };
  }, []);

  useLayoutEffect(() => {
    if (!open || unlockRef.current) return;
    const root = document.documentElement;
    const x = window.scrollX;
    const y = window.scrollY;
    const overflowX = root.style.overflowX;
    const overflowY = root.style.overflowY;
    root.style.overflowX = "hidden";
    root.style.overflowY = "hidden";
    // 一直锁到关闭动画结束；提前解锁会让动画期间的滚轮移动背景列表。
    unlockRef.current = (restorePosition) => {
      root.style.overflowX = overflowX;
      root.style.overflowY = overflowY;
      if (restorePosition) window.scrollTo({ left: x, top: y, behavior: "instant" });
    };
  }, [open]);

  const handleOpenChange = useCallback((visible: boolean) => {
    if (!visible) {
      unlockRef.current?.(true);
      unlockRef.current = null;
    }
    afterOpenChange?.(visible);
  }, [afterOpenChange]);

  if (!container) return null;
  return <Drawer {...props} open={open} getContainer={container}
    styles={{ ...styles, body: { ...styles?.body, overscrollBehavior: "contain" } }}
    rootStyle={{ ...rootStyle, position: "fixed" }} afterOpenChange={handleOpenChange} />;
}
