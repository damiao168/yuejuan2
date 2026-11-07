export type StudentPage = { kind: "home" } | { kind: "exam"; examID: string };

export const STUDENT_NAVIGATION_EVENT = "edugrade:student-navigation";

export function studentBasePath(): string {
  // 生产包挂在 /student/，开发服务挂在根；按钮导航不能把学生带进教师端的根路由。
  return import.meta.env.BASE_URL === "/student/" || /^\/student(?:\/|$)/.test(window.location.pathname) ? "/student/" : "/";
}

function legacyStudentLocation(): string | undefined {
  const { pathname, search, hash, origin } = window.location;
  // 只有根入口的旧书签需要迁移；考试页片段、协议相对地址和反斜杠不能覆盖当前页面。
  const base = studentBasePath();
  if (pathname !== base && pathname !== `${base}index.html`) return undefined;
  if (!/^#\/(?![\\/])/.test(hash) || /[\\\r\n]/.test(hash)) return undefined;
  const target = new URL(hash.slice(1), origin);
  if (target.origin !== origin) return undefined;
  if (base !== "/" && !target.pathname.startsWith(base)) target.pathname = `${base}${target.pathname.slice(1)}`;
  const params = new URLSearchParams(search);
  target.searchParams.forEach((value, key) => params.set(key, value));
  const query = params.toString();
  return `${target.pathname}${query ? `?${query}` : ""}${target.hash}`;
}

export function studentLocation(): string {
  return legacyStudentLocation() ?? `${window.location.pathname}${window.location.search}`;
}

export function normalizeStudentLocation(): void {
  const path = legacyStudentLocation();
  if (path) window.history.replaceState(window.history.state, "", path);
}

export function readStudentPage(): StudentPage {
  const path = studentLocation().split(/[?#]/, 1)[0];
  const relativePath = studentBasePath() === "/student/" ? path.replace(/^\/student(?=\/|$)/, "") : path;
  const match = /^\/exams\/([^/?#]+)$/.exec(relativePath);
  if (match) {
    try {
      return { kind: "exam", examID: decodeURIComponent(match[1]) };
    } catch {
      // 坏书签不能阻塞学生查看已发布的考试列表。
      return { kind: "home" };
    }
  }
  return { kind: "home" };
}

function navigateStudent(path: string): void {
  const target = new URL(`${studentBasePath()}${path.replace(/^\//, "")}`, window.location.origin);
  const school = new URLSearchParams(window.location.search).get("school");
  if (school) target.searchParams.set("school", school);
  window.history.pushState(window.history.state, "", `${target.pathname}${target.search}`);
  // pushState 不触发 popstate；同页按钮和浏览器前进后退共用 App 的回执保护。
  window.dispatchEvent(new Event(STUDENT_NAVIGATION_EVENT));
}

export function goHome(): void { navigateStudent("/"); }
export function goExam(examID: string): void { navigateStudent(`/exams/${encodeURIComponent(examID)}`); }
