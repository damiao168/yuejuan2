import { Alert, Button, Progress, Space } from "antd";
import { getPaperImportUserMessage } from "../../api/client";
import type { PaperImportIssue, PaperImportJob, Question } from "../../api/papers";
import { hasBlockingImportIssues, hasNoExamContentDetected, paperImportTeacherProgress, paperImportSummary } from "./materials";
import { PaperImportReviewPanel } from "./PaperImportReviewPanel";
import { importExcludedPageFurniture, importProgressTiming, importRetainedDraftDescription, importReviewDescription, importVisibleIssues, isImportStructureConfirmed } from "./paperImportPresentation";
import type { usePaperImportWorkflow } from "./usePaperImportWorkflow";

type ImportWorkflow = ReturnType<typeof usePaperImportWorkflow>;
type ImportActionsProps = Readonly<{ job: PaperImportJob; workflow: ImportWorkflow; onReview: () => void }>;
const rubricRequiredArchetypes = new Set(["structured_steps", "short_constructed", "extended_response", "diagram_graph", "table_experiment"]);

function ReviewRequiredActions({ job, workflow, onReview }: ImportActionsProps) {
  const { updatingImportSources, replaceImportSources, savingImportReview, saveImportReview,
    parsing, invalidReviewRubric, invalidReviewScore, confirmPaperImport } = workflow;
  if (isImportStructureConfirmed(job)) return <Button onClick={onReview}>核对小题与分值</Button>;
  if (hasNoExamContentDetected(job)) return <Space wrap>
    <Button type="primary" loading={updatingImportSources} onClick={() => void replaceImportSources(job, job.sources)}>重新识别</Button>
    <Button onClick={onReview}>手动补充题目</Button>
  </Space>;
  const hasPaperMaterial = job.sources.some((source) => source.detected_role === "question" || source.detected_role === "mixed");
  return <Space>
    <Button onClick={onReview}>逐题核对</Button>
    <Button loading={savingImportReview} onClick={() => void saveImportReview(job)}>保存草稿</Button>
    {!hasPaperMaterial ? <Button type="primary" loading={parsing} disabled={hasBlockingImportIssues(job) || invalidReviewRubric || invalidReviewScore || workflow.confirmedReviewCount < workflow.reviewDrafts.length}
      onClick={() => void confirmPaperImport(job)}>确认导入</Button> : null}
  </Space>;
}

function ParseRetryActions({ job, workflow, cancelled }: Readonly<{ job: PaperImportJob; workflow: ImportWorkflow; cancelled: boolean }>) {
  return <Space wrap>
    <Button type="primary" loading={workflow.retryingParse} onClick={() => void workflow.retryImportParse(job)}>{cancelled ? "仅解析本次补充" : "仅重新解析"}</Button>
    <Button loading={workflow.updatingImportSources} onClick={() => void workflow.replaceImportSources(job, job.sources)}>重新识别全部</Button>
  </Space>;
}

function PaperImportActions({ job, workflow, onReview }: ImportActionsProps) {
  switch (job.status) {
    case "review_required":
      return <ReviewRequiredActions job={job} workflow={workflow} onReview={onReview} />;
    case "failed":
      if (job.error_code === "ai_parse_failed") return <ParseRetryActions job={job} workflow={workflow} cancelled={false} />;
      if (!job.sources.length) return null;
      return <Button type="primary" loading={workflow.updatingImportSources} onClick={() => void workflow.replaceImportSources(job, job.sources)}>重新识别</Button>;
    case "cancelled":
      if (!job.sources.length) return null;
      return <ParseRetryActions job={job} workflow={workflow} cancelled />;
    case "processing":
      return <Button danger loading={workflow.stoppingImport} onClick={() => workflow.stopPaperImport(job)}>停止识别</Button>;
    default:
      return null;
  }
}

function PaperImportProgress({ job }: Readonly<{ job: PaperImportJob }>) {
  if (job.status !== "processing" && job.status !== "cancelled") return null;
  const progress = paperImportTeacherProgress(job);
  const timing = job.status === "processing" ? importProgressTiming(progress.startedAt) : "";
  let indicator = null;
  if (progress.percent !== undefined) {
    indicator = <Progress percent={progress.percent} status="normal" />;
    if (job.status === "processing") indicator = <Progress percent={progress.percent} format={() => progress.counter ?? (progress.percent + "%")} status="active" />;
  } else if (job.status === "processing") {
    // 未知完成比例时不设置 value；原有动画只负责外观，progress 提供原生进度语义。
    indicator = <div className="paper-import-progress-indeterminate">
      <progress aria-label="正在处理，暂无可计算的完成比例" style={{ width: "100%", height: "100%", opacity: 0 }} />
      <span aria-hidden="true" />
    </div>;
  }
  return <div className="paper-import-progress" aria-live="polite">
    <div><strong>{progress.label}</strong><span>{progress.detail}{timing ? " · " + timing : ""}</span></div>
    {progress.updatedAt && <span className="muted">最近更新：{new Date(progress.updatedAt).toLocaleTimeString("zh-CN", {hour12:false})}</span>}
    {indicator}
  </div>;
}

function RetainedDraftNotice({ job, count }: Readonly<{ job: PaperImportJob; count: number }>) {
  if (job.status === "applied" || job.status === "review_required" || !count) return null;
  return <Alert type={job.status === "failed" ? "warning" : "info"} showIcon
    message={"上一轮已识别的 " + count + " 道题目仍然保留"} description={importRetainedDraftDescription(job.status)} />;
}

