import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { Button, Space } from "antd";
import { ArrowLeft, RefreshCw } from "lucide-react";
import { getManagementExam, type ManagementExam, type ManagementExamKind } from "../../../api/exams";
import { getUserErrorMessage } from "../../../api/client";
import { listClasses, listGrades } from "../../../api/org";
import { ErrorState, LoadingState } from "../../../components/PageState";
import { defaultExamWorkspaceRoute } from "../workspace/defaultExamWorkspaceRoute";
import { ManagementExamDetails } from "./ManagementExamDetails";

export function ManagementExamOverviewPage({ kind, examId, onNavigate }: Readonly<{
  kind: ManagementExamKind;
  examId: string;
  onNavigate: (path: string) => void;
}>) {
  const [exam, setExam] = useState<ManagementExam | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [names, setNames] = useState<{ grade?: string; classes: Map<string, string> }>({ classes: new Map() });
  const [generation, setGeneration] = useState(0);
  const refresh = useCallback(() => setGeneration((value) => value + 1), []);
  // 刷新序号是读取身份的一部分；相同考试重试也要淘汰前一轮迟到的结果。
  const request = useMemo(() => ({ kind, examId, generation }), [kind, examId, generation]);

  useEffect(() => {
    let active = true;
    setExam(null);
    setError(null);
    setLoading(true);
    void (async () => {
      try {
        const result = await getManagementExam(request.kind, request.examId);
        if (!active) return;
        setExam(result.exam);
        // 组织名称只是展示补充；缺少组织目录权限不应阻止访问已授权的考试。
        const results = await Promise.allSettled([listGrades(result.exam.school_id), listClasses()]);
        if (!active) return;
        setNames({
          grade: results[0].status === "fulfilled" ? results[0].value.grades.find((item) => item.id === result.exam.grade_id)?.name : undefined,
          classes: new Map(results[1].status === "fulfilled" ? results[1].value.classes.map((item) => [item.id, item.name]) : [])
        });
      } catch (currentError) {
        if (active) setError(getUserErrorMessage(currentError, "整场考试加载失败"));
      } finally {
        if (active) setLoading(false);
      }
    })();
    return () => { active = false; };
  }, [request]);

  // 加载和错误优先于详情；页头始终保留，重试仍沿用同一读取流程。
  let content: ReactNode = null;
  if (loading) {
    content = <LoadingState label="正在读取整场考试" />;
  } else if (error) {
    content = <ErrorState message={error} onRetry={refresh} />;
  } else if (exam) {
    content = <section className="workspace-section management-exam-overview-section">
      <ManagementExamDetails exam={exam} layout="columns" gradeName={names.grade} classNames={names.classes.size ? names.classes : undefined} onOpenSubject={(subject) => onNavigate(defaultExamWorkspaceRoute(subject))} />
    </section>;
  }

  return <div className="page-stack">
    <section className="page-heading"><div><h1>{exam?.name ?? "考试工作区"}</h1><p>查看整场考试进度，选择学科进入配置或后续业务。</p></div><Space><Button icon={<ArrowLeft size={16} />} onClick={() => onNavigate("/exams")}>考试列表</Button><Button icon={<RefreshCw size={16} />} loading={loading} onClick={refresh}>刷新</Button></Space></section>
    {content}
  </div>;
}
