import { App, Form, type UploadProps } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import { confirmRegistration, createCaptureBatch, findCaptureTargets, getCaptureBatch, overrideCapturePageQuality, processCaptureBatch, processSubmissionPages, recoverCaptureBatchCommand, reopenCaptureBatch, retryRegistration, updateCapturePage, type CaptureBatch, type CapturePage, type CreateCaptureBatchInput } from "../api/capture";
import { excludeCaptureFile, type CaptureCollectionIssue } from "../api/captureCollection";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { downloadFileBlob } from "../api/files";
import type { SessionUser } from "../auth/session";
import { acceptBatchCreateCommand, batchCreateCommandKey, loadBatchCreateCommand } from "../features/capture/batchCreateCommand";
import { captureIssuePages, captureProgressPercent, captureSubmissionIds, captureSummary } from "../features/capture/captureBatchSummary";
import { type CaptureCollectionTarget } from "../features/capture/CaptureCollectionPanel";
import { captureFileColumns, capturePageColumns } from "../features/capture/captureColumns";
import { useCaptureBatchDetail } from "../features/capture/useCaptureBatchDetail";
import { useCaptureBatchList } from "../features/capture/useCaptureBatchList";
import { useCaptureBatchPolling } from "../features/capture/useCaptureBatchPolling";
import { useCaptureCollection } from "../features/capture/useCaptureCollection";
import { useCaptureMatchActions } from "../features/capture/useCaptureMatchActions";
import { useCaptureUpload } from "../features/capture/useCaptureUpload";
import { hashQueryParam } from "../router/query";

import { captureSelectionError } from "../features/capture/CaptureFileOrderPreview";


