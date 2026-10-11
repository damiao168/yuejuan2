import { createElement, useCallback, useEffect, useRef, useState } from "react";
import { App, type UploadProps } from "antd";
import { ApiClientError, getPaperImportUserMessage, getUserErrorMessage } from "../../api/client";
import type { Exam } from "../../api/exams";
import { downloadFileBlob } from "../../api/files";
import {
  addPaperImportSources,
  applyPaperImport,
  cancelPaperImport,
  createPaperImport,
  fillPaperImportContent,
  getPaperImport,
  replacePaperImportSources,
  retryPaperImportParse,
  savePaperImportReview,
  uploadFile,
  type PaperImportDraftQuestion,
  type PaperImportJob,
  type PaperImportRole,
  type PaperVersion
} from "../../api/papers";
import {
  filesFromClipboard,
  isSupportedPaperImportFile,
  isTextPasteTarget,
  MAX_PASTED_MATERIAL_CHARS,
  markImportFieldConfirmed,
  normalizePastedMarkdown,
  orderedSourcesAfterRemoval,
  pastedMarkdownFile
} from "./materials";
import { importAnswerShapeError, resolveImportAnswerValue } from "./answerShape";
import { isReviewQuestionConfirmed, mergeDirtyReviewDrafts, olderReviewTimestamp, remapReviewJsonFields, reviewConfirmationFields, reviewDraftIndex, sameReviewValue } from "./reviewConfirmation";
import { PaperImportReviewComparison } from "./PaperImportReviewComparison";

