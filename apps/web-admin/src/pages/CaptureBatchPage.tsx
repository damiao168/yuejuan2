import { useEffect, useMemo, useRef, useState } from "react";
import type { SessionUser } from "../auth/session";
import { acceptBatchCreateCommand, batchCreateCommandKey, loadBatchCreateCommand } from "../features/capture/batchCreateCommand";
import { findCaptureTargets, getCaptureBatch, recoverCaptureBatchCommand } from "../api/capture";
import { hashQueryParam } from "../router/query";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import {
  App,
  Alert,
  Button,
  Empty,
  Form,
  Input,
  Modal,
  Select,
  Space,
  type UploadProps,
} from "antd";
import {
  Plus,
  RefreshCw,
} from "lucide-react";
import {
  confirmRegistration,
  createCaptureBatch,
  overrideCapturePageQuality,
  processCaptureBatch,
  processSubmissionPages,
  reopenCaptureBatch,
  retryRegistration,
  updateCapturePage,
  type CaptureBatch,
  type CapturePage,
  type CreateCaptureBatchInput,
} from "../api/capture";
import { CaptureCollectionPanel, type CaptureCollectionTarget } from "../features/capture/CaptureCollectionPanel";
import { useCaptureCollection } from "../features/capture/useCaptureCollection";
import { excludeCaptureFile, type CaptureCollectionIssue } from "../api/captureCollection";
import { downloadFileBlob } from "../api/files";
import { ErrorState, LoadingState } from "../components/PageState";
import { RegistrationCorrectionWorkspace } from "../components/RegistrationCorrectionWorkspace";
import { useCaptureBatchList } from "../features/capture/useCaptureBatchList";
import { useCaptureBatchDetail } from "../features/capture/useCaptureBatchDetail";
import { useCaptureMatchActions } from "../features/capture/useCaptureMatchActions";
import { captureProgressPercent, captureSummary, captureSubmissionIds, captureIssuePages } from "../features/capture/captureBatchSummary";
import { useCaptureUpload } from "../features/capture/useCaptureUpload";
import { captureFileColumns, capturePageColumns } from "../features/capture/captureColumns";

import { CaptureBatchListPanel, CaptureBatchWorkspace } from "../features/capture/CaptureBatchPanels";

function formatError(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请重试");
}

