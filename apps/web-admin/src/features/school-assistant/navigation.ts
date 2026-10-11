import type { SchoolAssistantAction } from "../../api/schoolAssistant";
import type { SessionUser } from "../../auth/session";
import { hasPathAccess } from "../../router/navigation";
import { hasExamWorkspaceSectionAccess } from "../../router/routes";

export function canUseSchoolAssistant(user: SessionUser) {
  return user.roles.includes("school_admin") && !user.roles.includes("platform_admin");
}

export function assistantActionPath(action: SchoolAssistantAction, examId: string | undefined, user: SessionUser): string | null {
  // 服务端返回的是结构化目标，不能直接把模型文本或任意 URL 当作跳转；也不能跳到另一个考试。
  if (!canUseSchoolAssistant(user) || action.target.kind !== "exam_section" || !examId || action.target.exam_id !== examId
    || !hasExamWorkspaceSectionAccess("admin", action.target.section)) return null;
  const path = `/exams/${encodeURIComponent(examId)}/${action.target.section}`;
  return hasPathAccess(user, path) ? path : null;
}
