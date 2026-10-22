import { createContext, useContext, useLayoutEffect } from "react";

export interface PageReadiness {
  registerLoading: () => () => void;
}

export const PageReadinessContext = createContext<PageReadiness | null>(null);
export const PageVisibilityContext = createContext(true);

/** 候选页已挂载但尚未展示时，自动打开的 body 浮层也必须保持关闭。 */
export function usePageVisible() {
  return useContext(PageVisibilityContext);
}

/** 首屏加载状态参与切页等待；脱离路由容器时仍按普通加载组件显示。 */
export function usePageLoading(loading = true) {
  const readiness = useContext(PageReadinessContext);
  useLayoutEffect(() => loading ? readiness?.registerLoading() : undefined, [readiness, loading]);
}

export function createPageReadiness() {
  const blockers = new Set<symbol>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let complete: (() => void) | undefined;

  const cancel = () => { clearTimeout(timer); timer = undefined; };
  const schedule = () => {
    cancel();
    if (!complete || blockers.size) return;
    // 延后一轮任务，让首屏 effect 发起取数、父子加载组件交接后再判断，避免只等到模块就提前切页。
    timer = setTimeout(() => {
      if (!complete || blockers.size) return;
      const onReady = complete;
      complete = undefined;
      onReady();
    }, 0);
  };

  return {
    registerLoading() {
      const token = Symbol();
      blockers.add(token);
      cancel();
      return () => { blockers.delete(token); schedule(); };
    },
    start(onReady: () => void) { complete = onReady; schedule(); },
    stop() { complete = undefined; cancel(); }
  };
}
