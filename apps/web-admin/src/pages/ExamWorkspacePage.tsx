import { useEffect, useState, type ReactNode } from "react";
import { ErrorState, LoadingState } from "../components/PageState";
import type { SessionUser } from "../auth/session";
import type { ProductExperience } from "../router/experience";
import { useExamWorkspace } from "../query/examWorkspace";
import { getUserErrorMessage } from "../api/client";
import { listExams, type Exam } from "../api/exams";
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
  const [subjectExams, setSubjectExams] = useState<Exam[]>([]);

  useEffect(() => {
    let active = true;
    setSubjectExams([]);
    void listExams({ limit: 200 }).then((response) => {
      const currentExam = response.exams.find((exam) => exam.id === examId);
      if (!currentExam?.exam_session_id) return currentExam ? [currentExam] : [];
      return response.exams.filter((candidate) => candidate.exam_session_id === currentExam.exam_session_id);
    }).then((exams) => {
      if (active) setSubjectExams(exams.length ? exams : []);
    }).catch(() => undefined);
    return () => { active = false; };
  }, [examId]);

  if (!data && isPending) return <LoadingState label="正在加载考试工作区" />;
  if (!data && error) return <ErrorState message={getUserErrorMessage(error, "考试工作区加载失败")} onRetry={() => void refetch()} />;
  if (!data) return null;

  return <ExamWorkspaceLayout
    data={data}
    section={section}
    subjectExams={subjectExams}
    moduleContent={moduleContent}
    onNavigate={onNavigate}
  />;
}
