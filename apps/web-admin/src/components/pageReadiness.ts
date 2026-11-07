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

/** 仅在显式提供 PageReadiness 的边界中登记等待；路由只为模块加载提供该上下文，页面取数独立展示。 */
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
    // 延后一轮任务，等模块加载边界提交或移除后再展示；普通页面取数不会在这里登记。
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
