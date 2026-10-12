// 批次详情只负责展示；异步命令和当前批次校验仍由页面统一持有，避免面板切换改变操作范围。
import { Alert, Button, Empty, Progress, Space, Tabs, Upload, type TableColumnsType, type UploadProps } from "antd";
import { Check, FileUp, RefreshCw, ScanLine, SlidersHorizontal, Undo2, Workflow } from "lucide-react";
import type { CaptureBatchDetail, CapturePage, ProcessingSummary } from "../../api/capture";
import { ErrorState, LoadingState } from "../../components/PageState";
import { ResponsiveTable } from "../../components/ResponsiveTable";
import { StatusTag } from "../../components/StatusTag";
import { hashQueryParam } from "../../router/query";
import { ImageQualityWorkspace } from "./ImageQualityWorkspace";
import { MatchingWorkspace } from "./MatchingWorkspace";
import type { useCaptureBatchList } from "./useCaptureBatchList";
import type { useCaptureBatchDetail } from "./useCaptureBatchDetail";
import type { captureSummary } from "./captureBatchSummary";
import { statusLabels, statusTone } from "./capturePresentation";
import { captureFileColumns, capturePageColumns, errorLabels } from "./captureColumns";

export function CaptureBatchListPanel({ batches, selectedId, setSelectedId, hasMoreBatches, loadingMoreBatches, loadMoreBatches }: Readonly<Pick<ReturnType<typeof useCaptureBatchList>, "batches" | "selectedId" | "setSelectedId" | "hasMoreBatches" | "loadingMoreBatches" | "loadMoreBatches">>) {
  return (
    <aside className="capture-batch-list">
      <div className="capture-pane-title">
        <strong>批次</strong>
        <span>{batches.length}</span>
      </div>
      {batches.map((batch) => (
        <button
          type="button"
          key={batch.id}
          className={batch.id === selectedId ? "capture-batch-row active" : "capture-batch-row"}
          onClick={() => setSelectedId(batch.id)}
        >
          <span>
            <strong>{batch.name}</strong>
            <small>{new Date(batch.created_at).toLocaleString("zh-CN", { hour12: false })}</small>
          </span>
          <StatusTag tone={statusTone(batch.status)}>{statusLabels[batch.status]}</StatusTag>
        </button>
      ))}
      {hasMoreBatches ? <Button block loading={loadingMoreBatches} onClick={() => void loadMoreBatches()}>加载更多批次</Button> : null}
    </aside>
  );
}

function CaptureProgress({ percent }: Readonly<{ percent: number | null }>) {
  if (percent === null) return null;
  return <Progress percent={percent} size="small" />;
}

function CaptureIssueQueue({ detail, issuePages, pageColumns, actioning, batchCanManage, startProcessing, uploadRepair }: Readonly<{
  detail: CaptureBatchDetail;
  issuePages: CapturePage[];
  pageColumns: TableColumnsType<CapturePage>;
  actioning: boolean;
  batchCanManage: boolean;
  startProcessing: () => Promise<void>;
  uploadRepair: () => void;
}>) {
  if (!(issuePages.length || detail.batch.failed_count > 0)) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有需要人工处理的页面" />;
  }
  return (
    <div className="capture-issue-queue">
      {detail.files.filter((file) => file.status === "failed").map((file) => (
        <div className="capture-file-issue" key={file.id}>
          <div><strong>{file.original_name}</strong><span title={file.error_code || undefined}>文件处理失败：{(file.error_code && errorLabels[file.error_code]) || "请重新导入正确的文件"}</span></div>
          <Space><Button icon={<RefreshCw size={15} />} loading={actioning} disabled={!batchCanManage} onClick={() => void startProcessing()}>重试原文件</Button><Button icon={<FileUp size={15} />} disabled={!batchCanManage} onClick={uploadRepair}>上传修复文件</Button></Space>
        </div>
      ))}
      {issuePages.length > 0 ? <ResponsiveTable className="dense-data-table" rowKey="id" columns={pageColumns} dataSource={issuePages} pagination={false} size="small" /> : null}
    </div>
  );
}

function pageProcessingLabel(complete: boolean, summary?: ProcessingSummary) {
  if (complete) return "已完成版面对齐与题目切分";
  if (summary) return `已完成 ${summary.ready_pages} 页 · 需人工处理 ${summary.blocked_pages} 页 · 处理中 ${summary.pending_pages} 页`;
  return "等待版面对齐或人工确认";
}