export function CaptureBatchPage({
  user,
  examId,
  canManage,
  examStatus = "collecting",
  onCollectionChanged,
}: Readonly<{
  user: SessionUser;
  examId: string;
  canManage: boolean;
  examStatus?: string;
  onCollectionChanged?: () => Promise<unknown>;
}>) {
  const { message } = App.useApp();
  const [form] = Form.useForm<{
    name: string;
    source_type: CaptureBatch["source_type"];
    reason?: string;
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
  const captureTargetQuery = [hashQueryParam("batch"), hashQueryParam("submission"), hashQueryParam("student")].filter(Boolean).join(":");
  const [recordsExpanded, setRecordsExpanded] = useState(Boolean(captureTargetQuery));
  const [pagesPerStudent, setPagesPerStudent] = useState<number>();
  const [locatedSubmission,setLocatedSubmission]=useState("");
  const [locatedBatch, setLocatedBatch] = useState("");
  const [locatedTab, setLocatedTab] = useState(hashQueryParam("tab") || "matching");
  const [locationError,setLocationError]=useState("");
  const [locationAttempt,setLocationAttempt]=useState(0);
  const [locating, setLocating] = useState(() => Boolean(hashQueryParam("batch") || hashQueryParam("submission") || hashQueryParam("student")));
  const [locationChoices, setLocationChoices] = useState<Array<{ batch: CaptureBatch; submissionId: string; candidateNo: string }>>([]);
  const [replacement, setReplacement] = useState<{ fileId: string; batchId: string; name: string; reason: string }>();
  const { detail, detailError, detailLoading, matching, matchingError, setMatching, matchingLoading, processingSummaries,
    loadDetail, loadMatching, resetDetail } = useCaptureBatchDetail(setBatches, setActiveTab, selectedId, selectedId === locatedBatch ? locatedTab : undefined);
  const { uploading, pendingUpload, uploadSources, retryUpload, discardUpload } = useCaptureUpload(detail, examId, loadDetail, selectedId);
  const recordsVisible = recordsExpanded || uploading;
  const collection = useCaptureCollection(examId, `${batches.map(batch => `${batch.id}:${batch.revision}`).join(",")}:${detail?.batch.revision ?? ""}:${uploading}:${actioning}`);
  const currentExamStatus = collection.summary?.exam_status ?? examStatus;
  const supplement = ["grading", "reviewing"].includes(currentExamStatus);
  const canCreateBatch = canManage && (currentExamStatus === "collecting" || supplement);
  const [reopenOpen, setReopenOpen] = useState(false);
  const [reopenReason, setReopenReason] = useState("");
  const [qualityOverridePage, setQualityOverridePage] = useState<CapturePage>();
  const [qualityOverrideReason, setQualityOverrideReason] = useState("");
  const previewRequestRef = useRef(0);
  const locationRequest = useRef(0);
  const currentExam = useRef(examId);
  if (currentExam.current !== examId) { currentExam.current = examId; locationRequest.current += 1; }
  const unfinishedBatch = !["completed", "cancelled"].includes(detail?.batch.status ?? "");
  // 进入阅卷不等于本场导入已结束；旧流程留下的未封存批次仍可处理，关闭台账后只能新建补传。
  const canEditSelectedBatch = canManage && collection.summary?.closed !== true && (currentExamStatus === "collecting" ||
    (supplement && (detail?.batch.purpose === "supplement" || (collection.summary?.closed === false && unfinishedBatch))));
  const batchCanManage = canEditSelectedBatch && !locating && !uploading && detail?.batch.id === selectedId && !detailLoading && !detailError && !["completed", "cancelled"].includes(detail?.batch.status ?? "");

  const defaultPages = collection.summary?.pages_per_student;
  // biome-ignore lint/correctness/useExhaustiveDependencies: 换考试重置展开状态；新的明确定位自动展开，普通批次刷新保留用户选择。
  useEffect(() => { setRecordsExpanded(Boolean(captureTargetQuery)); }, [examId, captureTargetQuery]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 换批次使用本场模板页数重新初始化，不携带另一批次的手动设置。
  useEffect(() => { setPagesPerStudent(defaultPages && defaultPages >= 1 && defaultPages <= 20 ? defaultPages : undefined); }, [examId, selectedId]);
  useEffect(() => {
    // 模板事实可能晚于批次到达；只补尚未选择的值，后续刷新不能覆盖用户的显式修改。
    if (defaultPages && Number.isInteger(defaultPages) && defaultPages >= 1 && defaultPages <= 20) setPagesPerStudent(current => current ?? defaultPages);
  }, [defaultPages]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 换考试时清理旧批次选择及影像请求；resetDetail 回调身份不能代替考试身份边界。
  useEffect(() => {
    resetDetail();
    previewRequestRef.current += 1;
    setSelectedId("");
    setPreview(undefined);
    setReplacement(undefined);
  }, [examId, resetDetail, setSelectedId]);
  useEffect(() => () => { locationRequest.current += 1; }, []);
  useEffect(() => {
    void loadBatches();
  }, [loadBatches]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: locationAttempt 用于主动重试深链定位，保留同一考试的显式重新查找能力。
  useEffect(() => {
    const batch = hashQueryParam("batch"), submission = hashQueryParam("submission"), student = hashQueryParam("student");
    if (loading || (!batch && !submission && !student)) return;
    let active = true;
    setLocating(true); setLocationError(""); setLocationChoices([]);
    const locate = async () => {
      const targets = batch ? [{ batch_id: batch, submission_id: submission, candidate_no: "" }] : (await findCaptureTargets(examId, submission, student)).targets;
      const unique = [...new Map(targets.map(target => [`${target.batch_id}:${target.submission_id}`, target])).values()];
      if (!unique.length) throw new Error("尚未找到该名册对象的采集页面，请核查漏扫或未确认学生的答卷。");
      const choices = await Promise.all(unique.map(async target => {
        const result = await getCaptureBatch(target.batch_id);
        if (result.batch.exam_id !== examId) throw new Error("答卷不属于当前考试");
        return { batch: result.batch, submissionId: target.submission_id, candidateNo: target.candidate_no };
      }));
      if (!active) return;
      setBatches(current => [...new Map([...current, ...choices.map(choice => choice.batch)].map(value => [value.id, value])).values()]);
      if (choices.length > 1) {
        // 同一名册对象可能有重扫/重复答卷，不能静默把第一份当作需要处理的唯一对象。
        setLocationChoices(choices); setSelectedId(""); setLocatedSubmission(""); setLocatedBatch(""); return;
      }
      setSelectedId(choices[0].batch.id); setLocatedBatch(choices[0].batch.id); setLocatedSubmission(choices[0].submissionId); setLocatedTab(hashQueryParam("tab") || "matching"); setActiveTab(hashQueryParam("tab") || "matching"); setLocating(false);
    };
    void locate().catch(cause => { if (active) { setLocationError(formatError(cause)); setLocating(false); } });
    return () => { active = false; };
  }, [examId, loading, locationAttempt, setBatches, setSelectedId]);
  useEffect(() => {
    previewRequestRef.current += 1;
    setPreview(undefined);
    setCorrectionRunId(undefined);
    setQualityOverridePage(undefined);
    setQualityOverrideReason("");
    setReopenOpen(false);
    void loadDetail(selectedId);
  }, [loadDetail, selectedId]);
  useEffect(() => {
    void loadMatching(selectedId);
  }, [loadMatching, selectedId]);
  useEffect(() => {
    if (detail?.batch.id !== selectedId || !["uploading", "processing"].includes(detail.batch.status))
      return;
    const timer = window.setInterval(
      () => void loadDetail(detail.batch.id, true),
      3000,
    );
    return () => window.clearInterval(timer);
  }, [detail, loadDetail, selectedId]);
  useEffect(
    () => () => {
      if (preview?.url) URL.revokeObjectURL(preview.url);
    },
    [preview],
  );
  // 本地仍有待确认命令时复用原载荷和幂等键；页面表单的新编辑不能偷偷改变已经发出的操作。
  async function submitBatch() {
    if (creationBusy.current || !canCreateBatch) return;
    creationBusy.current = true;
    try {
      const pending = loadBatchCreateCommand(localStorage, creationCommandKey);
      const values: Omit<CreateCaptureBatchInput, "idempotency_key"> = pending?.payload ?? {
        ...await form.validateFields(), ...(supplement ? { purpose: "supplement" as const } : {}),
      };
      setCreating(true);
      const command = acceptBatchCreateCommand(localStorage, creationCommandKey, values);
      setPendingCreation(true);
      const recovered = pending ? await recoverCaptureBatchCommand(examId, command.payload.idempotency_key) : null;
      const result = recovered?.command.status === "succeeded" && recovered.command.batch
        ? { batch: recovered.command.batch }
        : await createCaptureBatch(examId, command.payload);
      try { localStorage.removeItem(creationCommandKey); } catch { /* 恢复仍返回同一个批次。 */ }
      setPendingCreation(false);
      setModalOpen(false);
      setRecordsExpanded(true);
      form.resetFields();
      await loadBatches();
      setSelectedId(result.batch.id);
      await collection.load();
      message.success(supplement ? "补传批次已创建，当前阅卷阶段保持不变" : "采集批次已创建");
    } catch (currentError) {
      if (currentError instanceof ApiClientError && currentError.code === "capture_invalid_input") {
        try { localStorage.removeItem(creationCommandKey); setPendingCreation(false); } catch { /* 保留恢复数据。 */ }
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
    disabled: !batchCanManage || !pagesPerStudent || Boolean(pendingUpload),
    beforeUpload: (file, files) => {
      // antd 为每个文件调用一次；只用首个回调提交完整选择，避免同一选择被拆成多个学生组。
      if (file === files[0] && batchCanManage && pagesPerStudent && !pendingUpload) {
        const original = replacement?.batchId === selectedId ? replacement : undefined;
        if (!original) { void uploadSources(files, pagesPerStudent); return false; }
        void uploadSources(files, pagesPerStudent, async fileIds => {
          if (!fileIds[0]) throw new Error("尚未收到替代文件登记结果，请重试本次上传");
          await excludeCaptureFile(original.fileId, original.reason, fileIds[0]);
          if (currentExam.current === examId) {
            setReplacement(value => value?.fileId === original.fileId ? undefined : value);
            await collection.load();
            message.success("替代答卷已登记，原选择的全部材料已退出有效输入；请等待新答卷处理完成");
          }
        });
      }
      return false;
    },
  };

  async function startProcessing() {
    if (!detail || !batchCanManage) return;
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

  function reopenBatch() {
    setReopenReason("");
    setReopenOpen(true);
  }

  async function confirmReopen() {
    if (detail?.batch.id !== selectedId || !canEditSelectedBatch || !reopenReason.trim()) return;
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
    if (!batchCanManage || !detail?.pages.some((page) => page.submission_id === submissionId)) return;
    if (detail.pages.some(page => page.submission_id === submissionId && page.page_identity.grouping_status && page.page_identity.grouping_status !== "complete")) {
      message.error("这份答卷尚未完成分组，请先核对缺页、文件失败或学生身份冲突");
      return;
    }
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
    if (!detail || !batchCanManage || qualityOverridePage?.capture_batch_id !== selectedId || !qualityOverridePage?.submission_page_id || !qualityOverrideReason.trim()) return;
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
    if (page.capture_batch_id !== selectedId) return;
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
    if (!batchCanManage || page.capture_batch_id !== selectedId) return;
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

  async function locateCollectionTarget(target: CaptureCollectionTarget) {
    if (uploading || locating || !target.batchId) return false;
    setRecordsExpanded(true);
    setLocating(true);
    setLocationError("");
    const sourceExam = examId;
    const request = ++locationRequest.current;
    try {
      const result = await getCaptureBatch(target.batchId);
      if (request !== locationRequest.current || sourceExam !== currentExam.current) return false;
      if (result.batch.exam_id !== sourceExam) throw new Error("答卷不属于当前考试");
      setBatches(current => [...new Map([...current, result.batch].map(batch => [batch.id, batch])).values()]);
      setLocationChoices([]);
      setLocatedBatch(target.batchId);
      setLocatedSubmission(target.submissionId ?? "");
      setLocatedTab(target.tab);
      setSelectedId(target.batchId);
      setActiveTab(target.tab);
      document.getElementById("capture-batch-workspace")?.scrollIntoView?.({ behavior: "smooth", block: "start" });
      return true;
    } catch (cause) {
      if (request === locationRequest.current && sourceExam === currentExam.current) setLocationError(formatError(cause));
      return false;
    } finally {
      if (request === locationRequest.current && sourceExam === currentExam.current) setLocating(false);
    }
  }

  async function prepareReplacement(issue: CaptureCollectionIssue, reason: string) {
    if (!issue.file_id || !await locateCollectionTarget({ batchId: issue.batch_id, tab: "files" })) throw new Error("暂时无法定位待替代材料，请刷新核对后重试");
    setPagesPerStudent(defaultPages && Number.isInteger(defaultPages) && defaultPages >= 1 && defaultPages <= 20 ? defaultPages : undefined);
    setReplacement({ fileId: issue.file_id, batchId: issue.batch_id, name: issue.name, reason });
  }
  async function resolveRegistration(runId: string, action: "confirm" | "retry") {
    setActioning(true);
    try {
      if (action === "confirm") {
        await confirmRegistration(runId, "人工核对版面对齐边界与题目区域正确");
      } else {
        await retryRegistration(runId);
      }
      // 确认与重试成功后都回读当前批次；刷新和提示不属于某一个动作分支。
      await loadDetail(selectedId, true);
      message.success(action === "confirm" ? "版面对齐已确认" : "已重新排队对齐");
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setActioning(false);
    }
  }

  if (correctionRunId) return <RegistrationCorrectionWorkspace runId={correctionRunId} canManage={batchCanManage} onClose={() => setCorrectionRunId(undefined)} onChanged={async () => { await loadDetail(selectedId, true); }} />;
  return (
    <div className="capture-batch-page">
      <section className="member-management-heading">
        <div>
          <h1>答卷导入</h1>
          <p>{supplement ? "补传答卷并核对学生，当前阅卷阶段保持不变。" : "按本场名册核对学生答卷，全部就绪后结束导入。"}</p>
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
            disabled={!canCreateBatch}
          >
            {supplement ? "补传答卷" : "导入答卷"}
          </Button>
        </Space>
      </section>
      <CaptureCollectionPanel key={`${user.tenant}:${user.id}:${examId}`} examId={examId} actorKey={`${user.tenant}:${user.id}`} summary={collection.summary} loading={collection.loading} error={collection.error}
        canManage={canCreateBatch} localBusy={uploading || Boolean(pendingUpload) || actioning || creating || locating}
        onRefresh={collection.load} onLocate={target => void locateCollectionTarget(target)}
        onReplace={prepareReplacement}
        onCompleted={async value => {
          collection.acceptSummary(value);
          await Promise.all([loadBatches(), loadDetail(selectedId, true), onCollectionChanged?.()]);
        }}
        onMaterialChanged={async batchId => { await loadBatches(); if (batchId === selectedId) await loadDetail(batchId, true); }} />
      {replacement?.batchId === selectedId ? <Alert type="warning" showIcon message={`正在替代：${replacement.name}`} description="请先选择每名学生页数，再一次上传本次选择所对应的全部完整答卷。登记成功后，原选择的全部文件及其答卷将退出有效输入；新答卷仍需处理成功。" action={<Button disabled={uploading || Boolean(pendingUpload)} onClick={() => setReplacement(undefined)}>取消替代</Button>} /> : null}
      {locationError ? <Alert type="warning" showIcon message={locationError} action={<Button onClick={() => setLocationAttempt(value => value + 1)}>重试定位</Button>} /> : null}
      {locationChoices.length > 1 ? <Alert type="info" message={`定位到 ${locationChoices.length} 份采集答卷，请选择本次核对对象`} description={<Select aria-label="选择名册对应采集答卷" style={{ minWidth: 320 }} value={locatedSubmission ? `${selectedId}:${locatedSubmission}` : undefined} options={locationChoices.map((choice, index) => ({ value: `${choice.batch.id}:${choice.submissionId}`, label: `${choice.batch.name} · 考号 ${choice.candidateNo || "待匹配"} · 答卷 ${index + 1}` }))} onChange={value => { const choice = locationChoices.find(item => `${item.batch.id}:${item.submissionId}` === value); if (choice) { setSelectedId(choice.batch.id); setLocatedBatch(choice.batch.id); setLocatedSubmission(choice.submissionId); setActiveTab(hashQueryParam("tab") || "matching"); setLocating(false); } }} />} /> : null}
      <section className="capture-records" aria-label="批次文件与处理记录">
        <button type="button" className="capture-records-toggle" aria-expanded={recordsVisible} aria-controls="capture-processing-records" disabled={uploading} onClick={() => setRecordsExpanded(value => !value)}>
          <span><strong>批次文件与处理记录</strong><small>{batches.length} 个批次 · 查看文件、质检、页面处理与学生匹配</small></span>
          <span>{uploading ? "正在上传" : recordsVisible ? "收起" : "展开"}</span>
        </button>
        {/* 保留已挂载的详情和上传进度；收起只影响展示，不能丢失当前操作或本次选择。 */}
        <div id="capture-processing-records" className="capture-records-body" hidden={!recordsVisible}>
      {loading ? <LoadingState label="正在加载采集批次" /> : error ? <ErrorState message={`采集批次加载失败：${error}`} onRetry={() => void loadBatches()} /> : batches.length === 0 ? (
        <Empty description="尚未创建采集批次">
          <Button type="primary" onClick={() => setModalOpen(true)} disabled={!canCreateBatch}>
            创建第一个批次
          </Button>
        </Empty>
      ) : (
        <div className="capture-batch-layout" id="capture-batch-workspace" aria-label="批次与材料详情">
          <CaptureBatchListPanel batches={batches} selectedId={selectedId} setSelectedId={setSelectedId} disabled={uploading} hasMoreBatches={hasMoreBatches} loadingMoreBatches={loadingMoreBatches} loadMoreBatches={loadMoreBatches} />
          <main className="capture-batch-workspace">
            <CaptureBatchWorkspace detail={detail} detailError={detailError} detailLoading={detailLoading} selectedId={selectedId}
              loadDetail={loadDetail} canManage={canEditSelectedBatch} batchCanManage={batchCanManage} progressPercent={progressPercent}
              summary={summary} uploadProps={uploadProps} uploading={uploading} actioning={actioning} startProcessing={startProcessing}
              uploadLabel={replacement?.batchId === selectedId ? "上传替代答卷" : undefined}
              pagesPerStudent={pagesPerStudent} setPagesPerStudent={setPagesPerStudent} pendingUpload={pendingUpload} retryUpload={retryUpload} discardUpload={discardUpload}
              reopenBatch={reopenBatch} activeTab={activeTab} setActiveTab={setActiveTab}
              issuePages={issuePages} pageColumns={pageColumns} fileColumns={fileColumns}
              uploadRepair={() => { setActiveTab("files"); message.info("请按学生页序一次选择完整答卷重新导入；新选择独立分组，系统会保留原失败记录"); }}
              onOverride={(page) => { setQualityOverrideReason(""); setQualityOverridePage(page); }}
              processingProps={{ submissions, processingSummaries, resolveRegistration, setCorrectionRunId, startPageProcessing }}
              matchingProps={{ loading: matchingLoading, error: matchingError, onRetry: () => void loadMatching(selectedId), queue: matching,
                workspaceProps: { initialSubmissionId: locatedSubmission, canManage: batchCanManage, actioning, onPreview: showPage,
                  onConfirmStudent: matchStudent, onUnknown: markUnknown, onConfirmPage: matchPage, onSplit: splitSubmission, onMerge: mergeSubmissions } }} />
          </main>
        </div>
      )}
        </div>
      </section>
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
        title={pendingCreation ? "恢复上次采集批次创建" : supplement ? "新建补传批次" : "新建采集批次"}
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
          {supplement ? <Form.Item name="reason" label="补传原因" rules={[{ required: true, whitespace: true, message: "请填写补传原因" }, { max: 300 }]}><Input.TextArea rows={3} maxLength={300} placeholder="例如：补交缺考学生答卷或补扫遗漏页面" /></Form.Item> : null}
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
