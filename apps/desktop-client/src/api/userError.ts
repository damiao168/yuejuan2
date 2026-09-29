export const DEFAULT_USER_ERROR_MESSAGE = "操作失败，请稍后重试。";
export const NETWORK_USER_ERROR_MESSAGE = "网络连接异常，请检查网络后重试。";

const apiErrorMessages: Record<string, string> = {
  exam_not_collecting: "当前考试尚未进入答卷采集阶段，请先完成考试准备并开始采集。",
  exam_not_found: "考试不存在或已被删除。",
  access_scope_missing: "当前账号没有访问这些数据的权限。",
  capture_not_found: "采集记录不存在或已被删除。",
  capture_invalid_input: "采集信息有误，请检查后重试。",
  capture_invalid_transition: "当前采集状态不能执行此操作，请刷新页面查看最新状态。",
  capture_revision_conflict: "采集记录已被其他操作更新，请刷新页面后重试。",
  capture_duplicate_file: "该文件已加入当前批次，无需重复导入。",
  capture_operation_failed: "采集操作失败，请稍后重试。",
  resource_version_conflict: "任务已被其他人更新，请刷新页面后重试。",
  operation_in_progress: "操作正在处理中，请稍后查看结果。",
  csrf_validation_failed: "页面安全状态已失效，请刷新页面后重试。",
  invalid_credentials: "学校代码、账号或密码不正确。",
  unauthenticated: "登录状态已失效，请重新登录。"
};

const httpErrorMessages: Record<number, string> = {
  400: "提交内容有误，请检查后重试。",
  401: "登录状态已失效，请重新登录。",
  403: "当前账号没有执行此操作的权限。",
  404: "请求的内容不存在或已被删除。",
  409: "当前数据状态已发生变化，请刷新后重试。",
  413: "上传内容过大，请调整文件后重试。",
  415: "文件格式不受支持，请更换文件后重试。",
  422: "提交内容暂时无法处理，请检查设置后重试。",
  429: "操作过于频繁，请稍后再试。"
};

export function getApiErrorMessage(code: string | undefined, status: number | undefined): string {
  if (code && apiErrorMessages[code]) return apiErrorMessages[code];
  if (status && status >= 500 && status <= 599) return "系统暂时无法完成操作，请稍后重试。";
  if (status && httpErrorMessages[status]) return httpErrorMessages[status];
  return DEFAULT_USER_ERROR_MESSAGE;
}

export function getUserErrorMessage(error: unknown, fallback?: string): string {
  if (isApiClientError(error)) return getApiErrorMessage(error.code, error.status);
  if (error instanceof TypeError && /failed to fetch|fetch failed|network(?:error| request failed| disconnected)|load failed/i.test(error.message)) {
    return NETWORK_USER_ERROR_MESSAGE;
  }
  if (error instanceof DOMException && error.name === "AbortError") return "操作已取消，请重新发起。";
  if (error instanceof Error && /[\u3400-\u9fff]/u.test(error.message)) return error.message;
  return fallback && /[\u3400-\u9fff]/u.test(fallback) ? fallback : DEFAULT_USER_ERROR_MESSAGE;
}

export function getSafeUserText(value: unknown, fallback: string): string {
  // 这里只判断是否有可读中文，不是 HTML 清洗；返回值仍应作为普通文本渲染。
  return typeof value === "string" && /[\u3400-\u9fff]/u.test(value) ? value : fallback;
}

function isApiClientError(error: unknown): error is { status: number; code: string } {
  if (!error || typeof error !== "object") return false;
  const candidate = error as { name?: unknown; status?: unknown; code?: unknown };
  return candidate.name === "ApiClientError"
    && typeof candidate.status === "number"
    && typeof candidate.code === "string";
}