function CaptureProcessingList({ detail, submissions, processingSummaries, actioning, batchCanManage, resolveRegistration, setCorrectionRunId, startPageProcessing }: Readonly<{
  detail: CaptureBatchDetail;
  submissions: string[];
  processingSummaries: Record<string, ProcessingSummary>;
  actioning: boolean;
  batchCanManage: boolean;
  resolveRegistration: (runId: string, action: "confirm" | "retry") => Promise<void>;
  setCorrectionRunId: (runId: string | undefined) => void;
  startPageProcessing: (submissionId: string) => Promise<void>;
}>) {
  if (!submissions.length) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="拆页完成后，系统将自动对齐版面并按题切分" />;
  }
  return (
    <div className="capture-processing-list">
      {submissions.map((submissionId, index) => {
        const pages = detail.pages.filter((item) => item.submission_id === submissionId);
        const complete = pages.every((item) => item.status === "ready");
        const summary = processingSummaries[submissionId];
        return (
          <div key={submissionId} className="capture-processing-row">
            <div>
              <strong>答卷 {index + 1}</strong>
              <span>{pages.length} 页 · {pageProcessingLabel(complete, summary)}</span>
            </div>
            <Space wrap>
              {summary?.blockers
                .filter((item) => item.registration_run_id && item.stage === "registration")
                .map((item) => (
                  <Space key={item.page_id} wrap>
                    {["confirm_registration", "retry_registration"].includes(item.action) && (
                      <Button
                        type={item.action === "confirm_registration" ? "primary" : "default"}
                        loading={actioning}
                        disabled={!batchCanManage}
                        onClick={() => void resolveRegistration(item.registration_run_id!, item.action === "confirm_registration" ? "confirm" : "retry")}
                      >
                        {item.action === "confirm_registration" ? `确认第 ${item.page_no} 页对齐` : `重试第 ${item.page_no} 页对齐`}
                      </Button>
                    )}
                    <Button icon={<SlidersHorizontal size={15} />} disabled={!batchCanManage} onClick={() => setCorrectionRunId(item.registration_run_id)}>校正第 {item.page_no} 页边界</Button>
                  </Space>
                ))}
              <Button icon={<Workflow size={16} />} loading={actioning} disabled={!batchCanManage || complete} onClick={() => void startPageProcessing(submissionId)}>{complete ? "处理完成" : "对齐版面并切题"}</Button>
            </Space>
          </div>
        );
      })}
    </div>
  );
}

function CaptureMatchingContent({ loading, error, onRetry, queue, workspaceProps }: Readonly<{
  loading: boolean;
  error?: string;
  onRetry: () => void;
  queue?: Parameters<typeof MatchingWorkspace>[0]["queue"];
  workspaceProps: Omit<Parameters<typeof MatchingWorkspace>[0], "queue">;
}>) {
  // 匹配读取尚未完成时优先显示加载状态，旧队列不能冒充本次读取结果。
  if (loading) return <LoadingState label="正在加载考试名册" />;
  if (error) return <ErrorState message={error} onRetry={onRetry} />;
  if (!queue) return <Empty description="暂无匹配数据" />;
  return <MatchingWorkspace queue={queue} {...workspaceProps} />;
}

