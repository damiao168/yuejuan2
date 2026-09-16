import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
  InputNumber,
  Modal,
  Progress,
  Select,
  Space,
  Tabs,
  Tooltip,
  Upload,
  type TableColumnsType,
  type UploadProps,
} from "antd";
import {
  Check,
  Combine,
  FileQuestion,
  FileUp,
  FolderOpen,
  Plus,
  RefreshCw,
  RotateCw,
  ScanLine,
  Scissors,
  SlidersHorizontal,
  Trash2,
  Undo2,
  UserCheck,
  Workflow,
} from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import {
  confirmPageMatch,
  confirmRegistration,
  confirmStudentMatch,
  completeCaptureBatch,
  createCaptureBatch,
  deleteCapturePage,
  getCaptureBatch,
  getMatchingQueue,
  listCaptureBatches,
  markStudentUnknown,
  mergeCaptureSubmissions,
  overrideCapturePageQuality,
  processCaptureBatch,
  processSubmissionPages,
  reopenCaptureBatch,
  registerCaptureFile,
  restoreCapturePage,
  retryRegistration,
  splitCaptureSubmission,
  updateCapturePage,
  type CaptureBatch,
  type CaptureBatchDetail,
  type CaptureFile,
  type CapturePage,
  type MatchingQueue,
  type ProcessingSummary,
} from "../api/capture";
import { downloadFileBlob, uploadFile } from "../api/files";
import { ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { RegistrationCorrectionWorkspace } from "../components/RegistrationCorrectionWorkspace";
import { ImageQualityWorkspace } from "../features/capture/ImageQualityWorkspace";

const statusLabels: Record<string, string> = {
  draft: "等待上传",
  uploading: "正在上传",
  matching: "等待匹配",
  processing: "正在处理",
  needs_review: "需要确认",
  ready: "处理完成",
  completed: "已完成",
  cancelled: "已取消",
  uploaded: "已上传",
  queued: "等待处理",
  duplicate: "重复文件",
  failed: "处理失败",
  grouped: "已归入答卷",
  registration: "正在对齐版面",
  deleted: "已删除",
  quality_rejected: "质量不合格",
  normalized: "质量已通过",
  page_matching: "正在匹配模板",
};

const errorLabels: Record<string, string> = {
  pdf_decode_failed: "PDF 无法解析，请重新导出后再导入",
  unsupported_type: "文件格式不支持，请转为 PDF 或图片",
};

const contentTypeLabels: Record<string, string> = {
  "application/pdf": "PDF",
  "image/jpeg": "JPG 图片",
  "image/png": "PNG 图片",
  "image/tiff": "TIFF 图片",
};

function formatError(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请重试");
}

function statusTone(
  status: string,
): "success" | "warning" | "danger" | "processing" | "neutral" {
  if (status === "completed" || status === "ready" || status === "grouped")
    return "success";
  if (status === "failed" || status === "cancelled") return "danger";
  if (
    status === "needs_review" ||
    status === "duplicate" ||
    status === "quality_rejected"
  )
    return "warning";
  if (status === "processing" || status === "queued" || status === "uploading" || status === "page_matching")
    return "processing";
  return "neutral";
}

function MatchingWorkspace({
  queue,
  canManage,
  actioning,
  onPreview,
  onConfirmStudent,
  onUnknown,
  onConfirmPage,
  onSplit,
  onMerge,
}: {
  queue: MatchingQueue;
  canManage: boolean;
  actioning: boolean;
  onPreview: (page: CapturePage) => Promise<void>;
  onConfirmStudent: (
    submissionId: string,
    studentId: string,
    revision: number,
  ) => Promise<void>;
  onUnknown: (submissionId: string, revision: number) => Promise<void>;
  onConfirmPage: (page: CapturePage, pageNo: number) => Promise<void>;
  onSplit?: (submissionId: string, pageId: string) => Promise<void>;
  onMerge?: (targetId: string, sourceId: string) => Promise<void>;
}) {
  const [selectedId, setSelectedId] = useState(queue.submissions[0]?.id ?? "");
  const [candidateId, setCandidateId] = useState("");
  const [mergeTarget, setMergeTarget] = useState("");
  const selected =
    queue.submissions.find((item) => item.id === selectedId) ??
    queue.submissions[0];
  useEffect(() => {
    setCandidateId(selected?.student_id ?? "");
  }, [selected?.id, selected?.student_id]);
  const used = new Set(
    queue.submissions
      .filter(
        (item) =>
          item.id !== selected?.id && item.identity_status === "matched",
      )
      .map((item) => item.student_id),
  );
  const candidates = queue.candidates.filter(
    (item) => !used.has(item.id) || item.id === selected?.student_id,
  );
  if (!selected) return <Empty description="暂无待匹配答卷" />;
  return (
    <div className="matching-workspace">
      <aside className="matching-submissions">
        <div className="capture-pane-title">
          <strong>答卷</strong>
          <span>{queue.submissions.length}</span>
        </div>
        {queue.submissions.map((item, index) => (
          <button
            type="button"
            key={item.id}
            className={
              item.id === selected.id
                ? "capture-batch-row active"
                : "capture-batch-row"
            }
            onClick={() => {
              setSelectedId(item.id);
              setCandidateId(item.student_id ?? "");
            }}
          >
            <span>
              <strong>答卷 {index + 1}</strong>
              <small>{item.pages.length} 页</small>
            </span>
            <StatusTag
              tone={
                item.identity_status === "matched"
                  ? "success"
                  : item.identity_status === "unknown"
                    ? "danger"
                    : "warning"
              }
            >
              {item.identity_status === "matched"
                ? "已匹配"
                : item.identity_status === "unknown"
                  ? "未知"
                  : "待确认"}
            </StatusTag>
          </button>
        ))}
      </aside>
      <section className="matching-evidence">
        <h3>答卷页面</h3>
        {selected.pages.map((page, index) => (
          <div className="matching-page-row" key={page.id}>
            <Button
              icon={<FolderOpen size={15} />}
              onClick={() => void onPreview(page)}
            >
              第 {page.sequence_no} 页
            </Button>
            <span className="muted-text">页码</span>
            <InputNumber
              key={`${page.id}-${page.assigned_page_no ?? "unset"}`}
              min={1}
              defaultValue={page.assigned_page_no ?? undefined}
              onBlur={(event) => {
                const next = Number(event.target.value);
                if (
                  Number.isInteger(next) &&
                  next >= 1 &&
                  next !== page.assigned_page_no
                )
                  void onConfirmPage(page, next);
              }}
              onPressEnter={(event) => {
                const next = Number(event.currentTarget.value);
                if (
                  Number.isInteger(next) &&
                  next >= 1 &&
                  next !== page.assigned_page_no
                )
                  void onConfirmPage(page, next);
              }}
              disabled={!canManage || actioning}
              aria-label="确认答卷页码"
            />
            <Space>
              <StatusTag tone={statusTone(page.status)}>
                {statusLabels[page.status] ?? "未知状态"}
              </StatusTag>
              {selected.pages.length > 1 && index > 0 && onSplit ? (
                <Button
                  icon={<Scissors size={14} />}
                  aria-label="拆分为新答卷"
                  onClick={() => void onSplit(selected.id, page.id)}
                />
              ) : null}
            </Space>
          </div>
        ))}
        <div className="matching-evidence-note">
          <ScanLine size={17} />
          <span>
            系统识别到的姓名、学号或条码仅供参考，最终以您在名册中确认的学生为准。
          </span>
        </div>
      </section>
      <section className="matching-candidates">
        <h3>本次考试名册</h3>
        <Select
          showSearch
          value={candidateId || undefined}
          onChange={setCandidateId}
          placeholder="按姓名或学号查找"
          optionFilterProp="label"
          options={candidates.map((item) => ({
            value: item.id,
            label: `${item.name} · ${item.student_no} · ${item.class_name}`,
          }))}
        />
        <Space wrap>
          <Button
            type="primary"
            icon={<UserCheck size={16} />}
            loading={actioning}
            disabled={!canManage || !candidateId}
            onClick={() =>
              void onConfirmStudent(
                selected.id,
                candidateId,
                selected.identity_revision,
              )
            }
          >
            确认学生
          </Button>
          <Button
            danger
            icon={<FileQuestion size={16} />}
            loading={actioning}
            disabled={!canManage}
            onClick={() =>
              void onUnknown(selected.id, selected.identity_revision)
            }
          >
            标记未知
          </Button>
        </Space>
        {queue.submissions.length > 1 && onMerge ? (
          <Space.Compact>
            <Select
              value={mergeTarget || undefined}
              onChange={setMergeTarget}
              placeholder="合并到其他答卷"
              options={queue.submissions
                .filter((item) => item.id !== selected.id)
                .map((item, index) => ({
                  value: item.id,
                  label: `答卷 ${index + 1}`,
                }))}
            />
            <Button
              icon={<Combine size={15} />}
              disabled={!mergeTarget}
              onClick={() => void onMerge(mergeTarget, selected.id)}
            >
              合并
            </Button>
          </Space.Compact>
        ) : null}
        {selected.identity_status === "matched" ? (
          <div className="matching-confirmed">
            <Check size={17} />
            已确认，仍可重新选择并更正
          </div>
        ) : null}
      </section>
    </div>
  );
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
  const [batches, setBatches] = useState<CaptureBatch[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [detail, setDetail] = useState<CaptureBatchDetail>();
  const [loading, setLoading] = useState(true);
  const [loadingMoreBatches, setLoadingMoreBatches] = useState(false);
  const [nextBatchCursor, setNextBatchCursor] = useState("");
  const [hasMoreBatches, setHasMoreBatches] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [modalOpen, setModalOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const creationBusy = useRef(false);
  const creationCommandKey = batchCreateCommandKey(user.tenant, user.id, examId);
  const [pendingCreation, setPendingCreation] = useState(() => {
    try { return Boolean(loadBatchCreateCommand(localStorage, creationCommandKey)); } catch { return true; }
  });
  const [uploading, setUploading] = useState(false);
  const [actioning, setActioning] = useState(false);
  const [preview, setPreview] = useState<{ url: string; page: CapturePage }>();
  const [matching, setMatching] = useState<MatchingQueue>();
  const [matchingLoading, setMatchingLoading] = useState(false);
  const [processingSummaries, setProcessingSummaries] = useState<Record<string, ProcessingSummary>>({});
  const [correctionRunId, setCorrectionRunId] = useState<string>();
  const [activeTab, setActiveTab] = useState("files");
  const [reopenOpen, setReopenOpen] = useState(false);
  const [reopenReason, setReopenReason] = useState("");
  const [qualityOverridePage, setQualityOverridePage] = useState<CapturePage>();
  const [qualityOverrideReason, setQualityOverrideReason] = useState("");
  const batchesRequestRef = useRef(0);
  const detailRequestRef = useRef(0);
  const matchingRequestRef = useRef(0);
  const previewRequestRef = useRef(0);
  const batchCanManage = canManage && !["completed", "cancelled"].includes(detail?.batch.status ?? "");

  const loadBatches = useCallback(async () => {
    const requestId = ++batchesRequestRef.current;
    setLoading(true);
    setError(undefined);
    try {
      const result = await listCaptureBatches(examId, { limit: 30 });
      if (requestId !== batchesRequestRef.current) return;
      setBatches(result.batches);
      setNextBatchCursor(result.next_cursor ?? "");
      setHasMoreBatches(Boolean(result.has_more));
      setSelectedId((current) =>
        result.batches.some((batch) => batch.id === current) ? current : result.batches[0]?.id || ""
      );
    } catch (currentError) {
      if (requestId !== batchesRequestRef.current) return;
      setError(formatError(currentError));
      setNextBatchCursor("");
      setHasMoreBatches(false);
    } finally {
      if (requestId === batchesRequestRef.current) setLoading(false);
    }
  }, [examId]);

  const loadMoreBatches = useCallback(async () => {
    if (!hasMoreBatches || !nextBatchCursor || loadingMoreBatches) return;
    const requestId = ++batchesRequestRef.current;
    setLoadingMoreBatches(true);
    try {
      const result = await listCaptureBatches(examId, { limit: 30, cursor: nextBatchCursor });
      if (requestId !== batchesRequestRef.current) return;
      setBatches((current) => {
        const byID = new Map(current.map((item) => [item.id, item]));
        result.batches.forEach((item) => byID.set(item.id, item));
        return Array.from(byID.values());
      });
      setNextBatchCursor(result.next_cursor ?? "");
      setHasMoreBatches(Boolean(result.has_more));
    } catch (currentError) {
      if (requestId === batchesRequestRef.current) message.error(formatError(currentError));
    } finally {
      if (requestId === batchesRequestRef.current) setLoadingMoreBatches(false);
    }
  }, [examId, hasMoreBatches, loadingMoreBatches, message, nextBatchCursor]);

  const loadDetail = useCallback(
    async (batchId: string, quiet = false) => {
      const requestId = ++detailRequestRef.current;
      if (!batchId) {
        setDetail(undefined);
		setProcessingSummaries({});
        setDetailLoading(false);
        return;
      }
      if (!quiet) setDetailLoading(true);
      try {
        const result = await getCaptureBatch(batchId);
        if (requestId !== detailRequestRef.current) return;
        setDetail(result);
		if (!quiet && (result.batch.failed_count > 0 || result.pages.some((item) => ["needs_review", "quality_rejected", "failed"].includes(item.status)))) setActiveTab("issues");
		setProcessingSummaries(Object.fromEntries(result.processing_summaries.map((item) => [item.submission_id, item])));
        setBatches((current) =>
          current.map((item) =>
            item.id === result.batch.id ? result.batch : item,
          ),
        );
      } catch (currentError) {
        if (requestId !== detailRequestRef.current) return;
        if (!quiet) message.error(formatError(currentError));
      } finally {
        if (!quiet && requestId === detailRequestRef.current) setDetailLoading(false);
      }
    },
    [message],
  );

  const loadMatching = useCallback(
    async (batchId: string) => {
      const requestId = ++matchingRequestRef.current;
      if (!batchId) {
        setMatching(undefined);
        setMatchingLoading(false);
        return;
      }
      setMatchingLoading(true);
      try {
        const result = await getMatchingQueue(batchId);
        if (requestId === matchingRequestRef.current) setMatching(result);
      } catch (currentError) {
        if (requestId !== matchingRequestRef.current) return;
        message.error(formatError(currentError));
      } finally {
        if (requestId === matchingRequestRef.current) setMatchingLoading(false);
      }
    },
    [message],
  );

  useEffect(() => {
    detailRequestRef.current += 1;
    matchingRequestRef.current += 1;
    previewRequestRef.current += 1;
    setSelectedId("");
    setDetail(undefined);
    setMatching(undefined);
    setProcessingSummaries({});
    setPreview(undefined);
  }, [examId]);
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

  async function uploadSource(file: File) {
    if (!detail) return;
    setUploading(true);
    try {
      const uploaded = await uploadFile(file, {
        owner_type: "capture_batch",
        owner_id: detail.batch.id,
        exam_id: examId,
      });
      await registerCaptureFile(
        detail.batch.id,
        uploaded.file.id,
        crypto.randomUUID(),
      );
      await loadDetail(detail.batch.id);
      message.success(`${file.name} 已加入批次`);
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setUploading(false);
    }
  }

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

  async function matchStudent(
    submissionId: string,
    studentId: string,
    revision: number,
  ) {
    setActioning(true);
    try {
      await confirmStudentMatch(submissionId, studentId, revision);
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      message.success("学生匹配已确认");
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }

  async function markUnknown(submissionId: string, revision: number) {
    setActioning(true);
    try {
      await markStudentUnknown(
        submissionId,
        revision,
        "答卷上无法可靠识别学生身份",
      );
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      message.warning("已标记为未知答卷");
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }

  async function matchPage(page: CapturePage, pageNo: number) {
    setActioning(true);
    try {
      await confirmPageMatch(page.id, pageNo, page.revision);
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      message.success(`第 ${pageNo} 页页码已保存`);
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }

  async function changePageLifecycle(page: CapturePage) {
    setActioning(true);
    try {
      if (page.status === "deleted")
        await restoreCapturePage(page.id, page.revision, "恢复误删页面");
      else await deleteCapturePage(page.id, page.revision, "移除非答卷页面");
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      message.success(page.status === "deleted" ? "页面已恢复" : "页面已删除");
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }
  async function splitSubmission(submissionId: string, pageId: string) {
    setActioning(true);
    try {
      setMatching(
        await splitCaptureSubmission(
          selectedId,
          submissionId,
          [pageId],
          "人工拆分混扫答卷",
        ),
      );
      await loadDetail(selectedId, true);
      message.success("已拆分为新答卷");
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }
  async function mergeSubmissions(targetId: string, sourceId: string) {
    setActioning(true);
    try {
      setMatching(
        await mergeCaptureSubmissions(
          selectedId,
          targetId,
          sourceId,
          "人工合并散页答卷",
        ),
      );
      await loadDetail(selectedId, true);
      message.success("答卷已合并");
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }

  const fileColumns: TableColumnsType<CaptureFile> = [
    { title: "文件", dataIndex: "original_name", ellipsis: true },
    {
      title: "类型",
      dataIndex: "content_type",
      width: 150,
      render: (value: string) => (
        <span title={value}>
          {contentTypeLabels[value] ??
            (value ? value.split("/").pop()!.toUpperCase() : "-")}
        </span>
      ),
    },
    { title: "页数", dataIndex: "page_count", width: 72 },
    {
      title: "状态",
      width: 110,
      render: (_, item) => (
        <StatusTag tone={statusTone(item.status)}>
          {statusLabels[item.status] ?? "未知状态"}
        </StatusTag>
      ),
    },
    {
      title: "问题",
      width: 200,
      render: (_, item) =>
        item.error_code ? (
          <Tooltip title={item.error_code}>
            <span>
              {errorLabels[item.error_code] ?? "处理失败，请删除后重新导入"}
            </span>
          </Tooltip>
        ) : item.status === "duplicate" ? (
          "内容与批次内文件重复"
        ) : (
          "-"
        ),
    },
  ];
  const pageColumns: TableColumnsType<CapturePage> = [
    { title: "扫描顺序", dataIndex: "sequence_no", width: 90 },
    { title: "原文件页码", dataIndex: "source_index", width: 100 },
    { title: "答卷页码", dataIndex: "assigned_page_no", width: 90 },
    {
      title: "旋转",
      dataIndex: "rotation_degrees",
      width: 72,
      render: (value: number) => `${value}°`,
    },
    {
      title: "状态",
      width: 110,
      render: (_, item) => (
        <StatusTag tone={statusTone(item.status)}>
          {statusLabels[item.status] ?? "未知状态"}
        </StatusTag>
      ),
    },
    {
      title: "模板判断",
      width: 220,
      render: (_, item) => {
        if (item.status === "page_matching") return "正在比较已锁定模板…";
        const candidates = item.match_candidates ?? [];
        if (!candidates.length) return "-";
        const top = candidates[0];
        const name = String(top.template_name || "候选模板");
        const version = Number(top.version_no || 0);
        const score = Math.round(Number(top.score || 0) * 100);
        const detailText = candidates.slice(0, 3).map((candidate) => {
          const candidateName = String(candidate.template_name || candidate.template_id || "候选模板");
          const candidateVersion = Number(candidate.version_no || 0);
          return `${candidateName}${candidateVersion ? ` v${candidateVersion}` : ""} · ${Math.round(Number(candidate.score || 0) * 100)}%`;
        }).join("；");
        return <Tooltip title={detailText}><span>{item.status === "needs_review" ? "需确认：" : "已识别："}{name}{version ? ` v${version}` : ""} · {score}%</span></Tooltip>;
      },
    },
    {
      title: "操作",
      width: 280,
      render: (_, item) => (
        <Space>
          <Button
            size="small"
            icon={<FolderOpen size={14} />}
            onClick={() => void showPage(item)}
            aria-label="查看页面"
          />
          <Button
            size="small"
            icon={<RotateCw size={14} />}
            onClick={() => void rotatePage(item)}
            disabled={!batchCanManage || item.status === "deleted"}
            aria-label="顺时针旋转"
          />
          <Button
            size="small"
            danger={item.status !== "deleted"}
            icon={
              item.status === "deleted" ? (
                <Undo2 size={14} />
              ) : (
                <Trash2 size={14} />
              )
            }
            onClick={() => void changePageLifecycle(item)}
            disabled={!batchCanManage}
            aria-label={item.status === "deleted" ? "恢复页面" : "删除页面"}
          />
          {["review", "failed"].includes(String(item.page_identity.quality_status ?? "")) &&
          item.status !== "deleted" ? (
            <Button
              size="small"
              type="primary"
              disabled={!batchCanManage || !item.submission_page_id}
              onClick={() => {
                setQualityOverrideReason("");
                setQualityOverridePage(item);
              }}
            >
              质量放行
            </Button>
          ) : null}
        </Space>
      ),
    },
  ];

  const progressPercent = useMemo(() => {
    if (!detail) return null;
    if (detail.batch.status === "completed") return 100;
    if (detail.batch.page_count === 0) return null;
    return Math.min(
      100,
      Math.round(
        (detail.pages.filter((item) => item.status === "ready").length /
          detail.batch.page_count) *
          100,
      ),
    );
  }, [detail]);
  const summary = useMemo(
    () =>
      detail
        ? [
            { label: "文件", value: detail.batch.file_count },
            { label: "页面", value: detail.batch.page_count },
            { label: "答卷", value: detail.batch.submission_count },
            { label: "待确认", value: detail.batch.review_count },
            { label: "失败", value: detail.batch.failed_count },
          ]
        : [],
    [detail],
  );
  const submissions = useMemo(
    () =>
      detail
        ? (Array.from(
            new Set(
              detail.pages.map((item) => item.submission_id).filter(Boolean),
            ),
          ) as string[])
        : [],
    [detail],
  );
  const issuePages = useMemo(
    () =>
      detail
        ? detail.pages.filter((item) =>
            ["needs_review", "quality_rejected", "failed"].includes(
              item.status,
            ),
          )
        : [],
    [detail],
  );
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
