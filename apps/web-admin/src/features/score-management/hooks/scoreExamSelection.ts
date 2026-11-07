import { currentRoutePath, replaceRouteQuery, routeQueryParam } from "../../../router/query";

export const SCORE_EXAM_SELECTION_PREFIX = "edugrade.score-exam.";

export function isScoreExamSelectionLocation(): boolean {
  const path = new URL(currentRoutePath(), window.location.origin).pathname;
  return /^\/(?:admin|teacher)\/scores\/?$/.test(path);
}

export function readScoreExamSelection(scope: string): string {
  if (!isScoreExamSelectionLocation()) return "";
  const requested = routeQueryParam("exam");
  if (requested) return requested;
  try {
    return window.sessionStorage.getItem(`${SCORE_EXAM_SELECTION_PREFIX}${scope}`)?.trim() ?? "";
  } catch {
    return "";
  }
}

export function rememberScoreExamSelection(scope: string, examId: string): void {
  // 切换页面时旧组件可能仍在等待新页面就绪，不能把它的考试写进目标页面地址。
  if (!isScoreExamSelectionLocation()) return;
  if (routeQueryParam("exam") !== examId) replaceRouteQuery((params) => params.set("exam", examId));
  try {
    // 只记当前标签页的选择；作用域由调用方按租户、用户和产品身份生成，不保存成绩数据。
    window.sessionStorage.setItem(`${SCORE_EXAM_SELECTION_PREFIX}${scope}`, examId);
  } catch {
    // 浏览器禁用存储时，地址仍支持后退和刷新恢复。
  }
}

export function clearRememberedScoreExamSelection(scope: string): void {
  try { window.sessionStorage.removeItem(`${SCORE_EXAM_SELECTION_PREFIX}${scope}`); } catch { /* 本次仍按权威列表重新选取，不能凭旧记忆读取业务。 */ }
}
