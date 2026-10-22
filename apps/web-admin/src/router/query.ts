import { currentRoutePath } from "./location";
export { currentRoutePath } from "./location";

export function routeSearchParams(): URLSearchParams {
  return new URL(currentRoutePath(), window.location.origin).searchParams;
}

export function routeQueryParam(name: string): string {
  return routeSearchParams().get(name)?.trim() ?? "";
}

// 旧模块沿用此导出名；查询来源已统一为路径参数，旧 # 入口只在兼容层处理。
export const hashQueryParam = routeQueryParam;

export function replaceRouteQuery(update: (params: URLSearchParams) => void): void {
  const target = new URL(currentRoutePath(), window.location.origin);
  update(target.searchParams);
  // 页面内筛选已自行处理未保存确认，只更新恢复地址，不新增一次路由离开。
  window.history.replaceState(window.history.state, "", `${target.pathname}${target.search}${target.hash}`);
}