export function formatError(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请重试");
}
// 状态、请求代次和互斥 ref 仍由同一次 Hook 调用拥有；视图只读取本次渲染的快照与动作。
export function useCaptureBatchPage({
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
  const newFilesInput = useRef<HTMLInputElement>(null);
  const [newFiles, setNewFiles] = useState<File[]>([]);
  const [newPdfPending, setNewPdfPending] = useState(false);
  const [appendPdfPending, setAppendPdfPending] = useState(false);
  const [newUpload, setNewUpload] = useState<{ examId: string; batchId: string; files: File[]; pages: number }>();
  const [appendPreview, setAppendPreview] = useState<{ batchId: string; examId: string; files: File[]; pages: number; original?: typeof replacement }>();
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
  const [pagesPerStudent, setPagesPerStudent] = useState<number>();
  const [locatedSubmission,setLocatedSubmission]=useState("");
  const [locatedBatch, setLocatedBatch] = useState("");
  const [locatedTab, setLocatedTab] = useState(hashQueryParam("tab") || "matching");
  const [locationError,setLocationError]=useState("");
  const [locationAttempt,setLocationAttempt]=useState(0);
  const [locating, setLocating] = useState(() => Boolean(hashQueryParam("batch") || hashQueryParam("submission") || hashQueryParam("student")));
  const [locationChoices, setLocationChoices] = useState<Array<{ batch: CaptureBatch; submissionId: string; candidateNo: string }>>([]);
  const [replacement, setReplacement] = useState<{ fileId: string; batchId: string; name: string; reason: string; code: string }>();
  const { detail, detailError, detailLoading, matching, matchingError, setMatching, matchingLoading, processingSummaries,
    loadDetail, loadMatching, resetDetail } = useCaptureBatchDetail(setBatches, setActiveTab, selectedId, selectedId === locatedBatch ? locatedTab : undefined);
  const { uploading, pendingUpload, uploadSources, retryUpload, discardUpload } = useCaptureUpload(detail, examId, loadDetail, selectedId);
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
  const handledLocation = useRef("");
  const currentExam = useRef(examId);
  if (currentExam.current !== examId) { currentExam.current = examId; locationRequest.current += 1; }
  const unfinishedBatch = !["completed", "cancelled"].includes(detail?.batch.status ?? "");
  // 进入阅卷不等于本场导入已结束；旧流程留下的未封存批次仍可处理，关闭台账后只能新建补传。
  const canEditSelectedBatch = canManage && collection.summary?.closed !== true && (currentExamStatus === "collecting" ||
    (supplement && (detail?.batch.purpose === "supplement" || (collection.summary?.closed === false && unfinishedBatch))));
  const batchCanManage = canEditSelectedBatch && !locating && !uploading && detail?.batch.id === selectedId && !detailLoading && !detailError && !["completed", "cancelled"].includes(detail?.batch.status ?? "");

  function matchingReadOnlyReason() {
    if (locating || detailLoading || detail?.batch.id !== selectedId) return "正在读取所选答卷，读取完成后才能修改学生匹配。";
    if (detailError) return "批次读取失败，暂不能修改学生匹配。请先重试读取批次。";
    if (uploading) return "正在上传答卷，请等待上传完成后再确认学生。";
    if (["finalized", "published", "archived"].includes(currentExamStatus)) return "考试已定稿、发布或归档，当前只能查看答卷，不能修改学生匹配。";
    if (!canManage) return "当前账号没有修改答卷的权限，请联系本场考试管理员处理。";
    if (detail?.batch.status === "cancelled") return "本批次已取消，当前只能查看答卷，不能修改学生匹配。";
    if (collection.summary?.closed === true) return "本场导入已结束，原批次只能查看，不能直接修改学生匹配。";
    if (detail?.batch.status === "completed") return canEditSelectedBatch
      ? "本批次已完成，学生匹配和页码修改已锁定。请先重开批次，再核对学生。"
      : "本批次已完成，当前阶段不能重开原批次，学生匹配和页码仅可查看。";
    return "当前考试阶段仅可处理允许编辑的批次，此批次的学生匹配暂不能修改。";
  }

  const defaultPages = collection.summary?.pages_per_student;
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
    setNewFiles([]); setNewUpload(undefined); setAppendPreview(undefined); setModalOpen(false);
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
    // 深链只定位一次；批次列表刷新不能覆盖之后的手动选择，主动重试另起一次定位。
    const locationKey = JSON.stringify([examId, batch, submission, student, locationAttempt]);
    if (handledLocation.current === locationKey) return;
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
      handledLocation.current = locationKey;
      setBatches(current => [...new Map([...current, ...choices.map(choice => choice.batch)].map(value => [value.id, value])).values()]);
      if (choices.length > 1) {
        // 同一名册对象可能有重扫/重复答卷，不能静默把第一份当作需要处理的唯一对象。
        setLocationChoices(choices); setSelectedId(""); setLocatedSubmission(""); setLocatedBatch(""); return;
      }
      setSelectedId(choices[0].batch.id); setLocatedBatch(choices[0].batch.id); setLocatedSubmission(choices[0].submissionId); setLocatedTab(hashQueryParam("tab") || "matching"); setActiveTab(hashQueryParam("tab") || "matching"); setLocating(false);
    };
    void locate().catch(cause => { if (active) { handledLocation.current = locationKey; setLocationError(formatError(cause)); setLocating(false); } });
    return () => { active = false; };
  }, [examId, captureTargetQuery, loading, locationAttempt, setBatches, setSelectedId]);
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
  useCaptureBatchPolling(detail?.batch.id === selectedId ? selectedId : undefined, detail?.batch.status, loadDetail);
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
    const isCurrent = () => currentExam.current === examId;
    try {
      const pending = loadBatchCreateCommand(localStorage, creationCommandKey);
      if (!pending) {
        const error = captureSelectionError(newFiles, pagesPerStudent);
        if (error || newPdfPending) throw new Error(error || "请先应用或恢复PDF页序");
      }
      const values: Omit<CreateCaptureBatchInput, "idempotency_key"> = pending?.payload ?? {
        ...await form.validateFields(), ...(supplement ? { purpose: "supplement" as const } : {}),
      };
      if (!isCurrent()) return;
      setCreating(true);
      const command = acceptBatchCreateCommand(localStorage, creationCommandKey, values);
      setPendingCreation(true);
      const recovered = pending ? await recoverCaptureBatchCommand(examId, command.payload.idempotency_key) : null;
      const result = recovered?.command.status === "succeeded" && recovered.command.batch
        ? { batch: recovered.command.batch }
        : await createCaptureBatch(examId, command.payload);
      try { localStorage.removeItem(creationCommandKey); } catch { /* 恢复仍返回同一个批次。 */ }
      // 创建回执属于原考试；切换后的页面不能接收旧文件选择或被旧结果改选批次。
      if (!isCurrent()) return;
      setPendingCreation(false);
      setModalOpen(false);
      form.resetFields();
      await loadBatches();
      if (!isCurrent()) return;
      setSelectedId(result.batch.id);
      if (newFiles.length && pagesPerStudent) {
        setNewUpload({ examId, batchId: result.batch.id, files: newFiles, pages: pagesPerStudent });
        setNewFiles([]);
      }
      await collection.load();
      message.success(supplement ? "补传批次已创建，当前阅卷阶段保持不变" : "采集批次已创建");
    } catch (currentError) {
      if (currentError instanceof ApiClientError && currentError.code === "capture_invalid_input") {
        try { localStorage.removeItem(creationCommandKey); setPendingCreation(false); } catch { /* 保留恢复数据。 */ }
      }
      if (isCurrent()) message.error(formatError(currentError));
    } finally {
      creationBusy.current = false;
      setCreating(false);
    }
  }

  useEffect(() => {
    if (!newUpload || newUpload.examId !== examId || detail?.batch.id !== newUpload.batchId || selectedId !== newUpload.batchId || !batchCanManage) return;
    // 创建回执与当前批次详情都到达后才上传；先取走待办，重绘不能重复发出同次选择。
    setNewUpload(undefined);
    void uploadSources(newUpload.files, newUpload.pages);
  }, [newUpload, examId, detail?.batch.id, selectedId, batchCanManage, uploadSources]);

  const uploadProps: UploadProps = {
    multiple: true,
    showUploadList: false,
    accept: ".pdf,.png,.jpg,.jpeg,.tif,.tiff",
    disabled: !batchCanManage || !pagesPerStudent || Boolean(pendingUpload),
    beforeUpload: (file, files) => {
      // antd 为每个文件调用一次；首个回调只建立本地预检，同一次选择不能被拆成多个学生组。
      if (file === files[0] && batchCanManage && pagesPerStudent && !pendingUpload) {
        const original = replacement?.batchId === selectedId ? replacement : undefined;
        setAppendPreview({ batchId: selectedId, examId, files: [...files], pages: pagesPerStudent, original });
      }
      return false;
    },
  };

  function confirmAppendPreview() {
    const selection = appendPreview;
    if (!selection || appendPdfPending || !batchCanManage || selection.batchId !== selectedId || selection.examId !== examId || captureSelectionError(selection.files, selection.pages)) return;
    setAppendPreview(undefined);
    const original = selection.original;
    if (!original) { void uploadSources(selection.files, selection.pages); return; }
    void uploadSources(selection.files, selection.pages, async fileIds => {
          if (!fileIds[0]) throw new Error("尚未收到替代文件登记结果，请重试本次上传");
          await excludeCaptureFile(original.fileId, original.reason, fileIds[0]);
          if (currentExam.current === examId) {
            setReplacement(value => value?.fileId === original.fileId ? undefined : value);
            await collection.load();
            message.success("替代答卷已登记，原选择的全部材料已退出有效输入；请等待新答卷处理完成");
          }
        });
  }

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
      document.getElementById("capture-processing-records")?.scrollIntoView?.({ behavior: "smooth", block: "start" });
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
    setReplacement({ fileId: issue.file_id, batchId: issue.batch_id, name: issue.name, reason, code: issue.code });
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
  return {
    user, examId, canManage, onCollectionChanged,
    message, form, batches, selectedId,
    setSelectedId, loading, loadingMoreBatches, hasMoreBatches,
    error, loadBatches, loadMoreBatches, modalOpen,
    setModalOpen, creating, newFilesInput, newFiles,
    setNewFiles, newPdfPending, setNewPdfPending, appendPdfPending,
    setAppendPdfPending, appendPreview, setAppendPreview, pendingCreation,
    actioning, preview, setPreview, correctionRunId,
    setCorrectionRunId, activeTab, setActiveTab, pagesPerStudent,
    setPagesPerStudent, locatedSubmission, setLocatedSubmission, setLocatedBatch,
    locationError, setLocationAttempt, locating, setLocating,
    locationChoices, replacement, setReplacement, detail,
    detailError, detailLoading, matching, matchingError,
    matchingLoading, processingSummaries, loadDetail, loadMatching,
    uploading, pendingUpload, retryUpload, discardUpload,
    collection, supplement, canCreateBatch, reopenOpen,
    setReopenOpen, reopenReason, setReopenReason, qualityOverridePage,
    setQualityOverridePage, qualityOverrideReason, setQualityOverrideReason, previewRequestRef,
    canEditSelectedBatch, batchCanManage, matchingReadOnlyReason, submitBatch,
    uploadProps, confirmAppendPreview, startProcessing, reopenBatch,
    confirmReopen, startPageProcessing, confirmQualityOverride, showPage,
    matchStudent, markUnknown, matchPage, splitSubmission,
    mergeSubmissions, fileColumns, pageColumns, progressPercent,
    summary, submissions, issuePages, locateCollectionTarget,
    prepareReplacement, resolveRegistration,
  };
}
