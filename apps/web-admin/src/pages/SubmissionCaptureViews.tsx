import {
  Alert,
  Button,
  Descriptions,
  List,
  Progress,
  type TableColumnsType,
} from "antd";
import { Eye } from "lucide-react";
import type { Exam } from "../api/exams";
import type { OcrTask, Submission, SubmissionPage } from "../api/submissions";
import { DataTableShell } from "@edugrade/ui";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import type { SubmissionView } from "../features/capture/captureQueries";
import { SubmissionCaptureSummary } from "../features/capture/SubmissionCaptureSummary";
import {
  formatTime,
  ocrLabel,
  qualityStatusLabels,
  submissionStatusLabels,
} from "../features/capture/submissionPresentation";

export interface UploadQueueItem {
  id: string;
  fileName: string;
  status: "processing" | "success" | "error";
  phase: string;
  percent: number;
  error?: string;
}

export interface PreviewState {
  url: string;
  contentType: string;
  filename?: string;
}

export interface OcrDrawerState {
  row: SubmissionView;
  tasks: OcrTask[];
  loading: boolean;
  loadingMore?: boolean;
  error?: string;
}

type CaptureSummary = Parameters<typeof SubmissionCaptureSummary>[0]["summary"];

interface CaptureListState {
  loading: boolean;
  error: string | null;
  exam: Exam | undefined;
  captureLoading: boolean;
  captureError: string | null;
}

interface CaptureTableData {
  rows: SubmissionView[];
  filteredRows: SubmissionView[];
  summary: CaptureSummary;
  columns: TableColumnsType<SubmissionView>;
  hasMoreSubmissions: boolean;
  loadingMore: boolean;
  loadMore: () => Promise<void>;
}

function uploadProgressStatus(status: UploadQueueItem["status"]) {
  if (status === "error") return "exception";
  if (status === "success") return "success";
  return "active";
}

export function UploadQueuePanel({
  queue,
  percent,
}: Readonly<{ queue: UploadQueueItem[]; percent: number }>) {
  if (queue.length === 0) return null;
  return (
    <section className="workspace-section upload-progress-panel">
      <div className="section-head">
        <div>
          <h2>上传队列</h2>
          <p>{queue.filter((item) => item.status === "error").length} 个失败</p>
        </div>
        <Progress className="batch-progress" percent={percent} size="small" />
      </div>
      <List
        className="upload-queue-list"
        dataSource={queue.slice(0, 8)}
        renderItem={(item) => (
          <List.Item className="upload-queue-item">
            <div>
              <strong>{item.fileName}</strong>
              <span>{item.error || item.phase}</span>
            </div>
            <Progress
              percent={item.percent}
              size="small"
              status={uploadProgressStatus(item.status)}
            />
          </List.Item>
        )}
      />
    </section>
  );
}

function captureTableDescription(table: Readonly<CaptureTableData>) {
  let failureText = "";
  if (table.summary.failed > 0) {
    failureText = `；${table.summary.failed} 份处理失败，可直接重试`;
  }
  return `${table.filteredRows.length} / ${table.rows.length} 条记录，已完成 ${table.summary.completed} 份${failureText}`;
}

export function CaptureRecords({
  state,
  table,
  reloadExams,
  reloadCapture,
}: Readonly<{
  state: Readonly<CaptureListState>;
  table: Readonly<CaptureTableData>;
  reloadExams: () => Promise<void>;
  reloadCapture: (examId: string) => Promise<void>;
}>) {
  // 考试加载和错误先于采集状态，避免残留记录掩盖当前考试的读取失败。
  if (state.loading) {
    return (
      <section className="workspace-section">
        <LoadingState label="正在读取考试列表" />
      </section>
    );
  }
  if (state.error) return <ErrorState message={state.error} onRetry={() => void reloadExams()} />;
  const exam = state.exam;
  if (!exam) {
    return (
      <section className="workspace-section">
        <EmptyState title="暂无考试" description="暂无可采集的考试。请先在『考试管理』创建考试并完成开考准备。" />
      </section>
    );
  }
  if (state.captureLoading) {
    return (
      <section className="workspace-section">
        <LoadingState label="正在读取答题卡记录" />
      </section>
    );
  }
  if (state.captureError) return <ErrorState message={state.captureError} onRetry={() => void reloadCapture(exam.id)} />;
  return (
    <DataTableShell title="答题卡处理" description={captureTableDescription(table)}>
      <ResponsiveTable<SubmissionView>
        className="dense-data-table"
        rowKey={(row) => row.submission.id}
        dataSource={table.filteredRows}
        columns={table.columns}
        pagination={{ pageSize: 10, showSizeChanger: false }}
        size="small"
        rowClassName={(row) => (row.detailError ? "row-with-warning" : "")}
        locale={{ emptyText: <EmptyState title="暂无答题卡" description="当前考试还没有答题卡。点击右上角『上传答题卡』导入扫描件。" /> }}
      />
      {table.hasMoreSubmissions ? (
        <Button block loading={table.loadingMore} onClick={() => void table.loadMore()}>
          加载更多答题卡
        </Button>
      ) : null}
    </DataTableShell>
  );
}

function previewTypeLabel(contentType: string) {
  if (contentType === "application/pdf") return "PDF 文档";
  if (contentType.startsWith("image/")) return "扫描图片";
  return "其他文件";
}

