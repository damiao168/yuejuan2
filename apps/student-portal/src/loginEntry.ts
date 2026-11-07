import { studentLocation } from "./navigation";

export function unifiedLoginURL(): string {
  const current = new URL(studentLocation(), window.location.origin);
  const destination = current.pathname.startsWith("/student/") ? current.pathname : `/student${current.pathname === "/" ? "/" : current.pathname}`;
  const login = new URL("/", window.location.origin);
  // 独立 Vite 学生服务没有统一登录页面；开发/预览分别交给教师端对应端口，生产保持同源 Cookie。
  if (import.meta.env.DEV && login.port === "5174") login.port = "5173";
  else if (login.port === "4174") login.port = "4173";
  const school = current.searchParams.get("school")?.trim() || current.searchParams.get("tenant")?.trim();
  if (school) login.searchParams.set("tenant", school);
  login.searchParams.set("return_to", `${destination}${current.search}`);
  return login.href;
}

export function leaveForUnifiedLogin(target: string): void {
  window.location.replace(target);
}
