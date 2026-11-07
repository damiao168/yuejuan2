/** 只接受学生门户内的返回地址；登录链接不能把认证后的浏览器带到站外或管理页。 */
export function studentPortalDestination(search: string, schoolCode = ""): string {
  const fallback = "/student/";
  const candidate = new URLSearchParams(search).get("return_to") || fallback;
  let destination: URL;
  try {
    if (candidate.length > 2048 || !candidate.startsWith("/student/") || /[\\\r\n\t]/.test(candidate)) throw new Error("invalid return path");
    destination = new URL(candidate, "https://login.invalid");
    if (destination.origin !== "https://login.invalid" || !destination.pathname.startsWith("/student/")) throw new Error("invalid return path");
  } catch {
    destination = new URL(fallback, "https://login.invalid");
  }
  // 学号按学校定位；机构码可能与学校码不同，不能用登录响应里的 tenant_code 覆盖它。
  if (schoolCode.trim()) destination.searchParams.set("school", schoolCode.trim());
  return `${destination.pathname}${destination.search}${destination.hash}`;
}

export function openStudentPortal(destination: string): void {
  // 两个门户是独立构建，须重载入口；SPA 路由切换仍会留在教师端脚本里。
  if (window.location.port === "5173" || window.location.port === "4173") {
    const target = new URL(destination, window.location.origin);
    target.port = window.location.port === "5173" ? "5174" : "4174";
    window.location.replace(target.href);
  } else {
    window.location.replace(destination);
  }
}
