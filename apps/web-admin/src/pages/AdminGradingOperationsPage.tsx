import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Button, Input, Progress, Space } from "antd";
import { ArrowRight, RefreshCw, Search } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { listExams, type Exam } from "../api/exams";
import { getScoringSummary, type ScoringSummary } from "../api/review";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { examStatusLabels, examStatusTone, examSubjectLabel } from "../constants/examStatus";

interface ExamOperation {
  exam: Exam;
  summary?: ScoringSummary;
}

const adminGradingStatuses = new Set(["grading", "reviewing"]);

export function isAdminGradingExam(exam: Pick<Exam, "status">) {
  return adminGradingStatuses.has(exam.status);
}

const runStatusLabels: Record<string, string> = {
  queued: "等待处理",
  processing: "自动评分中",
  running: "自动评分中",
  needs_review: "等待人工处理",
  completed: "评分完成",
  failed: "存在处理失败项",
  cancelling: "正在取消",
  cancelled: "已取消"
};

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.warn("阅卷管理请求失败", error.status, error.code);
  }
  return getUserErrorMessage(error, "阅卷管理数据加载失败");
}

function completion(summary?: ScoringSummary) {
  const run = summary?.run;
  if (!run || run.total_count <= 0) return 0;
  return Math.round(((run.auto_confirmed_count + run.human_confirmed_count) / run.total_count) * 100);
}

export function AdminGradingOperationsPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const [operations, setOperations] = useState<ExamOperation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [warnings, setWarnings] = useState<string[]>([]);
  const [keyword, setKeyword] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    setWarnings([]);
    try {
      const examResult = await listExams();
      const gradingExams = examResult.exams.filter(isAdminGradingExam);
      const summaries = await Promise.allSettled(gradingExams.map((exam) => getScoringSummary(exam.id)));
      setOperations(gradingExams.map((exam, index) => ({
        exam,
        summary: summaries[index].status === "fulfilled" ? summaries[index].value.scoring_summary : undefined
      })));
      const unavailable = summaries.filter((result) => result.status === "rejected" && !(result.reason instanceof ApiClientError && result.reason.status === 404)).length;
      if (unavailable > 0) setWarnings([`${unavailable} 场考试的阅卷进度暂时不可用`]);
    } catch (loadError) {
      setError(formatError(loadError));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const filtered = useMemo(() => {
    const query = keyword.trim().toLowerCase();
    return operations.filter(({ exam }) => !query
      || exam.name.toLowerCase().includes(query)
      || examSubjectLabel(exam.subject).toLowerCase().includes(query));
  }, [keyword, operations]);

  if (!operations.length && loading) return <LoadingState label="正在加载阅卷管理" />;
  if (!operations.length && error) return <ErrorState message={error} onRetry={() => void load()} />;

  return (
    <div className="page-stack admin-grading-monitor">
      <section className="page-heading">
        <div>
          <h1>阅卷管理</h1>
          <p>只显示已进入阅卷或复核阶段的考试，用于查看进度、分配任务和处理异常。</p>
        </div>
        <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button>
      </section>

      {error ? <Alert type="error" showIcon message="部分数据刷新失败" description={error} /> : null}
      {warnings.map((warning) => <Alert key={warning} type="warning" showIcon message={warning} />)}

      {operations.length ? (
        <>
          <section className="grading-monitor-toolbar">
            <Input prefix={<Search size={16} />} value={keyword} allowClear placeholder="搜索考试或学科" onChange={(event) => setKeyword(event.target.value)} />
            <span>{filtered.length} 场阅卷中考试</span>
          </section>

          <section className="grading-monitor-list" aria-label="阅卷管理列表">
            {filtered.length ? filtered.map(({ exam, summary }) => {
              const run = summary?.run;
              const progress = completion(summary);
              const confirmed = (run?.auto_confirmed_count ?? 0) + (run?.human_confirmed_count ?? 0);
              const hasProgress = Boolean(run && run.total_count > 0);
              return (
                <article className="grading-monitor-row" key={exam.id}>
                  <div className="grading-monitor-exam">
                    <Space size="small" wrap>
                      <StatusTag tone={examStatusTone(exam.status)}>{examStatusLabels[exam.status] ?? "阅卷中"}</StatusTag>
                      <span title={exam.subject}>{examSubjectLabel(exam.subject)}</span>
                    </Space>
                    <h2>{exam.name}</h2>
                    <span>{exam.class_ids.length} 个班级</span>
                  </div>

                  <div className="grading-monitor-progress">
                    {hasProgress ? (
                      <>
                        <div><span>{runStatusLabels[run!.status] ?? "阅卷进行中"}</span><strong>{progress}%</strong></div>
                        <Progress percent={progress} showInfo={false} status={(run?.failed_count ?? 0) > 0 ? "exception" : "normal"} />
                        <span>{confirmed} / {run!.total_count} 项已确认</span>
                      </>
                    ) : (
                      <div className="grading-monitor-awaiting"><strong>等待生成阅卷任务</strong><span>进入本场考试后检查评分准备并启动任务。</span></div>
                    )}
                  </div>

                  {hasProgress ? (
                    <div className="grading-monitor-counts">
                      <span><strong>{run?.queued_count ?? 0}</strong>处理中</span>
                      <span><strong>{run?.review_count ?? 0}</strong>待人工</span>
                      <span className={(run?.failed_count ?? 0) > 0 ? "danger" : ""}><strong>{run?.failed_count ?? 0}</strong>失败</span>
                    </div>
                  ) : <div />}

                  <div className="grading-monitor-actions">
                    <Button onClick={() => onNavigate(`/exams/${encodeURIComponent(exam.id)}/grading`)}>任务分配与监控<ArrowRight size={16} /></Button>
                  </div>
                </article>
              );
            }) : <EmptyState title="没有匹配的考试" description="请尝试其他考试名称或学科。" />}
          </section>
        </>
      ) : (
        <EmptyState title="暂无阅卷中的考试" description="考试进入阅卷或复核阶段后才会显示；考试准备和配置请前往“考试列表”。" />
      )}
    </div>
  );
}