function DraftResults({ job, workflow, existingQuestions }: Readonly<{ job: PaperImportJob; workflow: ImportWorkflow; existingQuestions?: Question[] }>) {
  if (job.status === "applied" || isImportStructureConfirmed(job) || !workflow.reviewDrafts.length) return null;
  const summary = paperImportSummary(job);
  return <>
    <div className="paper-import-facts">
      <span><strong>{summary.questions}</strong> 道题目</span>
      <span><strong>{summary.answers}</strong> 个答案</span>
      <span><strong>{summary.solutions}</strong> 份解析</span>
      <span><strong>{job.rubric_candidates?.length ?? 0}</strong> 份评分标准</span>
      <span><strong>{summary.reviewIssues}</strong> 项需核对</span>
      <span>已逐题确认 <strong>{workflow.confirmedReviewCount ?? 0} / {workflow.reviewDrafts.length}</strong></span>
      <span><strong>{job.questions.reduce((sum, item) => sum + item.score, 0)}</strong> 分</span>
    </div>
    <PaperImportReviewPanel job={job} drafts={workflow.reviewDrafts} existingQuestions={existingQuestions} readOnly={job.status !== "review_required" || workflow.savingImportReview}
      onConfirmQuestion={(index) => void workflow.saveImportReview(job, index)}
      onChange={workflow.updateReviewDraft} onOpenSource={(ref) => void workflow.openImportSource(ref.file_asset_id, ref.page_no)} />
  </>;
}

function ExcludedFurnitureIssue({ issue, index, onOpenSource }: Readonly<{
  issue: PaperImportIssue; index: number; onOpenSource: ImportWorkflow["openImportSource"];
}>) {
  const source = issue.source_refs?.[0];
  return <li key={issue.message + "-" + index}>{issue.message}{source?.file_asset_id
    ? <Button type="link" size="small" onClick={() => void onOpenSource(source.file_asset_id, source.page_no)}>核对来源</Button>
    : null}</li>;
}

function ExcludedFurniture({ job, onOpenSource }: Readonly<{ job: PaperImportJob; onOpenSource: ImportWorkflow["openImportSource"] }>) {
  const issues = importExcludedPageFurniture(job);
  if (!issues.length) return null;
  return <details>
    <summary>已自动排除 {issues.length} 项页眉、页脚或水印</summary>
    <ul className="validation-issue-list">{issues.map((issue, index) => <ExcludedFurnitureIssue key={issue.message + "-" + index} issue={issue} index={index} onOpenSource={onOpenSource} />)}</ul>
    <p>原图和 OCR 原文仍保留；如果正文被误排除，请对照来源补充。</p>
  </details>;
}

function AppliedResult({ job, onReview }: Readonly<{ job: PaperImportJob; onReview: () => void }>) {
  if (job.status !== "applied") return null;
  return <div className="paper-import-result">
    <div className="paper-import-facts">
      <span>已写入题目：<strong>{job.questions.length}</strong></span>
      <span>已配置答案：<strong>{job.questions.filter((item) => item.answer_key).length}</strong></span>
      <span>已导入解析：<strong>{job.questions.filter((item) => item.solution).length}</strong></span>
      <span>已配置评分标准：<strong>{job.questions.filter((item) => item.rubric).length}</strong></span>
      <span>已锁定评分标准：<strong>{job.questions.filter((item) => item.rubric?.status === "locked").length}</strong></span>
      <span>仍需处理：<strong>{job.questions.filter((item) => rubricRequiredArchetypes.has(item.assessment_archetype ?? "") && item.rubric?.status !== "locked").length}</strong></span>
    </div>
    <Button type="primary" onClick={onReview}>逐题核对内容与分值</Button>
  </div>;
}

function VisibleIssueAlert({ job }: Readonly<{ job: PaperImportJob }>) {
  const issues = importVisibleIssues(job);
  if (!issues.length || isImportStructureConfirmed(job)) return null;
  const noExamContent = hasNoExamContentDetected(job);
  return <Alert type={job.status === "failed" || noExamContent ? "error" : "warning"} showIcon
    message={noExamContent ? "未识别到考试内容" : "还需完善"}
    description={<ul className="validation-issue-list">{issues.map((issue, index) => <li key={issue.message + "-" + index}>
      <strong>{issue.question_no ? "第" + issue.question_no + "题：" : ""}</strong>
      {getPaperImportUserMessage(issue.message, "考试资料存在需要核对的内容")}{issue.certainty === "suspected" ? "（疑似）" : ""}
    </li>)}</ul>} />;
}

export function PaperImportResults({ job, workflow, existingQuestions, onReview }: Readonly<{
  job: PaperImportJob; workflow: ImportWorkflow; existingQuestions?: Question[]; onReview: () => void;
}>) {
  return <section className="workspace-section paper-import-review">
    <div className="section-head">
      <div><h2>自动识别结果</h2><p>{importReviewDescription(job)}</p></div>
      <PaperImportActions job={job} workflow={workflow} onReview={onReview} />
    </div>
    <PaperImportProgress job={job} />
    <RetainedDraftNotice job={job} count={workflow.reviewDrafts.length} />
    <DraftResults job={job} workflow={workflow} existingQuestions={existingQuestions} />
    <ExcludedFurniture job={job} onOpenSource={workflow.openImportSource} />
    <AppliedResult job={job} onReview={onReview} />
    <VisibleIssueAlert job={job} />
  </section>;
}
