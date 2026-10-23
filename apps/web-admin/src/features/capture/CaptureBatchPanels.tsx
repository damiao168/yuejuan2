// 批次详情只负责展示；异步命令和当前批次校验仍由页面统一持有，避免面板切换改变操作范围。
import { Alert, Button, Empty, Progress, Select, Space, Tabs, Upload, type TableColumnsType, type UploadProps } from "antd";
import { FileUp, RefreshCw, ScanLine, SlidersHorizontal, Undo2, Workflow } from "lucide-react";
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
import { captureFileColumns, capturePageColumns, captureFileErrorLabel } from "./captureColumns";
import type { PendingCaptureUpload } from "./useCaptureUpload";

export function CaptureBatchListPanel({ batches, selectedId, setSelectedId, hasMoreBatches, loadingMoreBatches, loadMoreBatches, disabled }: Readonly<Pick<ReturnType<typeof useCaptureBatchList>, "batches" | "selectedId" | "setSelectedId" | "hasMoreBatches" | "loadingMoreBatches" | "loadMoreBatches"> & { disabled?: boolean }>) {
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
          disabled={disabled}
          className={batch.id === selectedId ? "capture-batch-row active" : "capture-batch-row"}
          onClick={() => setSelectedId(batch.id)}
        >
          <span>
            <strong>{batch.name}</strong>
            <small>{new Date(batch.created_at).toLocaleString("zh-CN", { hour12: false })}</small>
          </span>
          <StatusTag tone={statusTone(batch.status)}>{batch.status === "ready" ? "本批处理完成" : statusLabels[batch.status]}</StatusTag>
        </button>
      ))}
      {hasMoreBatches ? <Button block loading={loadingMoreBatches} onClick={() => void loadMoreBatches()}>加载更多批次</Button> : null}
    </aside>
  );
}

function CaptureProgress({ percent }: Readonly<{ percent: number | null }>) {
  if (percent === null) return null;
  return <div aria-label="页面处理完成进度"><small>页面处理完成进度</small><Progress percent={percent} size="small" /></div>;
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
          <div><strong>{file.original_name}</strong><span title={file.error_code || undefined}>文件处理失败：{captureFileErrorLabel(file.error_code)}</span></div>
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
        const pages = detail.pages.filter((item) => item.submission_id === submissionId && item.status !== "deleted");
        const complete = pages.every((item) => item.status === "ready");
        const groupingBlocked = pages.some(page => page.page_identity.grouping_status && page.page_identity.grouping_status !== "complete");
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
                        disabled={!batchCanManage || groupingBlocked}
                        onClick={() => void resolveRegistration(item.registration_run_id!, item.action === "confirm_registration" ? "confirm" : "retry")}
                      >
                        {item.action === "confirm_registration" ? `确认第 ${item.page_no} 页对齐` : `重试第 ${item.page_no} 页对齐`}
                      </Button>
                    )}
                    <Button icon={<SlidersHorizontal size={15} />} disabled={!batchCanManage || groupingBlocked} onClick={() => setCorrectionRunId(item.registration_run_id)}>校正第 {item.page_no} 页边界</Button>
                  </Space>
                ))}
              <Button icon={<Workflow size={16} />} loading={actioning} disabled={!batchCanManage || complete || groupingBlocked} onClick={() => void startPageProcessing(submissionId)}>{complete ? "处理完成" : groupingBlocked ? "等待答卷完整分组" : "对齐版面并切题"}</Button>
            </Space>
          </div>
        );
      })}
    </div>
  );
}

