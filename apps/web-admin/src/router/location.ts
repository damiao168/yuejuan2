type RouteLocation = Pick<Location, "pathname" | "search" | "hash" | "origin">;

export function legacyHashRoute(location: RouteLocation): string | undefined {
  // 只兼容旧站内路由；脚注、PDF 页码和协议相对地址不能被改成业务页面。
  if (!/^#\/(?![\\/])/.test(location.hash) || /[\\\r\n]/.test(location.hash)) return undefined;
  if (location.pathname !== "/" && location.pathname !== "/index.html") return undefined;
  const target = new URL(location.hash.slice(1), location.origin);
  if (target.origin !== location.origin) return undefined;
  const params = new URLSearchParams(location.search);
  target.searchParams.forEach((value, key) => params.set(key, value));
  const query = params.toString();
  return `${target.pathname}${query ? `?${query}` : ""}${target.hash}`;
}

export function migrateLegacyHashRoute(): void {
  const path = legacyHashRoute(window.location);
  // 旧入口只替换当前历史位置，后退不会再次进入同一个 # 地址；已初始化的 Browser History 也会收到替换通知。
  if (path) window.history.replaceState(window.history.state, "", path);
}

export function listenForLegacyHashRoutes(): () => void {
  // 根登录页打开后再点击旧书签属于同文档导航，不会重新执行模块；只监听此兼容入口，不接管业务页锚点。
  window.addEventListener("hashchange", migrateLegacyHashRoute);
  return () => window.removeEventListener("hashchange", migrateLegacyHashRoute);
}

export function currentRoutePath(): string {
  return legacyHashRoute(window.location) ?? `${window.location.pathname}${window.location.search}`;
}