function PreviewContent({ preview }: Readonly<{ preview: PreviewState }>) {
  if (preview.contentType.startsWith("image/")) {
    return <img src={preview.url} alt={preview.filename ?? "答题卡页面"} />;
  }
  if (preview.contentType === "application/pdf") {
    return <iframe title={preview.filename ?? "答题卡 PDF"} src={preview.url} />;
  }
  return <Alert type="info" showIcon message="该文件类型不支持内嵌预览" description="可使用新窗口打开或浏览器下载查看。" />;
}

function PagePreview({ preview }: Readonly<{ preview: PreviewState | null }>) {
  if (!preview) return null;
  return (
    <div className="page-preview">
      <div className="section-head">
        <div>
          <h2>{preview.filename ?? "页面预览"}</h2>
          <p title={preview.contentType}>{previewTypeLabel(preview.contentType)}</p>
        </div>
        <Button icon={<Eye size={16} />} onClick={() => window.open(preview.url, "_blank", "noopener,noreferrer")}>
          新窗口打开
        </Button>
      </div>
      <PreviewContent preview={preview} />
    </div>
  );
}

export function SubmissionPageDetails({
  row,
  preview,
  studentName,
  columns,
}: Readonly<{
  row: SubmissionView;
  preview: PreviewState | null;
  studentName: (submission: Submission) => string;
  columns: TableColumnsType<SubmissionPage>;
}>) {
  return (
    <div className="detail-stack">
      <Descriptions bordered size="small" column={2}>
        <Descriptions.Item label="答题卡编号">{row.submission.candidate_no || "暂未生成"}</Descriptions.Item>
        <Descriptions.Item label="学生姓名">{studentName(row.submission)}</Descriptions.Item>
        <Descriptions.Item label="采集状态">{submissionStatusLabels[row.submission.status] ?? "未知状态"}</Descriptions.Item>
        <Descriptions.Item label="质量状态">{qualityStatusLabels[row.submission.quality_status] ?? row.submission.quality_status}</Descriptions.Item>
      </Descriptions>
      <ResponsiveTable<SubmissionPage>
        className="dense-data-table"
        rowKey="id"
        dataSource={row.pages}
        columns={columns}
        pagination={false}
        size="small"
        locale={{ emptyText: <EmptyState title="暂无页面" description="该答题卡还没有关联页面文件。" /> }}
      />
      <PagePreview preview={preview} />
    </div>
  );
}

function OcrResults({ drawer }: Readonly<{ drawer: OcrDrawerState }>) {
  if (drawer.tasks.flatMap((task) => task.results ?? []).length === 0) {
    return <EmptyState title="暂无识别结果" description="识别任务已创建，系统处理完成后结果将显示在这里。" />;
  }
  return (
    <div className="ocr-result-list">
      {drawer.tasks.map((task, index) => (
        <section className="ocr-result-block" key={task.id}>
          <div className="section-head">
            <div>
              <h2 title={task.id}>识别任务 {index + 1}</h2>
              <p>创建于 {formatTime(task.created_at)} · {task.results?.length ?? 0} 条识别结果</p>
            </div>
          </div>
          <ResponsiveTable
            className="dense-data-table"
            rowKey="id"
            dataSource={task.results ?? []}
            pagination={false}
            size="small"
            columns={[
              {
                title: "页面",
                dataIndex: "submission_page_id",
                width: 100,
                render: (value: string) => {
                  const pageNo = drawer.row.pages.find((page) => page.id === value)?.page_no;
                  return <span title={value}>{pageNo ? `第 ${pageNo} 页` : "-"}</span>;
                },
              },
              { title: "文本", dataIndex: "text" },
              { title: "置信度", dataIndex: "confidence", width: 100, render: (value: number) => `${Math.round(value * 100)}%` },
            ]}
          />
        </section>
      ))}
    </div>
  );
}

export function SubmissionOcrDetails({
  drawer,
  columns,
  loadMore,
}: Readonly<{
  drawer: OcrDrawerState | null;
  columns: TableColumnsType<OcrTask>;
  loadMore: () => Promise<void>;
}>) {
  if (drawer?.loading) return <LoadingState label="正在读取识别详情" />;
  if (drawer?.error) return <ErrorState message={drawer.error} />;
  if (!drawer) return null;
  return (
    <div className="detail-stack">
      <Descriptions bordered size="small" column={2}>
        <Descriptions.Item label="答题卡编号">{drawer.row.submission.candidate_no || "暂未生成"}</Descriptions.Item>
        <Descriptions.Item label="识别状态">{ocrLabel(drawer.row.ocrTasks)}</Descriptions.Item>
      </Descriptions>
      <ResponsiveTable<OcrTask>
        className="dense-data-table"
        rowKey="id"
        dataSource={drawer.tasks}
        columns={columns}
        pagination={false}
        size="small"
        locale={{ emptyText: <EmptyState title="暂无识别任务" description="该答题卡还没有文字识别任务。" /> }}
      />
      {drawer.row.ocrHasMore ? (
        <div className="load-more-row">
          <Button loading={drawer.loadingMore} onClick={() => void loadMore()}>加载更早的识别任务</Button>
        </div>
      ) : null}
      <OcrResults drawer={drawer} />
    </div>
  );
}
