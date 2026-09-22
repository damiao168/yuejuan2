import type { ReactNode } from "react";
import { ErrorState, LoadingState } from "../components/PageState";
import type { SessionUser } from "../auth/session";
import type { ProductExperience } from "../router/experience";
import { useExamWorkspace } from "../query/examWorkspace";
import { getUserErrorMessage } from "../api/client";
import { ExamWorkspaceLayout } from "../features/exams/workspace/ExamWorkspaceLayout";

export function ExamWorkspacePage({ examId, section, moduleContent, onNavigate }: {
  examId: string;
  section: string;
  experience: ProductExperience;
  currentUser: SessionUser;
  moduleContent?: ReactNode;
  onNavigate: (path: string) => void;
}) {
  const { data, error, isPending, refetch } = useExamWorkspace(examId);

  if (!data && isPending) return <LoadingState label="正在加载考试工作区" />;
  if (!data && error) return <ErrorState message={getUserErrorMessage(error, "考试工作区加载失败")} onRetry={() => void refetch()} />;
  if (!data) return null;

  return <ExamWorkspaceLayout
    data={data}
    section={section}
    moduleContent={moduleContent}
    onNavigate={onNavigate}
  />;
}
