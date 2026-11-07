import type { ReactNode } from "react";
import { ErrorState, LoadingState } from "../components/PageState";
import { hasEveryPermission, sessionAuthorizationKey, type SessionUser } from "../auth/session";
import type { ProductExperience } from "../router/experience";
import { useExamWorkspace } from "../query/examWorkspace";
import { getUserErrorMessage } from "../api/client";
import { ExamWorkspaceLayout } from "../features/exams/workspace/ExamWorkspaceLayout";

export function ExamWorkspacePage({ examId, section, moduleContent, onNavigate, currentUser, experience }: Readonly<{
  examId: string;
  section: string;
  experience: ProductExperience;
  currentUser: SessionUser;
  moduleContent?: ReactNode | ((readOnly: boolean) => ReactNode);
  onNavigate: (path: string) => void;
}>) {
  const { data, error, isPending, refetch } = useExamWorkspace(examId, sessionAuthorizationKey(currentUser));

  if (!data && isPending) return <LoadingState label="正在加载考试工作区" />;
  // 后台重新授权失败时，缓存投影不能继续挂载小题、答卷或成绩等业务模块。
  if (error) return <ErrorState message={getUserErrorMessage(error, "考试工作区加载失败")} onRetry={() => void refetch()} />;
  if (!data) return null;

  return <ExamWorkspaceLayout
    data={data}
    section={section}
    moduleContent={typeof moduleContent === "function" ? moduleContent(Boolean(data.read_only) || data.exam_status === "archived") : moduleContent}
    onNavigate={onNavigate}
    canReadReadiness={hasEveryPermission(currentUser, ["exam:manage"])}
    canManageScores={experience === "admin" && hasEveryPermission(currentUser, ["score:manage", "exam:manage", "submission:manage"])}
    canReadGrading={experience === "admin" && hasEveryPermission(currentUser, ["review:manage"])}
  />;
}