export function CaptureBatchWorkspace({
  detail, detailError, detailLoading, selectedId, loadDetail, canManage, batchCanManage,
  progressPercent, summary, uploadProps, uploading, actioning, startProcessing, completeBatch,
  reopenBatch, activeTab, setActiveTab, issuePages, pageColumns, fileColumns, uploadRepair,
  onOverride, processingProps, matchingProps,
}: Readonly<{
  detail?: CaptureBatchDetail;
  detailError?: string;
  detailLoading: boolean;
  selectedId: string;
  loadDetail: ReturnType<typeof useCaptureBatchDetail>["loadDetail"];
  canManage: boolean;
  batchCanManage: boolean;
  progressPercent: number | null;
  summary: ReturnType<typeof captureSummary>;
  uploadProps: UploadProps;
  uploading: boolean;
  actioning: boolean;
  startProcessing: () => Promise<void>;
  completeBatch: () => void;
  reopenBatch: () => void;
  activeTab: string;
  setActiveTab: (key: string) => void;
  issuePages: CapturePage[];
  pageColumns: ReturnType<typeof capturePageColumns>;
  fileColumns: ReturnType<typeof captureFileColumns>;
  uploadRepair: () => void;
  onOverride: (page: CapturePage) => void;
  processingProps: Omit<Parameters<typeof CaptureProcessingList>[0], "detail" | "actioning" | "batchCanManage">;
  matchingProps: Parameters<typeof CaptureMatchingContent>[0];
}>) {
  // 首次读取失败时没有可展示事实；同批次刷新失败仍保留原详情，只在就绪面板提示错误。
  if (detailError && !detail) return <ErrorState message={detailError} onRetry={() => void loadDetail(selectedId)} />;
  if (detailLoading || !detail) return <LoadingState label="正在读取批次" />;
  return <>
    {detailError ? <Alert type="warning" showIcon message={detailError} action={<Button onClick={() => void loadDetail(selectedId)}>重试读取批次</Button>} /> : null}
    <header className="capture-batch-header">
      <div>
        <Space>
          <ScanLine size={20} />
          <h2>{detail.batch.name}</h2>
          <StatusTag tone={statusTone(detail.batch.status)}>{statusLabels[detail.batch.status]}</StatusTag>
        </Space>
        <CaptureProgress percent={progressPercent} />
      </div>
      <Space wrap>
        <Upload {...uploadProps}>
          <Button icon={<FileUp size={16} />} loading={uploading} disabled={!batchCanManage}>导入文件</Button>
        </Upload>
        <Button type="primary" icon={<Workflow size={16} />} onClick={() => void startProcessing()} loading={actioning}
          disabled={!batchCanManage || !detail.files.some((item) => ["uploaded", "failed"].includes(item.status))}>
          {detail.files.some((item) => item.status === "failed") ? "处理待办文件" : "开始处理"}
        </Button>
        {detail.batch.status === "ready" && (
          <Button type="primary" icon={<Check size={16} />} onClick={completeBatch} loading={actioning} disabled={!batchCanManage}>完成批次</Button>
        )}
        {detail.batch.status === "completed" && canManage && (
          <Button icon={<Undo2 size={16} />} onClick={reopenBatch} loading={actioning}>重开批次</Button>
        )}
      </Space>
    </header>
    <div className="capture-summary-strip">
      {summary.map((item) => <div key={item.label}><span>{item.label}</span><strong>{item.value}</strong></div>)}
    </div>
    <Tabs activeKey={activeTab} onChange={setActiveTab} items={[
      {
        key: "issues",
        label: `需要处理 (${issuePages.length + detail.batch.failed_count})`,
        children: <CaptureIssueQueue detail={detail} issuePages={issuePages} pageColumns={pageColumns} actioning={actioning} batchCanManage={batchCanManage} startProcessing={startProcessing} uploadRepair={uploadRepair} />,
      },
      {
        key: "files",
        label: "全部文件与页面",
        children: <div className="capture-detail-stack">
          <ResponsiveTable className="dense-data-table" rowKey="id" columns={fileColumns} dataSource={detail.files} pagination={false} size="small" />
          <ResponsiveTable className="dense-data-table" rowKey="id" columns={pageColumns} dataSource={detail.pages} pagination={{ pageSize: 20 }} size="small" />
        </div>,
      },
      {
        key: "image-quality",
        label: `图像质检 (${detail.pages.filter((page) => page.status !== "deleted").length})`,
        children: <ImageQualityWorkspace key={selectedId} initialPageId={hashQueryParam("page")} pages={detail.pages}
          canManage={batchCanManage} onRefresh={() => loadDetail(detail.batch.id, true)} onOverride={onOverride} />,
      },
      {
        key: "processing",
        label: `页面处理 (${processingProps.submissions.length})`,
        children: <CaptureProcessingList detail={detail} actioning={actioning} batchCanManage={batchCanManage} {...processingProps} />,
      },
      {
        key: "matching",
        label: `学生匹配（待确认 ${matchingProps.queue?.submissions.filter((item) => item.identity_status !== "matched").length ?? 0}）`,
        children: <CaptureMatchingContent {...matchingProps} />,
      },
    ]} />
  </>;
}
