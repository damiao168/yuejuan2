import { PortalApiError } from "./api";

type PortalErrorContext = "result" | "question" | "appeal" | "image" | "login";

function notFoundErrorMessage(context: PortalErrorContext) {
  switch (context) {
    case "question": return "本题详情暂不可用，考试总分仍可查看。请重试本题或返回成绩。";
    case "appeal": return "未找到这条复核申请，请刷新我的申请。";
    case "image": return "本页图像暂不可用，题目文字反馈仍可查看。";
    default: return "学校尚未发布这场考试的成绩。";
  }
}

// 同一 HTTP 状态可表示不同业务失败；先按公开错误代码解释，不能把截止或重复申请当成断网。
export function friendlyError(reason: unknown, context: PortalErrorContext = "result") {
  if (reason instanceof PortalApiError) {
    switch (reason.code) {
      case "invalid_credentials": return "学校代码、账号或密码不正确，请核对学校发放的登录信息。";
      case "login_rate_limited": return "登录尝试过于频繁，请稍后再试；如忘记密码，请联系学校管理员。";
      case "student_score_scope_required":
      case "student_scope_required": return "学生账号尚未关联本人学籍，请联系学校管理员核对账号与学号。";
      case "appeal_window_closed": return "本版成绩的复核窗口已截止，不能再提交；说明已保留，可联系学校管理员。";
      case "appeal_already_filed": return "本题在这一版成绩下已经提交过复核申请，请查看已有申请记录。";
      case "student_release_changed": return "学校已更新当前成绩。原版本草稿已保留，请查看当前成绩并重新核对题目后再申请复核。";
      case "appeal_source_release_invalid": return "申请对应的成绩版本已不可用。请先查看当前成绩，草稿仍按原版本保留。";
      case "invalid_appeal_input": return "复核说明或圈选区域不符合要求，请核对 10—500 字的说明及所选区域。";
      case "appeal_resource_not_found": return "暂未找到这条复核记录，请刷新申请记录后再查看。";
      case "student_question_unavailable": return "本题详情暂不可用，考试总分仍可查看。请重试本题或返回成绩。";
    }
    if (reason.status === 401) return "登录状态已失效，请重新登录；未提交的复核草稿会在同一账号登录后恢复。";
    if (reason.status === 403) return "你无权查看此内容，请联系学校管理员核对账号权限。";
    if (reason.status === 404) return notFoundErrorMessage(context);
    if (reason.status === 429) return "请求过于频繁，请稍后重试。";
  }
  return "暂时无法连接服务，请稍后重试。";
}

export function sessionExpired(reason: unknown) {
  return reason instanceof PortalApiError && reason.status === 401 && reason.code !== "invalid_credentials";
}

export function studentReleaseChanged(reason: unknown) {
 return reason instanceof PortalApiError && reason.code === "student_release_changed";
}