function CaptureGroupingStatus({ detail }: Readonly<{ detail: CaptureBatchDetail }>) {
  const groups = new Map<string, CaptureBatchDetail["files"]>();
  for (const file of detail.files) {
    if (!file.upload_group_id) continue;
    const files = groups.get(file.upload_group_id) ?? [];
    files.push(file);
    groups.set(file.upload_group_id, files);
  }
  return <>{[...groups.entries()].map(([id, files], index) => {
    const expectedFiles = Math.max(...files.map(file => file.upload_group_file_count ?? 0));
    const pagesPerStudent = files[0].pages_per_student ?? 1;
    const fileIds = new Set(files.map(file => file.id));
    const pages = detail.pages.filter(page => fileIds.has(page.capture_file_id) && page.status !== "deleted");
    const statuses = new Set(pages.map(page => page.page_identity.grouping_status));
    const failed = files.filter(file => file.status === "failed").length;
    const duplicate = files.filter(file => file.status === "duplicate").length;
    const problems: string[] = [];
    if (files.length < expectedFiles) problems.push(`应有 ${expectedFiles} 个文件，已登记 ${files.length} 个，文件尚未上传齐全。请重试本次上传`);
    if (failed) problems.push(`${failed} 个文件解码失败，请重试原文件`);
    if (duplicate) problems.push(`${duplicate} 个重复文件未计入完整答卷，请核对后一次重新选择完整答卷`);
    if (statuses.has("incomplete_pages")) {
      const totalPages = files.reduce((total, file) => total + file.page_count, 0);
      const missing = (pagesPerStudent - totalPages % pagesPerStudent) % pagesPerStudent;
      problems.push(`已解码 ${totalPages} 页，每名学生 ${pagesPerStudent} 页${missing ? `，缺少 ${missing} 页` : "，页面不完整"}，请一次重新选择完整答卷`);
    }
    if (statuses.has("identity_conflict")) problems.push("同一份答卷的学生身份冲突，请核对学生信息与页面顺序");
    if (problems.length) return <Alert key={id} type="warning" showIcon message={`上传组 ${index + 1} 尚未完整分组，不进入后续页面处理`} description={`${problems.join("；")}。每次文件选择独立分组，不会与其他选择凑页。`} />;
    if (statuses.has("waiting_files") || files.some(file => file.status !== "completed")) return <Alert key={id} type="info" showIcon message={`上传组 ${index + 1} 等待文件拆页与分组校验`} description={`每名学生 ${pagesPerStudent} 页；PDF/TIFF 按解码后的实际页数计算，完整分组后才进入后续处理。`} />;
    return null;
  })}</>;
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
  progressPercent, summary, uploadProps, uploading, actioning, startProcessing,
  reopenBatch, activeTab, setActiveTab, issuePages, pageColumns, fileColumns, uploadRepair,
  onOverride, processingProps, matchingProps,
  pagesPerStudent, setPagesPerStudent, pendingUpload, retryUpload, discardUpload,
  uploadLabel = "导入文件",
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
  uploadLabel?: string;
  pagesPerStudent?: number;
  setPagesPerStudent: (pages: number) => void;
  pendingUpload?: PendingCaptureUpload;
  retryUpload: () => Promise<void>;
  discardUpload: () => void;
  actioning: boolean;
  startProcessing: () => Promise<void>;
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
          <StatusTag tone={statusTone(detail.batch.status)}>{detail.batch.status === "ready" ? "本批处理完成" : statusLabels[detail.batch.status]}</StatusTag>
        </Space>
        <CaptureProgress percent={progressPercent} />
      </div>
      <Space wrap>
        <label htmlFor="capture-pages-per-student">每名学生答题卡页数</label>
        <Select id="capture-pages-per-student" aria-label="每名学生答题卡页数" placeholder="请先选择页数" value={pagesPerStudent}
          style={{ width: 200 }} disabled={!batchCanManage || Boolean(pendingUpload)} onChange={setPagesPerStudent}
          options={Array.from({ length: 20 }, (_, index) => ({ value: index + 1, label: index === 0 ? "1 页（单面）" : index === 1 ? "2 页（正反面）" : `${index + 1} 页` }))} />
        <Upload {...uploadProps}>
          <Button icon={<FileUp size={16} />} loading={uploading} disabled={!batchCanManage || !pagesPerStudent || Boolean(pendingUpload)}>{uploadLabel}</Button>
        </Upload>
        <Button type="primary" icon={<Workflow size={16} />} onClick={() => void startProcessing()} loading={actioning}
          disabled={!batchCanManage || !detail.files.some((item) => ["uploaded", "failed"].includes(item.status))}>
          {detail.files.some((item) => item.status === "failed") ? "处理待办文件" : "开始处理"}
        </Button>
        {detail.batch.status === "completed" && canManage && (
          <Button icon={<Undo2 size={16} />} onClick={reopenBatch} loading={actioning}>重开批次</Button>
        )}
      </Space>
    </header>
    <Alert type="info" showIcon message="请将同一学生的全部页面连续排列，再排列下一名学生" description="按选择的文件顺序、文件内页序，每 N 页组成一份学生答卷。1 页用于单面，2 页用于正反面，支持 1–20 页。PDF/TIFF 按解码页数计算；每次选择独立分组，缺页不会进入后续处理，也不会跨两次选择凑页。" />
    {pendingUpload ? <Alert type={pendingUpload.error ? "warning" : "info"} showIcon message={`${uploading ? "正在上传" : "本次上传未完成"}：已登记 ${pendingUpload.completedFiles}/${pendingUpload.totalFiles} 个文件，每名学生 ${pendingUpload.pagesPerStudent} 页`}
      description={pendingUpload.error || "请等待本次选择的文件按顺序上传和登记。"}
      action={!uploading ? <Space><Button onClick={() => void retryUpload()} disabled={!batchCanManage}>重试本次上传</Button><Button onClick={discardUpload}>结束本次上传</Button></Space> : undefined} /> : null}
    <CaptureGroupingStatus detail={detail} />
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
