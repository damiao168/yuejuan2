import { useEffect, useMemo, useRef, useState } from "react";
import type { SessionUser } from "../auth/session";
import { acceptBatchCreateCommand, batchCreateCommandKey, loadBatchCreateCommand } from "../features/capture/batchCreateCommand";
import { recoverCaptureBatchCommand } from "../api/capture";
import { ApiClientError } from "../api/client";
import {
  App,
  Button,
  Empty,
  Form,
  Input,
  Modal,
  Progress,
  Select,
  Space,
  Tabs,
  Upload,
  type UploadProps,
} from "antd";
import {
  Check,
  FileUp,
  Plus,
  RefreshCw,
  ScanLine,
  SlidersHorizontal,
  Undo2,
  Workflow,
} from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import {
  confirmRegistration,
  completeCaptureBatch,
  createCaptureBatch,
  overrideCapturePageQuality,
  processCaptureBatch,
  processSubmissionPages,
  reopenCaptureBatch,
  retryRegistration,
  updateCapturePage,
  type CaptureBatch,
  type CapturePage,
} from "../api/capture";
import { downloadFileBlob } from "../api/files";
import { ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { RegistrationCorrectionWorkspace } from "../components/RegistrationCorrectionWorkspace";
import { ImageQualityWorkspace } from "../features/capture/ImageQualityWorkspace";
import { MatchingWorkspace } from "../features/capture/MatchingWorkspace";
import { useCaptureBatchList } from "../features/capture/useCaptureBatchList";
import { useCaptureBatchDetail } from "../features/capture/useCaptureBatchDetail";
import { useCaptureMatchActions } from "../features/capture/useCaptureMatchActions";
import { captureProgressPercent, captureSummary, captureSubmissionIds, captureIssuePages } from "../features/capture/captureBatchSummary";
import { useCaptureUpload } from "../features/capture/useCaptureUpload";
import { statusLabels, statusTone } from "../features/capture/capturePresentation";
import { captureFileColumns, capturePageColumns, errorLabels } from "../features/capture/captureColumns";

function formatError(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请重试");
}

export function CaptureBatchPage({
  user,
  examId,
  canManage,
}: {
  user: SessionUser;
  examId: string;
  canManage: boolean;
}) {
  const { message, modal } = App.useApp();
  const [form] = Form.useForm<{
    name: string;
    source_type: CaptureBatch["source_type"];
  }>();
  const {
    batches, setBatches, selectedId, setSelectedId, loading, loadingMoreBatches,
    hasMoreBatches, error, loadBatches, loadMoreBatches
  } = useCaptureBatchList(examId);
  const [modalOpen, setModalOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const creationBusy = useRef(false);
  const creationCommandKey = batchCreateCommandKey(user.tenant, user.id, examId);
  const [pendingCreation, setPendingCreation] = useState(() => {
    try { return Boolean(loadBatchCreateCommand(localStorage, creationCommandKey)); } catch { return true; }
  });
  const [actioning, setActioning] = useState(false);
  const [preview, setPreview] = useState<{ url: string; page: CapturePage }>();
  const [correctionRunId, setCorrectionRunId] = useState<string>();
  const [activeTab, setActiveTab] = useState("files");
  const { detail, detailLoading, matching, setMatching, matchingLoading, processingSummaries,
    loadDetail, loadMatching, resetDetail } = useCaptureBatchDetail(setBatches, setActiveTab);
  const { uploading, uploadSource } = useCaptureUpload(detail, examId, loadDetail);
  const [reopenOpen, setReopenOpen] = useState(false);
  const [reopenReason, setReopenReason] = useState("");
  const [qualityOverridePage, setQualityOverridePage] = useState<CapturePage>();
  const [qualityOverrideReason, setQualityOverrideReason] = useState("");
  const previewRequestRef = useRef(0);
  const batchCanManage = canManage && !["completed", "cancelled"].includes(detail?.batch.status ?? "");

  useEffect(() => {
    resetDetail();
    previewRequestRef.current += 1;
    setSelectedId("");
    setPreview(undefined);
  }, [examId, resetDetail]);
  useEffect(() => {
    void loadBatches();
  }, [loadBatches]);
  useEffect(() => {
    void loadDetail(selectedId);
  }, [loadDetail, selectedId]);
  useEffect(() => {
    void loadMatching(selectedId);
  }, [loadMatching, selectedId]);
  useEffect(() => {
    if (!detail || !["uploading", "processing"].includes(detail.batch.status))
      return;
    const timer = window.setInterval(
      () => void loadDetail(detail.batch.id, true),
      3000,
    );
    return () => window.clearInterval(timer);
  }, [detail, loadDetail]);
  useEffect(
    () => () => {
      if (preview?.url) URL.revokeObjectURL(preview.url);
    },
    [preview],
  );

  async function submitBatch() {
    if (creationBusy.current) return;
    creationBusy.current = true;
    try {
      const pending = loadBatchCreateCommand(localStorage, creationCommandKey);
      const values = pending?.payload ?? await form.validateFields();
      setCreating(true);
      const command = acceptBatchCreateCommand(localStorage, creationCommandKey, values);
      setPendingCreation(true);
      const recovered = pending ? await recoverCaptureBatchCommand(examId, command.payload.idempotency_key) : null;
      const result = recovered?.command.status === "succeeded" && recovered.command.batch
        ? { batch: recovered.command.batch }
        : await createCaptureBatch(examId, command.payload);
      try { localStorage.removeItem(creationCommandKey); } catch { /* recovery still returns the same batch */ }
      setPendingCreation(false);
      setModalOpen(false);
      form.resetFields();
      await loadBatches();
      setSelectedId(result.batch.id);
      message.success("采集批次已创建");
    } catch (currentError) {
      if (currentError instanceof ApiClientError && currentError.code === "capture_invalid_input") {
        try { localStorage.removeItem(creationCommandKey); setPendingCreation(false); } catch { /* retain recovery data */ }
      }
      message.error(formatError(currentError));
    } finally {
      creationBusy.current = false;
      setCreating(false);
    }
  }

  const uploadProps: UploadProps = {
    multiple: true,
    showUploadList: false,
    accept: ".pdf,.png,.jpg,.jpeg,.tif,.tiff",
    beforeUpload: (file) => {
      void uploadSource(file);
      return false;
    },
  };

  async function startProcessing() {
    if (!detail) return;
    setActioning(true);
    try {
      await processCaptureBatch(detail.batch.id);
      await loadDetail(detail.batch.id);
      message.success("文件已进入页面处理队列");
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }

  function completeBatch() {
    if (!detail) return;
    modal.confirm({
      title: "确认完成批次",
      content: "完成后批次将不可再修改。请确认所有页面已处理完毕，且每份答卷都已确认学生身份。",
      okText: "完成批次",
      cancelText: "返回检查",
      onOk: async () => {
        setActioning(true);
        try {
          await completeCaptureBatch(detail.batch.id, "所有页面处理完成并已人工确认");
          await Promise.all([loadBatches(), loadDetail(detail.batch.id)]);
          message.success("批次已完成");
        } catch (currentError) {
          message.error(formatError(currentError));
          throw currentError;
        } finally {
          setActioning(false);
        }
      },
    });
  }

  function reopenBatch() {
    setReopenReason("");
    setReopenOpen(true);
  }

  async function confirmReopen() {
    if (!detail || !reopenReason.trim()) return;
    setActioning(true);
    try {
      await reopenCaptureBatch(detail.batch.id, reopenReason.trim());
      setReopenOpen(false);
      await Promise.all([loadBatches(), loadDetail(detail.batch.id)]);
      message.success("批次已重开");
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }

  async function startPageProcessing(submissionId: string) {
    setActioning(true);
    try {
      const result = await processSubmissionPages(submissionId);
      await loadDetail(detail!.batch.id);
      message.success(result.runs.length > 0
        ? `已开始处理 ${result.runs.length} 页（版面对齐与题目切分）`
        : "已开始识别首张答卷模板");
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }

  async function confirmQualityOverride() {
    if (!detail || !qualityOverridePage?.submission_page_id || !qualityOverrideReason.trim()) return;
    setActioning(true);
    try {
      const result = await overrideCapturePageQuality(
        qualityOverridePage.submission_page_id,
        qualityOverrideReason.trim(),
      );
      setQualityOverridePage(undefined);
      setQualityOverrideReason("");
      await loadDetail(detail.batch.id);
      message.success(`质量误判已放行，并恢复 ${result.registration_runs.length} 个版面对齐任务`);
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }

  async function showPage(page: CapturePage) {
    const requestId = ++previewRequestRef.current;
    try {
      const blob = await downloadFileBlob(page.decoded_file_asset_id);
      if (requestId !== previewRequestRef.current) return;
      const url = URL.createObjectURL(blob.blob);
      setPreview((current) => {
        if (current?.url) URL.revokeObjectURL(current.url);
        return { url, page };
      });
    } catch (currentError) {
      if (requestId === previewRequestRef.current) message.error(formatError(currentError));
    }
  }

  async function rotatePage(page: CapturePage) {
    try {
      await updateCapturePage(page.id, {
        revision: page.revision,
        rotation_degrees: (page.rotation_degrees + 90) % 360,
      });
      await loadDetail(page.capture_batch_id);
      message.success("页面旋转已保存，后续处理将重新校验");
    } catch (currentError) {
      message.error(formatError(currentError));
    }
  }

  const { matchStudent, markUnknown, matchPage, changePageLifecycle, splitSubmission, mergeSubmissions } = useCaptureMatchActions({
    selectedId, loadMatching, loadDetail, setMatching, setActioning,
  });

  const fileColumns = captureFileColumns();
  const pageColumns = capturePageColumns({ batchCanManage, showPage, rotatePage, changePageLifecycle, setQualityOverrideReason, setQualityOverridePage });

  const progressPercent = useMemo(() => captureProgressPercent(detail), [detail]);
  const summary = useMemo(() => captureSummary(detail), [detail]);
  const submissions = useMemo(() => captureSubmissionIds(detail), [detail]);
  const issuePages = useMemo(() => captureIssuePages(detail), [detail]);
  async function resolveRegistration(runId: string, action: "confirm" | "retry") { setActioning(true); try { if (action === "confirm") await confirmRegistration(runId, "人工核对版面对齐边界与题目区域正确"); else await retryRegistration(runId); await loadDetail(selectedId, true); message.success(action === "confirm" ? "版面对齐已确认" : "已重新排队对齐"); } catch (currentError) { message.error(formatError(currentError)); } finally { setActioning(false); } }

  if (loading) return <LoadingState label="正在加载采集批次" />;
  if (error)
    return (
      <ErrorState
        message={`采集批次加载失败：${error}`}
        onRetry={() => void loadBatches()}
      />
    );
  if (correctionRunId) return <RegistrationCorrectionWorkspace runId={correctionRunId} canManage={batchCanManage} onClose={() => setCorrectionRunId(undefined)} onChanged={async () => { await loadDetail(selectedId, true); }} />;
  return (
    <div className="capture-batch-page">
      <section className="member-management-heading">
        <div>
          <h1>答卷导入</h1>
          <p>上传扫描文件并优先处理学生匹配、图像质量和处理失败问题。</p>
        </div>
        <Space>
          <Button
            icon={<RefreshCw size={16} />}
            onClick={() => void loadBatches()}
          >
            刷新
          </Button>
          <Button
            type="primary"
            icon={<Plus size={16} />}
            onClick={() => setModalOpen(true)}
            disabled={!canManage}
          >
            新建批次
          </Button>
        </Space>
      </section>
      {batches.length === 0 ? (
        <Empty description="尚未创建采集批次">
          <Button type="primary" onClick={() => setModalOpen(true)} disabled={!canManage}>
            创建第一个批次
          </Button>
        </Empty>
      ) : (
        <div className="capture-batch-layout">
          <aside className="capture-batch-list">
            <div className="capture-pane-title">
              <strong>批次</strong>
              <span>{batches.length}</span>
            </div>
            {batches.map((batch) => (
              <button
                type="button"
                key={batch.id}
                className={
                  batch.id === selectedId
                    ? "capture-batch-row active"
                    : "capture-batch-row"
                }
                onClick={() => setSelectedId(batch.id)}
              >
                <span>
                  <strong>{batch.name}</strong>
                  <small>{new Date(batch.created_at).toLocaleString("zh-CN", { hour12: false })}</small>
                </span>
                <StatusTag tone={statusTone(batch.status)}>
                  {statusLabels[batch.status]}
                </StatusTag>
              </button>
            ))}
            {hasMoreBatches ? (
              <Button block loading={loadingMoreBatches} onClick={() => void loadMoreBatches()}>
                加载更多批次
              </Button>
            ) : null}
          </aside>
          <main className="capture-batch-workspace">
            {detailLoading || !detail ? (
              <LoadingState label="正在读取批次" />
            ) : (
              <>
                <header className="capture-batch-header">
                  <div>
                    <Space>
                      <ScanLine size={20} />
                      <h2>{detail.batch.name}</h2>
                      <StatusTag tone={statusTone(detail.batch.status)}>
                        {statusLabels[detail.batch.status]}
                      </StatusTag>
                    </Space>
                    {progressPercent !== null ? (
                      <Progress percent={progressPercent} size="small" />
                    ) : null}
                  </div>
                  <Space wrap>
                    <Upload {...uploadProps}>
                      <Button
                        icon={<FileUp size={16} />}
                        loading={uploading}
                        disabled={!batchCanManage}
                      >
                        导入文件
                      </Button>
                    </Upload>
                    <Button
                      type="primary"
                      icon={<Workflow size={16} />}
                      onClick={() => void startProcessing()}
                      loading={actioning}
                      disabled={
                        !batchCanManage ||
                        !detail.files.some((item) =>
                          ["uploaded", "failed"].includes(item.status),
                        )
                      }
                    >
                      {detail.files.some((item) => item.status === "failed")
                        ? "处理待办文件"
                        : "开始处理"}
                    </Button>
                    {detail.batch.status === "ready" && (
                      <Button
                        type="primary"
                        icon={<Check size={16} />}
                        onClick={completeBatch}
                        loading={actioning}
                        disabled={!batchCanManage}
                      >
                        完成批次
                      </Button>
                    )}
                    {detail.batch.status === "completed" && canManage && (
                      <Button icon={<Undo2 size={16} />} onClick={reopenBatch} loading={actioning}>
                        重开批次
                      </Button>
                    )}
                  </Space>
                </header>
                <div className="capture-summary-strip">
                  {summary.map((item) => (
                    <div key={item.label}>
                      <span>{item.label}</span>
                      <strong>{item.value}</strong>
                    </div>
                  ))}
                </div>
                <Tabs
                  activeKey={activeTab}
                  onChange={setActiveTab}
                  items={[
                    {
                      key: "issues",
                      label: `需要处理 (${issuePages.length + detail.batch.failed_count})`,
                      children: issuePages.length || detail.batch.failed_count > 0 ? (
                        <div className="capture-issue-queue">
                          {detail.files.filter((file) => file.status === "failed").map((file) => (
                            <div className="capture-file-issue" key={file.id}>
                              <div><strong>{file.original_name}</strong><span title={file.error_code || undefined}>文件处理失败：{(file.error_code && errorLabels[file.error_code]) || "请重新导入正确的文件"}</span></div>
                              <Space><Button icon={<RefreshCw size={15} />} loading={actioning} disabled={!batchCanManage} onClick={() => void startProcessing()}>重试原文件</Button><Button icon={<FileUp size={15} />} disabled={!batchCanManage} onClick={() => { setActiveTab("files"); message.info("可重新导入修复后的同一文件，系统会保留原失败记录"); }}>上传修复文件</Button></Space>
                            </div>
                          ))}
                          {issuePages.length > 0 ? <ResponsiveTable className="dense-data-table" rowKey="id" columns={pageColumns} dataSource={issuePages} pagination={false} size="small" /> : null}
                        </div>
                      ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有需要人工处理的页面" />,
                    },
                    {
                      key: "files",
                      label: "全部文件与页面",
                      children: (
                        <div className="capture-detail-stack">
                          <ResponsiveTable
                            className="dense-data-table"
                            rowKey="id"
                            columns={fileColumns}
                            dataSource={detail.files}
                            pagination={false}
                            size="small"
                          />
                          <ResponsiveTable
                            className="dense-data-table"
                            rowKey="id"
                            columns={pageColumns}
                            dataSource={detail.pages}
                            pagination={{ pageSize: 20 }}
                            size="small"
                          />
                        </div>
                      ),
                    },
                    {
                      key: "image-quality",
                      label: `图像质检 (${detail.pages.filter((page) => page.status !== "deleted").length})`,
                      children: (
                        <ImageQualityWorkspace
                          pages={detail.pages}
                          canManage={batchCanManage}
                          onRefresh={() => loadDetail(detail.batch.id, true)}
                          onOverride={(page) => {
                            setQualityOverrideReason("");
                            setQualityOverridePage(page);
                          }}
                        />
                      ),
                    },
                    {
                      key: "processing",
                      label: `页面处理 (${submissions.length})`,
                      children: submissions.length ? (
                        <div className="capture-processing-list">
                          {submissions.map((submissionId, index) => {
                            const pages = detail.pages.filter(
                              (item) => item.submission_id === submissionId,
                            );
                            const complete = pages.every(
                              (item) => item.status === "ready",
                            );
                            const summary = processingSummaries[submissionId];
                            return (
                              <div
                                key={submissionId}
                                className="capture-processing-row"
                              >
                                <div>
                                  <strong>答卷 {index + 1}</strong>
                                  <span>
                                    {pages.length} 页 ·{" "}
                                    {complete
                                      ? "已完成版面对齐与题目切分"
                                      : summary ? `已完成 ${summary.ready_pages} 页 · 需人工处理 ${summary.blocked_pages} 页 · 处理中 ${summary.pending_pages} 页` : "等待版面对齐或人工确认"}
                                  </span>
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
                                            onClick={() =>
                                              void resolveRegistration(
                                                item.registration_run_id!,
                                                item.action === "confirm_registration" ? "confirm" : "retry",
                                              )
                                            }
                                          >
                                            {item.action === "confirm_registration"
                                              ? `确认第 ${item.page_no} 页对齐`
                                              : `重试第 ${item.page_no} 页对齐`}
                                          </Button>
                                        )}
                                        <Button
                                          icon={<SlidersHorizontal size={15} />}
                                          disabled={!batchCanManage}
                                          onClick={() => setCorrectionRunId(item.registration_run_id)}
                                        >
                                          校正第 {item.page_no} 页边界
                                        </Button>
                                      </Space>
                                    ))}
                                  <Button icon={<Workflow size={16} />} loading={actioning} disabled={!batchCanManage || complete} onClick={() => void startPageProcessing(submissionId)}>{complete ? "处理完成" : "对齐版面并切题"}</Button>
                                </Space>
                              </div>
                            );
                          })}
                        </div>
                      ) : (
                        <Empty
                          image={Empty.PRESENTED_IMAGE_SIMPLE}
                          description="拆页完成后，系统将自动对齐版面并按题切分"
                        />
                      ),
                    },
                    {
                      key: "matching",
                      label: `学生匹配（待确认 ${matching?.submissions.filter((item) => item.identity_status !== "matched").length ?? 0}）`,
                      children: matchingLoading ? (
                        <LoadingState label="正在加载考试名册" />
                      ) : matching ? (
                        <MatchingWorkspace
                          queue={matching}
                          canManage={batchCanManage}
                          actioning={actioning}
                          onPreview={showPage}
                          onConfirmStudent={matchStudent}
                          onUnknown={markUnknown}
                          onConfirmPage={matchPage}
                          onSplit={splitSubmission}
                          onMerge={mergeSubmissions}
                        />
                      ) : (
                        <Empty description="暂无匹配数据" />
                      ),
                    },
                  ]}
                />
              </>
            )}
          </main>
        </div>
      )}
      <Modal
        title="人工放行质量误判"
        open={Boolean(qualityOverridePage)}
        okText="确认放行并继续处理"
        cancelText="取消"
        confirmLoading={actioning}
        okButtonProps={{ disabled: qualityOverrideReason.trim().length < 5 }}
        onCancel={() => {
          setQualityOverridePage(undefined);
          setQualityOverrideReason("");
        }}
        onOk={() => void confirmQualityOverride()}
      >
        <p>仅当你已查看原图并确认页面可正常阅卷时使用。系统会保留原质量问题、操作者、原因和时间，并立即恢复版面对齐任务。</p>
        <Input.TextArea
          value={qualityOverrideReason}
          rows={4}
          maxLength={500}
          placeholder="至少填写 5 个字，例如：人工核对字迹和答题区域清晰，倾斜不影响识别"
          onChange={(event) => setQualityOverrideReason(event.target.value)}
        />
      </Modal>
      <Modal
        title="重开采集批次"
        open={reopenOpen}
        okText="确认重开"
        cancelText="取消"
        confirmLoading={actioning}
        okButtonProps={{ disabled: !reopenReason.trim() }}
        onCancel={() => setReopenOpen(false)}
        onOk={() => void confirmReopen()}
      >
        <Input.TextArea value={reopenReason} rows={3} maxLength={300} placeholder="填写重开原因" onChange={(event) => setReopenReason(event.target.value)} />
      </Modal>
      <Modal
        title={pendingCreation ? "恢复上次采集批次创建" : "新建采集批次"}
        okText={pendingCreation ? "继续确认原操作" : "创建"}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => void submitBatch()}
        confirmLoading={creating}
      >
        <Form
          form={form}
          layout="vertical"
          initialValues={{ source_type: "web_upload" }}
        >
          <Form.Item
            name="name"
            label="批次名称"
            rules={[
              { required: true, message: "请输入批次名称" },
              { max: 120 },
            ]}
          >
            <Input placeholder="例如：期末考试第一扫描批次" />
          </Form.Item>
          <Form.Item
            name="source_type"
            label="采集方式"
            rules={[{ required: true }]}
          >
            <Select
              options={[
                { value: "web_upload", label: "网页文件导入" },
                { value: "scanner_upload", label: "扫描工作站" },
                { value: "folder_import", label: "文件夹导入" },
                { value: "desktop_sync", label: "离线工作站同步" },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>
      <Modal
        title={preview ? `页面 ${preview.page.sequence_no}` : "页面预览"}
        open={Boolean(preview)}
        onCancel={() => {
          previewRequestRef.current += 1;
          if (preview?.url) URL.revokeObjectURL(preview.url);
          setPreview(undefined);
        }}
        footer={null}
        width={900}
      >
        {preview ? (
          <img
            className="capture-page-preview"
            src={preview.url}
            alt={`采集页面 ${preview.page.sequence_no}`}
          />
        ) : null}
      </Modal>
    </div>
  );
}
