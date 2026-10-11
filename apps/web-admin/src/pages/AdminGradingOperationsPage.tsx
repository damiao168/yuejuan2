import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Button, Input, Progress, Space } from "antd";
import { ArrowRight, FileUp, RefreshCw, Search } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { listAllExams, type Exam } from "../api/exams";
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

export function isAdminGradingOperationExam(exam: Pick<Exam, "status">) {
  return ["ready", "collecting"].includes(exam.status) || isAdminGradingExam(exam);
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

const importStatusLabels: Record<string, string> = { ready: "待导入", collecting: "导入中" };
const awaitingMessages: Record<string, string> = {
  ready: "准备已完成，等待导入答卷",
  collecting: "正在导入和处理答卷"
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

function ExamOperationProgress({ exam, summary }: Readonly<ExamOperation>) {
  const run = summary?.run;
  if (run && run.total_count > 0) {
    const progress = completion(summary);
    const confirmed = (run.auto_confirmed_count ?? 0) + (run.human_confirmed_count ?? 0);
    return <>
      <div className="grading-monitor-progress">
        <div><span>{runStatusLabels[run.status] ?? "阅卷进行中"}</span><strong>{progress}%</strong></div>
        <Progress percent={progress} showInfo={false} status={(run.failed_count ?? 0) > 0 ? "exception" : "normal"} />
        <span>{confirmed} / {run.total_count} 项已确认</span>
      </div>
      <div className="grading-monitor-counts">
        <span><strong>{run.queued_count ?? 0}</strong>处理中</span>
        <span><strong>{run.review_count ?? 0}</strong>待人工</span>
        <span className={(run.failed_count ?? 0) > 0 ? "danger" : ""}><strong>{run.failed_count ?? 0}</strong>失败</span>
      </div>
    </>;
  }
  // 尚无任务或没有正数任务项时保留阶段提示，不能把空任务显示成已完成的进度。
  const importing = ["ready", "collecting"].includes(exam.status);
  return <>
    <div className="grading-monitor-progress">
      <div className="grading-monitor-awaiting"><strong>{awaitingMessages[exam.status] ?? "等待生成阅卷任务"}</strong><span>{importing ? "上传扫描文件，完成页面处理与学生匹配后进入阅卷。" : "进入本场考试后检查评分准备并启动任务。"}</span></div>
    </div>
    <div />
  </>;
}

function AdminGradingOperationRow({ exam, summary, canImport, onNavigate }: Readonly<ExamOperation & {
  canImport: boolean; onNavigate: (path: string) => void;
}>) {
  const importing = ["ready", "collecting"].includes(exam.status);
  return <article className="grading-monitor-row">
    <div className="grading-monitor-exam">
      <Space size="small" wrap>
        <StatusTag tone={examStatusTone(exam.status)}>{importStatusLabels[exam.status] ?? examStatusLabels[exam.status] ?? "阅卷中"}</StatusTag>
        <span title={exam.subject}>{examSubjectLabel(exam.subject)}</span>
      </Space>
      <h2>{exam.name}</h2>
      <span>{exam.class_ids.length} 个班级</span>
    </div>
    <ExamOperationProgress exam={exam} summary={summary} />
    <div className="grading-monitor-actions">
      <Space wrap>
        {importing ? <Button type="primary" icon={<FileUp size={16} />} disabled={!canImport} onClick={() => onNavigate(`/exams/${encodeURIComponent(exam.id)}/capture`)}>{exam.status === "ready" ? "导入答卷" : "继续导入"}</Button> : null}
        {!importing && canImport ? <Button onClick={() => onNavigate(`/exams/${encodeURIComponent(exam.id)}/capture`)}>查看答卷</Button> : null}
        {exam.status !== "ready" ? <Button onClick={() => onNavigate(`/exams/${encodeURIComponent(exam.id)}/grading`)}>任务分配与监控<ArrowRight size={16} /></Button> : null}
      </Space>
    </div>
  </article>;
}

export function AdminGradingOperationsPage({ canImport, onNavigate }: Readonly<{ canImport: boolean; onNavigate: (path: string) => void }>) {
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
      const examResult = await listAllExams();
      const operationExams = examResult.exams.filter(isAdminGradingOperationExam);
      // 待导入和导入中的考试尚无评分任务；只为阅卷、复核阶段读取进度，单场失败不影响其他考试。
      const summaries = await Promise.allSettled(operationExams.map((exam) => isAdminGradingExam(exam) ? getScoringSummary(exam.id).then((result) => result.scoring_summary) : Promise.resolve(undefined)));
      setOperations(operationExams.map((exam, index) => ({
        exam,
        summary: summaries[index].status === "fulfilled" ? summaries[index].value : undefined
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
          <p>导入学生答卷，查看阅卷进度、分配任务和处理异常。</p>
        </div>
        <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button>
      </section>

      {error ? <Alert type="error" showIcon message="部分数据刷新失败" description={error} /> : null}
      {warnings.map((warning) => <Alert key={warning} type="warning" showIcon message={warning} />)}

      {operations.length ? (
        <>
          <section className="grading-monitor-toolbar">
            <Input prefix={<Search size={16} />} value={keyword} allowClear placeholder="搜索考试或学科" onChange={(event) => setKeyword(event.target.value)} />
            <span>{filtered.length} 场考试</span>
          </section>

          <section className="grading-monitor-list" aria-label="阅卷管理列表">
            {filtered.length ? filtered.map(({ exam, summary }) => <AdminGradingOperationRow key={exam.id} exam={exam} summary={summary} canImport={canImport} onNavigate={onNavigate} />) : <EmptyState title="没有匹配的考试" description="请尝试其他考试名称或学科。" />}
          </section>
        </>
      ) : (
        <EmptyState title="暂无待导入或阅卷中的考试" description="确认考试准备完成后，即可在这里导入答卷；考试准备和配置请前往“考试列表”。" />
      )}
    </div>
  );
}