function formatError(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function pointTotal(points: NonNullable<PaperImportDraftQuestion["rubric"]>["points"]) {
  return points.reduce((sum, point) => sum + (Number(point.score) || 0), 0);
}

function reviewRubricHasScoreMismatch(draft: PaperImportDraftQuestion) {
  if (draft.question_type === "fill_blank") return false;
  return Boolean(draft.rubric
    && (Math.abs(draft.rubric.max_score - draft.score) > 0.0001
      || Math.abs(pointTotal(draft.rubric.points) - draft.score) > 0.0001));
}

function reviewJobKey(job?: PaperImportJob) {
  return job ? `${job.exam_id}:${job.id}:${job.generation}:${job.source_revision ?? ""}` : null;
}

function reviewJsonKeyMatches(key: string, index: number, draft: PaperImportDraftQuestion) {
  return key.startsWith(`${index}:`) || Boolean(draft.candidate_id && key.startsWith(`${draft.candidate_id}:`));
}

function confirmedImportDrafts(drafts: PaperImportDraftQuestion[], originalDrafts: PaperImportDraftQuestion[], confirmedIndex?: number) {
  return drafts.map((draft, index) => {
    const original = originalDrafts.find((item) => item.candidate_id && item.candidate_id === draft.candidate_id) ?? originalDrafts[index];
    // 填空题按答案评分；不能把解析建议误存成已确认的主观题评分细则。
    const answerKeyOnly = draft.question_type === "fill_blank";
    const fields = new Set(draft.human_confirmed_fields ?? []);
    if (answerKeyOnly) fields.delete("rubric");
    if (index === confirmedIndex) for (const field of reviewConfirmationFields(draft)) fields.add(field);
    return {
      ...draft,
      answer_key: draft.answer_key ? {
        ...draft.answer_key,
        standard_answer: resolveImportAnswerValue(draft.answer_key.standard_answer, original?.answer_key?.standard_answer)
      } : undefined,
      rubric_candidate_id: answerKeyOnly ? undefined : draft.rubric_candidate_id,
      rubric: answerKeyOnly ? undefined : draft.rubric,
      human_confirmed_fields: [...fields]
    };
  });
}

type ReviewValidationNotice = Readonly<{ level: "error" | "warning"; text: string }>;
type ReviewSubmission = Readonly<{
  local: PaperImportDraftQuestion[];
  selected?: PaperImportDraftQuestion;
  texts: Record<string, string>;
  errors: Record<string, string>;
  payload: PaperImportDraftQuestion[];
  receiptBaseline: PaperImportDraftQuestion[];
  baseline: PaperImportDraftQuestion[];
  expectedUpdatedAt: string;
}>;
type ReviewReceiptDecision = { kind: "accepted"; job: PaperImportJob } | { kind: "refresh" } | { kind: "ignored" };

function reviewValidationNotice(
  local: PaperImportDraftQuestion[], authority: PaperImportDraftQuestion[], selected: PaperImportDraftQuestion | undefined,
  confirmedIndex: number | undefined, errors: Record<string, string>, apply: boolean
): ReviewValidationNotice | undefined {
  const checking = selected ? [selected] : local;
  // 本题确认只检查本题；整卷保存检查全卷，遇到首个错误就停止。
  if (selected && (!Number.isFinite(selected.score) || selected.score <= 0)) return { level: "error", text: `第${selected.question_no}题分值必须大于 0，不能确认` };
  const invalidRubric = checking.find(reviewRubricHasScoreMismatch);
  if (invalidRubric) return { level: "error", text: `第${invalidRubric.question_no}题评分细则分值不一致，不能确认` };
  if (Object.keys(errors).some(key => !selected || reviewJsonKeyMatches(key, confirmedIndex!, selected))) return { level: "error", text: "高级评分数据包含无效 JSON，请先修正" };
  const invalidAnswer = checking.find(draft => importAnswerShapeError(draft.answer_key?.standard_answer,
    authority[reviewDraftIndex(authority, draft, local.indexOf(draft))]?.answer_key?.standard_answer));
  if (invalidAnswer) return { level: "error", text: `第${invalidAnswer.question_no}题标准答案格式不正确，不能保存` };
  if (!apply) return undefined;
  const unconfirmed = local.filter(draft => !isReviewQuestionConfirmed(draft));
  if (unconfirmed.length) return { level: "warning", text: `还有 ${unconfirmed.length} 道题未确认，请逐题核对并点击“确认并保存本题”后导入。` };
  return undefined;
}

function remainingReviewJson(values: Record<string, string>, submitted: Record<string, string>, selected: PaperImportDraftQuestion | undefined, currentIndex: number) {
  return Object.fromEntries(Object.entries(values)
    .filter(([key, value]) => selected && !reviewJsonKeyMatches(key, currentIndex, selected) || value !== submitted[key]));
}

export function usePaperImportWorkflow({
  canManage,
  selectedExam,
  papers,
  paperImports,
  loadConfig,
  onChanged
}: {
  canManage: boolean;
  selectedExam?: Exam;
  papers: PaperVersion[];
  paperImports: PaperImportJob[];
  loadConfig: (examId: string, options?: { silent?: boolean; propagateError?: boolean }) => Promise<void>;
  onChanged?: () => void;
}) {
  const { message, modal } = App.useApp();
  const materialUploadRef = useRef<(files: File[]) => void>(() => undefined);
  const [parsing, setParsing] = useState(false);
  const [reviewDrafts, setReviewDrafts] = useState<PaperImportDraftQuestion[]>([]);
  const [reviewDirty, setReviewDirty] = useState(false);
  const [reviewJsonErrors, setReviewJsonErrors] = useState<Record<string, string>>({});
  const [reviewJsonTexts, setReviewJsonTexts] = useState<Record<string, string>>({});
  const loadedReviewJobKey = useRef<string | null>(null);
  const activeReviewJob = paperImports[0];
  const activeReviewJobKey = reviewJobKey(activeReviewJob);
  const activeReviewJobKeyRef = useRef(activeReviewJobKey);
  activeReviewJobKeyRef.current = activeReviewJobKey;
  const activeReviewJobRef = useRef(activeReviewJob);
  activeReviewJobRef.current = activeReviewJob;
  const selectedExamIdRef = useRef(selectedExam?.id);
  selectedExamIdRef.current = selectedExam?.id;
  // state 负责显示；ref 同步记录草稿和权威回执，异步返回不能用旧闭包清除后来输入。
  const authoritativeReviewRef = useRef<PaperImportJob | undefined>(undefined);
  // 首次编辑冻结读取基线；后台轮询可更新未改动题，不能替用户同意覆盖新的核对内容。
  const reviewEditBaselineRef = useRef<PaperImportJob | undefined>(undefined);
  const reviewDraftsRef = useRef(reviewDrafts); reviewDraftsRef.current = reviewDrafts;
  const reviewJsonTextsRef = useRef(reviewJsonTexts); reviewJsonTextsRef.current = reviewJsonTexts;
  const reviewJsonErrorsRef = useRef(reviewJsonErrors); reviewJsonErrorsRef.current = reviewJsonErrors;
  const reviewSaveInFlight = useRef(false);
  const reviewConflictRef = useRef<{ key: string | null; baseline: PaperImportDraftQuestion[] } | undefined>(undefined);
  const comparisonPromptOpen = useRef(false);
  const [savingImportReview, setSavingImportReview] = useState(false);
  const [fillingImportContent, setFillingImportContent] = useState(false);
  const fillingContentRef = useRef(false);
  const [contentFillResult, setContentFillResult] = useState<Awaited<ReturnType<typeof fillPaperImportContent>> | null>(null);
  const [updatingImportSources, setUpdatingImportSources] = useState(false);
  const [stoppingImport, setStoppingImport] = useState(false);
  const [retryingParse, setRetryingParse] = useState(false);

  const replaceReviewState = useCallback((drafts: PaperImportDraftQuestion[], texts: Record<string, string>, errors: Record<string, string>) => {
    reviewDraftsRef.current = drafts; reviewJsonTextsRef.current = texts; reviewJsonErrorsRef.current = errors;
    setReviewDrafts(drafts); setReviewJsonTexts(texts); setReviewJsonErrors(errors);
    const dirty = !sameReviewValue(drafts, authoritativeReviewRef.current?.questions ?? []) || Object.keys(texts).length > 0 || Object.keys(errors).length > 0;
    if (!dirty) reviewEditBaselineRef.current = undefined;
    setReviewDirty(dirty);
  }, []);

  const ownsReviewRequest = (key: string | null, examId: string) => activeReviewJobKeyRef.current === key
    && selectedExamIdRef.current === examId && activeReviewJobRef.current?.exam_id === examId;

  useEffect(() => {
    const previous = authoritativeReviewRef.current;
    const sameJob = activeReviewJobKey === loadedReviewJobKey.current;
    if (!sameJob) { reviewConflictRef.current = undefined; reviewEditBaselineRef.current = undefined; }
    if (!sameJob) setContentFillResult(null);
    // 写入回执可能先于旧轮询返回；已知时间更早的同代次结果不能撤销刚保存的确认事实。
    if (sameJob && olderReviewTimestamp(activeReviewJob?.updated_at, previous?.updated_at)) return;
    const current = reviewDraftsRef.current;
    // 同一任务回读只合并未改动题；考试、任务或资料版本改变后，才重建本地复核草稿。
    const drafts = sameJob ? mergeDirtyReviewDrafts(current, previous?.questions ?? [], activeReviewJob?.questions ?? []) : activeReviewJob?.questions ?? [];
    authoritativeReviewRef.current = activeReviewJob;
    loadedReviewJobKey.current = activeReviewJobKey;
    replaceReviewState(drafts, sameJob ? remapReviewJsonFields(reviewJsonTextsRef.current, current, drafts) : {}, sameJob ? remapReviewJsonFields(reviewJsonErrorsRef.current, current, drafts) : {});
  }, [activeReviewJob, activeReviewJobKey, replaceReviewState]);

  useEffect(() => {
    const onPaste = (event: ClipboardEvent) => {
      if (isTextPasteTarget(event.target)) return;
      const files = filesFromClipboard(event.clipboardData);
      if (!files.length || !selectedExam || !canManage) return;
      event.preventDefault();
      materialUploadRef.current(files);
    };
    window.addEventListener("paste", onPaste);
    return () => window.removeEventListener("paste", onPaste);
  }, [canManage, selectedExam]);

  const handleMaterialFiles = async (files: File[], roleHint: PaperImportRole = "auto") => {
    if (!selectedExam || !files.length) { message.error("请先选择考试，再添加考试资料"); return false; }
    if (reviewDirty) { message.warning("请先保存人工核对，再添加资料，避免重新识别覆盖未保存修改"); return false; }
    if (paperImports.some((item) => item.status === "processing")) { message.warning("当前资料仍在识别，请完成后再继续添加"); return false; }
    const supportedFiles = files.filter(isSupportedPaperImportFile);
    if (supportedFiles.length !== files.length) message.warning("已忽略不支持的文件，仅接受 PDF、Word、图片、Markdown 或 TXT");
    if (!supportedFiles.length) return false;
    setParsing(true);
    try {
      const uploaded = [];
      for (const file of supportedFiles) {
        uploaded.push((await uploadFile(file, {
          owner_type: "import",
          owner_id: selectedExam.id,
          exam_id: selectedExam.id,
          school_id: selectedExam.school_id
        })).file);
      }
      const activeImport = paperImports.find((item) => ["review_required", "failed", "cancelled"].includes(item.status));
      // 上传端可能复用同一文件资产；新增资料需同时排除历史来源与本批重复资产。
      const existingAssetIDs = new Set(activeImport?.sources.map((source) => source.file_asset_id).filter(Boolean) ?? []);
      const acceptedAssetIDs = new Set<string>();
      const newAssets = uploaded.filter((file) => {
        if (existingAssetIDs.has(file.id) || acceptedAssetIDs.has(file.id)) return false;
        acceptedAssetIDs.add(file.id);
        return true;
      });
      const duplicateCount = uploaded.length - newAssets.length;
      if (activeImport && newAssets.length === 0) {
        message.info("这份资料已在当前识别任务中，无需重复添加");
        return true;
      }
      const startIndex = activeImport?.sources.length ?? 0;
      const sources = newAssets.map((file, index) => ({
        file_asset_id: file.id,
        document_index: startIndex + index,
        role_hint: roleHint
      }));
      const commandId = crypto.randomUUID();
      const result = activeImport
        ? await addPaperImportSources(activeImport.id, activeImport.generation, sources, commandId)
        : await createPaperImport(selectedExam.id, {
            exam_paper_id: papers[0]?.id,
            subject: selectedExam.subject,
            sources
          }, commandId);
      if (result.import.status === "failed") {
        message.error(getPaperImportUserMessage(result.import.issues[0], "考试资料识别失败，请检查资料后重试"));
      } else if (duplicateCount > 0) {
        message.success(`已忽略 ${duplicateCount} 份重复资料，新增 ${newAssets.length} 份并开始识别`);
      } else {
        message.success(`已添加 ${newAssets.length} 份资料，系统正在识别和匹配`);
      }
      await loadConfig(selectedExam.id, { silent: true });
      return true;
    } catch (error) {
      await loadConfig(selectedExam.id, { silent: true }).catch(() => undefined);
      message.error(formatError(error));
      return false;
    } finally {
      setParsing(false);
    }
  };
  materialUploadRef.current = (files) => { void handleMaterialFiles(files); };

  const importPastedText = async (value: string, roleHint: PaperImportRole) => {
    const content = normalizePastedMarkdown(value);
    if (content.length < 20) {
      message.error("请至少粘贴 20 个字符的考试资料");
      return false;
    }
    if (content.length > MAX_PASTED_MATERIAL_CHARS) {
      message.error(`粘贴内容不能超过 ${MAX_PASTED_MATERIAL_CHARS.toLocaleString("zh-CN")} 个字符`);
      return false;
    }
    return handleMaterialFiles([pastedMarkdownFile(content)], roleHint);
  };

  const uploadProps: UploadProps = {
    showUploadList: false,
    multiple: true,
    accept: ".pdf,.docx,.png,.jpg,.jpeg,.tif,.tiff,.txt,.md,.markdown,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document,image/png,image/jpeg,image/tiff,text/plain,text/markdown",
    beforeUpload: (file, fileList) => {
      if (file.uid === fileList[0]?.uid) void handleMaterialFiles(fileList as File[]);
      return false;
    }
  };

  const replaceImportSources = async (job: PaperImportJob, sources: PaperImportJob["sources"]) => {
    if (!["processing", "review_required", "failed", "cancelled"].includes(job.status)) return;
    if (reviewDirty) { message.warning("请先保存人工核对，再调整资料，避免重新识别覆盖未保存修改"); return; }
    setUpdatingImportSources(true);
    try {
      await replacePaperImportSources(job.id, job.generation, sources.map((source, documentIndex) => ({
        id: source.id,
        document_index: documentIndex,
        role_hint: source.role_hint
      })), crypto.randomUUID());
      message.success(sources.length ? "资料顺序或类型已更新，系统正在重新匹配" : "已删除全部考试资料，请重新上传正确的资料");
      if (selectedExam) await loadConfig(selectedExam.id, { silent: true });
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setUpdatingImportSources(false);
    }
  };

  const stopPaperImport = (job: PaperImportJob) => {
    if (job.status !== "processing" || stoppingImport) return;
    modal.confirm({
      title: "停止当前识别？",
      content: "系统会停止尚未完成的文字识别和 AI 解析；已上传资料会保留，之后可以重新识别。",
      okText: "停止识别",
      cancelText: "继续识别",
      okButtonProps: { danger: true },
      onOk: async () => {
        setStoppingImport(true);
        try {
          await cancelPaperImport(job.id, job.generation, crypto.randomUUID());
          message.success("已停止识别，上传资料仍然保留");
          if (selectedExam) await loadConfig(selectedExam.id, { silent: true });
        } catch (error) {
          message.error(formatError(error));
        } finally {
          setStoppingImport(false);
        }
      }
    });
  };

  const retryImportParse = async (job: PaperImportJob) => {
    const retryable = (job.status === "failed" && job.error_code === "ai_parse_failed") || job.status === "cancelled";
    if (!retryable || retryingParse) return;
    setRetryingParse(true);
    try {
      await retryPaperImportParse(job.id, job.generation);
      message.success("已复用本次新增资料，正在重新解析题目结构");
      if (selectedExam) await loadConfig(selectedExam.id, { silent: true });
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setRetryingParse(false);
    }
  };

  const removeImportSource = async (job: PaperImportJob, sourceID: string) => {
    await replaceImportSources(job, orderedSourcesAfterRemoval(job.sources, sourceID));
  };

  const openImportSource = async (fileAssetID: string, pageNo?: number) => {
    try {
      const file = await downloadFileBlob(fileAssetID);
      const url = URL.createObjectURL(file.blob);
      window.open(pageNo && pageNo > 0 ? `${url}#page=${pageNo}` : url, "_blank", "noopener,noreferrer");
      window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
    } catch (error) {
      message.error(formatError(error));
    }
  };

  const confirmPaperImport = async (job: PaperImportJob) => { await saveReview(job, undefined, true); };

  const fillRecognizedContent = async (job: PaperImportJob, quiet = false) => {
    if (!canManage || !selectedExam || fillingContentRef.current) return;
    const jobKey = reviewJobKey(job);
    if (activeReviewJobKeyRef.current !== jobKey) return;
    fillingContentRef.current = true;
    setFillingImportContent(true);
    try {
      const result = await fillPaperImportContent(job.id, job.generation);
      // 旧资料请求可完成服务端写入，但不能把结果或提示放到新资料的编辑页面。
      if (activeReviewJobKeyRef.current !== jobKey) return;
      setContentFillResult(result);
      if (result.filled_questions.length) {
        message.success(`已自动填入 ${result.filled_questions.length} 道题的识别内容，请核对答案与评分配置`);
        await loadConfig(selectedExam.id, { silent: true });
        onChanged?.();
      } else if (!quiet) message.info("现有内容已保留；未能自动匹配的题目请查看提示");
    } catch (error) {
      if (activeReviewJobKeyRef.current === jobKey) message.error(formatError(error));
    } finally {
      fillingContentRef.current = false;
      setFillingImportContent(false);
    }
  };

  const prepareReviewSubmission = (jobKey: string | null, examId: string, confirmedIndex?: number, apply = false): ReviewSubmission | undefined => {
    if (!ownsReviewRequest(jobKey, examId) || loadedReviewJobKey.current !== jobKey || activeReviewJobRef.current?.status !== "review_required") {
      message.warning(`资料已更新，请核对当前识别结果后再${apply ? "确认" : "保存"}`); return;
    }
    const local = structuredClone(reviewDraftsRef.current);
    const authoritativeJob = authoritativeReviewRef.current;
    if (!authoritativeJob?.updated_at) { message.warning("识别结果缺少保存版本，请重新读取后再保存；本页输入仍保留。"); return; }
    const authority = authoritativeJob.questions;
    const selected = confirmedIndex === undefined ? undefined : local[confirmedIndex];
    if (confirmedIndex !== undefined && (!Number.isInteger(confirmedIndex) || !selected)) return;
    const authorityIndex = selected ? reviewDraftIndex(authority, selected, confirmedIndex!) : -1;
    if (selected && authorityIndex < 0) { message.warning("本题对应的识别结果已更新，请重新核对后再保存"); return; }
    const texts = { ...reviewJsonTextsRef.current }; const errors = { ...reviewJsonErrorsRef.current };
    const notice = reviewValidationNotice(local, authority, selected, confirmedIndex, errors, apply);
    if (notice) {
      message[notice.level](notice.text);
      return;
    }
    // 服务端会整卷替换。本题保存只替换权威快照中的当前候选，不能夹带其他题的本地输入。
    const payload = selected ? structuredClone(authority) : confirmedImportDrafts(local, authority);
    const receiptBaseline = selected ? structuredClone(authority) : local;
    if (selected) {
      payload[authorityIndex] = confirmedImportDrafts([selected], [authority[authorityIndex]], 0)[0];
      receiptBaseline[authorityIndex] = selected;
    }
    return { local, selected, texts, errors, payload, receiptBaseline,
      baseline: structuredClone(authority), expectedUpdatedAt: authoritativeJob.updated_at };
  };

  const compareReviewConflict = (job: PaperImportJob, jobKey: string | null, examId: string) => {
    if (comparisonPromptOpen.current) return;
    comparisonPromptOpen.current = true;
    modal.confirm({
      title: "识别结果已更新，本次修改未保存",
      content: "其他人或另一个页面已保存新的核对内容。你的输入仍在本页，请读取最新结果并比较后再保存。",
      okText: "读取最新结果并比较", cancelText: "继续保留本页输入",
      onCancel: () => { comparisonPromptOpen.current = false; },
      onOk: async () => {
        try {
          const { import: latest } = await getPaperImport(job.id);
          const conflict = reviewConflictRef.current;
          if (!ownsReviewRequest(jobKey, examId) || conflict?.key !== jobKey) return;
          if (reviewJobKey(latest) !== jobKey || latest.status !== "review_required") {
            message.warning("资料代次或状态已变化，请先记录本页修改，再读取当前任务。"); return;
          }
          const local = structuredClone(reviewDraftsRef.current);
          // 无法对应的候选不能在合并时丢弃；只展示比较，不擅自清除本地输入。
          const missingLocal = local.some((draft, index) => reviewDraftIndex(latest.questions, draft, index) < 0);
          modal.confirm({
            title: "比较本页输入与最新核对结果", width: 880,
            content: createElement(PaperImportReviewComparison, { local, latest: latest.questions }),
            okText: "保留我的修改并继续核对", cancelText: "暂不合并",
            okButtonProps: { disabled: missingLocal },
            onOk: () => {
              if (!ownsReviewRequest(jobKey, examId) || reviewConflictRef.current !== conflict) return;
              if (missingLocal) return;
              if (olderReviewTimestamp(latest.updated_at, authoritativeReviewRef.current?.updated_at)) {
                message.warning("结果再次更新，请取消本次比较，再点击保存重新读取；本页修改仍保留。");
                throw new Error("review comparison is outdated");
              }
              const current = reviewDraftsRef.current;
              const merged = mergeDirtyReviewDrafts(current, conflict.baseline, latest.questions);
              authoritativeReviewRef.current = latest;
              reviewEditBaselineRef.current = latest;
              replaceReviewState(merged, remapReviewJsonFields(reviewJsonTextsRef.current, current, merged),
                remapReviewJsonFields(reviewJsonErrorsRef.current, current, merged));
              reviewConflictRef.current = undefined;
              message.info("最新结果已合并，本页修改仍保留。请核对差异后，再点击原保存按钮。");
            }
          });
        } catch (error) {
          message.error(getUserErrorMessage(error, "最新结果读取失败，本页输入仍保留，请重试读取。"));
          throw error;
        } finally { comparisonPromptOpen.current = false; }
      }
    });
  };

  const acceptReviewReceipt = (result: Awaited<ReturnType<typeof savePaperImportReview>>, payload: PaperImportDraftQuestion[], jobKey: string | null, examId: string, apply: boolean): ReviewReceiptDecision => {
    // 保存后的守卫同步检查；成功回执在合并前不能多等一轮异步任务。
    if (!ownsReviewRequest(jobKey, examId)) {
      if (apply) message.warning("资料已更新，本轮核对结果未导入，请核对最新结果");
      return { kind: "ignored" };
    }
    if (activeReviewJobRef.current?.status !== "review_required") return { kind: "ignored" };
    const savedJob = result.import as PaperImportJob;
    if (!Array.isArray(savedJob?.questions)) throw new Error("保存返回的识别结果不完整，请刷新后核对；本地草稿已保留");
    if (reviewJobKey(savedJob) !== jobKey || savedJob.exam_id !== examId || savedJob.status !== "review_required") return { kind: "refresh" };
    if (payload.some((draft, index) => reviewDraftIndex(savedJob.questions, draft, index) < 0)) throw new Error("保存返回的候选题目不完整，请刷新后核对；本地草稿已保留");
    return { kind: "accepted", job: savedJob };
  };

  const mergeSavedReview = (savedJob: PaperImportJob, submission: ReviewSubmission, jobKey: string | null, confirmedIndex?: number) => {
    const { local, selected, texts, errors, receiptBaseline } = submission;
    // 较新的轮询可能先于本次回执到达；旧回执仍证明写入完成，但不能回退已读的权威快照。
    const known = authoritativeReviewRef.current;
    const newest = reviewJobKey(known) === jobKey && olderReviewTimestamp(savedJob.updated_at, known?.updated_at) ? known! : savedJob;
    const current = reviewDraftsRef.current;
    const merged = mergeDirtyReviewDrafts(current, receiptBaseline, newest.questions);
    const currentIndex = selected ? reviewDraftIndex(current, selected, confirmedIndex!) : -1;
    const submittedTexts = remapReviewJsonFields(texts, local, current);
    const submittedErrors = remapReviewJsonFields(errors, local, current);
    // 请求期间可能重排候选；先对应当前身份和提交时文本，不能按旧索引清掉另一题的相同输入。
    authoritativeReviewRef.current = newest;
    // 只把本人的保存回执作为剩余草稿基线；更晚的他人轮询仍需显式比较。
    reviewEditBaselineRef.current = savedJob;
    replaceReviewState(merged,
      remapReviewJsonFields(remainingReviewJson(reviewJsonTextsRef.current, submittedTexts, selected, currentIndex), current, merged),
      remapReviewJsonFields(remainingReviewJson(reviewJsonErrorsRef.current, submittedErrors, selected, currentIndex), current, merged));
  };

  const notifyReviewSaved = (apply: boolean, selected?: PaperImportDraftQuestion) => {
    if (apply) {
      message.success("题目、标准答案、教师解析和评分点已写入当前考试");
      onChanged?.();
      return;
    }
    if (selected) {
      message.success(`第 ${selected.question_no} 题已确认并保存；其他题目的确认状态保持不变。`);
      return;
    }
    message.success("草稿已保存；未查看的题目仍待逐题确认。");
  };

  const compareReviewBeforeSave = (job: PaperImportJob, jobKey: string | null, examId: string) => {
    const editBaseline = reviewEditBaselineRef.current;
    if (editBaseline && reviewJobKey(editBaseline) === jobKey && editBaseline.updated_at !== authoritativeReviewRef.current?.updated_at) {
      reviewConflictRef.current ??= { key: jobKey, baseline: structuredClone(editBaseline.questions) };
    }
    // 冲突后再次点击只打开比较，不带最新时间静默重放旧整卷或继续导入。
    if (reviewConflictRef.current?.key !== jobKey) return false;
    compareReviewConflict(job, jobKey, examId);
    return true;
  };

  const handleReviewSaveFailure = (error: unknown, job: PaperImportJob, jobKey: string | null, examId: string, written: boolean, apply: boolean, submission: ReviewSubmission) => {
    // 写入前的版本冲突转入显式比较；保存已成功后的刷新失败不能撤销回执或重放写入。
    if (!written && error instanceof ApiClientError && error.status === 409 && ownsReviewRequest(jobKey, examId)) {
      reviewConflictRef.current = { key: jobKey, baseline: submission.baseline };
      compareReviewConflict(job, jobKey, examId);
      return;
    }
    if (!ownsReviewRequest(jobKey, examId)) return;
    if (written && !apply) message.warning(`${submission.selected ? "本题已确认并保存" : "本次草稿已保存"}，但最新结果刷新未完成；其余本地修改已保留，请重试刷新。`);
    else message.error(formatError(error));
  };

  const saveReview = async (job: PaperImportJob, confirmedIndex?: number, apply = false) => {
    if (!canManage || !selectedExam || reviewSaveInFlight.current) return;
    const jobKey = reviewJobKey(job);
    if (compareReviewBeforeSave(job, jobKey, selectedExam.id)) return;
    const submission = prepareReviewSubmission(jobKey, selectedExam.id, confirmedIndex, apply);
    if (!submission) return;
    // 同步锁先于请求与 React 更新，连续点击、整卷导入和迟到的放弃操作不能并发清写草稿。
    reviewSaveInFlight.current = true;
    setSavingImportReview(true);
    if (apply) setParsing(true);
    let written = false;
    try {
      const result = await savePaperImportReview(job.id, job.generation, submission.payload, submission.expectedUpdatedAt);
      const receipt = acceptReviewReceipt(result, submission.payload, jobKey, selectedExam.id, apply);
      if (receipt.kind === "ignored") return;
      if (receipt.kind === "refresh") {
        await loadConfig(selectedExam.id, { silent: true, propagateError: true });
        if (ownsReviewRequest(jobKey, selectedExam.id)) message.warning("资料已更新，请核对最新识别结果；本轮未继续导入");
        return;
      }
      mergeSavedReview(receipt.job, submission, jobKey, confirmedIndex);
      written = true;
      if (apply) {
        if (activeReviewJobRef.current?.status !== "review_required") return;
        await applyPaperImport(job.id);
      }
      if (!ownsReviewRequest(jobKey, selectedExam.id)) return;
      await loadConfig(selectedExam.id, { silent: true, propagateError: true });
      if (!ownsReviewRequest(jobKey, selectedExam.id)) return;
      notifyReviewSaved(apply, submission.selected);
    } catch (error) {
      handleReviewSaveFailure(error, job, jobKey, selectedExam.id, written, apply, submission);
    } finally {
      reviewSaveInFlight.current = false;
      setSavingImportReview(false);
      if (apply) setParsing(false);
    }
  };

  const saveImportReview = async (job: PaperImportJob, confirmedIndex?: number) => { await saveReview(job, confirmedIndex); };

  const updateReviewDraft = (index: number, field: string, patch: Partial<PaperImportDraftQuestion>) => {
    reviewEditBaselineRef.current ??= authoritativeReviewRef.current && structuredClone(authoritativeReviewRef.current);
    setReviewDirty(true);
    const next = reviewDraftsRef.current.map((draft, draftIndex) =>
      draftIndex === index ? markImportFieldConfirmed(draft, field, patch) : draft
    );
    reviewDraftsRef.current = next; setReviewDrafts(next);
  };

  const setReviewJsonText = (key: string, value: string, error?: string) => {
    reviewEditBaselineRef.current ??= authoritativeReviewRef.current && structuredClone(authoritativeReviewRef.current);
    const texts = { ...reviewJsonTextsRef.current, [key]: value };
    const errors = { ...reviewJsonErrorsRef.current };
    if (error) errors[key] = error;
    else delete errors[key];
    reviewJsonTextsRef.current = texts; reviewJsonErrorsRef.current = errors;
    setReviewJsonTexts(texts); setReviewJsonErrors(errors);
    setReviewDirty(true);
  };

  const clearReviewJsonTexts = (keyPrefix: string) => {
    const texts = Object.fromEntries(Object.entries(reviewJsonTextsRef.current).filter(([key]) => !key.startsWith(keyPrefix)));
    const errors = Object.fromEntries(Object.entries(reviewJsonErrorsRef.current).filter(([key]) => !key.startsWith(keyPrefix)));
    replaceReviewState(reviewDraftsRef.current, texts, errors);
  };

  const discardImportReviewDraft = () => {
    // 较早打开的放弃对话框不能在写入期间清空草稿；成功回执比尚未刷新的页面快照更新。
    if (reviewSaveInFlight.current) return;
    replaceReviewState(authoritativeReviewRef.current?.questions ?? [], {}, {});
  };

  const invalidReviewAnswer = reviewDrafts.some((draft, index) => importAnswerShapeError(draft.answer_key?.standard_answer,
    paperImports[0]?.questions.find((item) => item.candidate_id && item.candidate_id === draft.candidate_id)?.answer_key?.standard_answer
      ?? paperImports[0]?.questions[index]?.answer_key?.standard_answer));

  return {
    uploadProps, parsing, reviewDrafts, reviewDirty, discardImportReviewDraft, savingImportReview, updatingImportSources,
    fillingImportContent, fillRecognizedContent, contentFillResult,
    stoppingImport, retryingParse, confirmedReviewCount: reviewDrafts.filter(isReviewQuestionConfirmed).length,
    invalidReviewRubric: reviewDrafts.some(reviewRubricHasScoreMismatch),
    invalidReviewScore: reviewDrafts.some((draft) => !Number.isFinite(draft.score) || draft.score <= 0),
    invalidReviewAnswer, invalidReviewJson: Object.keys(reviewJsonErrors).length > 0,
    reviewJsonTexts, setReviewJsonText, clearReviewJsonTexts,
    replaceImportSources, stopPaperImport, retryImportParse, removeImportSource,
    openImportSource, confirmPaperImport, saveImportReview, updateReviewDraft, importPastedText
  };
}
