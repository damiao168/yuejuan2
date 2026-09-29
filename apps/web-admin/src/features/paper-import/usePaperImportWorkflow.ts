import { useEffect, useRef, useState } from "react";
import { App, type UploadProps } from "antd";
import { getPaperImportUserMessage, getUserErrorMessage } from "../../api/client";
import type { Exam } from "../../api/exams";
import { downloadFileBlob } from "../../api/files";
import {
  addPaperImportSources,
  applyPaperImport,
  cancelPaperImport,
  createPaperImport,
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

function confirmedImportDrafts(drafts: PaperImportDraftQuestion[], originalDrafts: PaperImportDraftQuestion[]) {
  return drafts.map((draft, index) => {
    const original = originalDrafts.find((item) => item.candidate_id && item.candidate_id === draft.candidate_id) ?? originalDrafts[index];
    // 填空题按答案评分；不能把解析建议误存成已确认的主观题评分细则。
    const answerKeyOnly = draft.question_type === "fill_blank";
    const fields = new Set(draft.human_confirmed_fields ?? []);
    if (answerKeyOnly) fields.delete("rubric");
    for (const field of ["question_no", "question_type", "score", "stem"]) fields.add(field);
    if (draft.options?.length) fields.add("options");
    if (draft.answer_key) fields.add("answer");
    if (draft.solution) fields.add("solution");
    if (draft.rubric && !answerKeyOnly) fields.add("rubric");
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
  loadConfig: (examId: string, options?: { silent?: boolean }) => Promise<void>;
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
  const activeReviewJobKey = activeReviewJob ? `${activeReviewJob.id}:${activeReviewJob.generation}` : null;
  const activeReviewJobKeyRef = useRef(activeReviewJobKey);
  activeReviewJobKeyRef.current = activeReviewJobKey;
  const [savingImportReview, setSavingImportReview] = useState(false);
  const [updatingImportSources, setUpdatingImportSources] = useState(false);
  const [stoppingImport, setStoppingImport] = useState(false);
  const [retryingParse, setRetryingParse] = useState(false);

  useEffect(() => {
    // 同一任务同一代次的轮询保留脏草稿，资料换代后才用新识别结果替换。
    if (reviewDirty && activeReviewJobKey === loadedReviewJobKey.current) return;
    loadedReviewJobKey.current = activeReviewJobKey;
    setReviewDrafts(activeReviewJob?.questions ?? []);
    setReviewDirty(false);
    setReviewJsonErrors({});
    setReviewJsonTexts({});
  }, [activeReviewJob?.questions, activeReviewJob?.updated_at, reviewDirty, activeReviewJobKey]);

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

  const confirmPaperImport = async (job: PaperImportJob) => {
    if (!selectedExam) return;
    const jobKey = `${job.id}:${job.generation}`;
    if (activeReviewJobKeyRef.current !== jobKey || loadedReviewJobKey.current !== jobKey) { message.warning("资料已更新，请核对当前识别结果后再确认"); return; }
    const invalidRubric = reviewDrafts.find(reviewRubricHasScoreMismatch);
    if (invalidRubric) { message.error(`第${invalidRubric.question_no}题评分细则分值不一致，不能确认`); return; }
    if (Object.keys(reviewJsonErrors).length) { message.error("高级评分数据包含无效 JSON，请先修正"); return; }
    const invalidAnswer = reviewDrafts.find((draft, index) => importAnswerShapeError(draft.answer_key?.standard_answer,
      job.questions.find((item) => item.candidate_id && item.candidate_id === draft.candidate_id)?.answer_key?.standard_answer ?? job.questions[index]?.answer_key?.standard_answer));
    if (invalidAnswer) { message.error(`第${invalidAnswer.question_no}题标准答案格式不正确，不能确认`); return; }
    setParsing(true);
    try {
      await savePaperImportReview(job.id, job.generation, confirmedImportDrafts(reviewDrafts, job.questions));
      if (activeReviewJobKeyRef.current !== jobKey) { message.warning("资料已更新，本轮核对结果未导入，请核对最新结果"); return; }
      await applyPaperImport(job.id);
      setReviewDirty(false);
      setReviewJsonErrors({});
      setReviewJsonTexts({});
      message.success("题目、标准答案、教师解析和评分点已写入当前考试");
      await loadConfig(selectedExam.id);
      onChanged?.();
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setParsing(false);
    }
  };

  const saveImportReview = async (job: PaperImportJob) => {
    if (!selectedExam) return;
    const jobKey = `${job.id}:${job.generation}`;
    if (activeReviewJobKeyRef.current !== jobKey || loadedReviewJobKey.current !== jobKey) { message.warning("资料已更新，请核对当前识别结果后再保存"); return; }
    const invalidRubric = reviewDrafts.find(reviewRubricHasScoreMismatch);
    if (invalidRubric) { message.error(`第${invalidRubric.question_no}题评分细则分值不一致，不能确认`); return; }
    if (Object.keys(reviewJsonErrors).length) { message.error("高级评分数据包含无效 JSON，请先修正"); return; }
    const invalidAnswer = reviewDrafts.find((draft, index) => importAnswerShapeError(draft.answer_key?.standard_answer,
      job.questions.find((item) => item.candidate_id && item.candidate_id === draft.candidate_id)?.answer_key?.standard_answer ?? job.questions[index]?.answer_key?.standard_answer));
    if (invalidAnswer) { message.error(`第${invalidAnswer.question_no}题标准答案格式不正确，不能保存`); return; }
    setSavingImportReview(true);
    try {
      await savePaperImportReview(job.id, job.generation, confirmedImportDrafts(reviewDrafts, job.questions));
      if (activeReviewJobKeyRef.current !== jobKey) return;
      setReviewDirty(false);
      setReviewJsonErrors({});
      setReviewJsonTexts({});
      message.success("人工核对结果已保存，后续追加资料不会覆盖已确认字段");
      await loadConfig(selectedExam.id);
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setSavingImportReview(false);
    }
  };

  const updateReviewDraft = (index: number, field: string, patch: Partial<PaperImportDraftQuestion>) => {
    setReviewDirty(true);
    setReviewDrafts((current) => current.map((draft, draftIndex) =>
      draftIndex === index ? markImportFieldConfirmed(draft, field, patch) : draft
    ));
  };

  const setReviewJsonText = (key: string, value: string, error?: string) => {
    setReviewJsonTexts((current) => ({ ...current, [key]: value }));
    setReviewJsonErrors((current) => {
      if (!error && !current[key]) return current;
      const next = { ...current };
      if (error) next[key] = error;
      else delete next[key];
      return next;
    });
    setReviewDirty(true);
  };

  const clearReviewJsonTexts = (keyPrefix: string) => {
    setReviewJsonTexts((current) => Object.fromEntries(Object.entries(current).filter(([key]) => !key.startsWith(keyPrefix))));
    setReviewJsonErrors((current) => Object.fromEntries(Object.entries(current).filter(([key]) => !key.startsWith(keyPrefix))));
  };

  const discardImportReviewDraft = () => {
    setReviewDrafts(paperImports[0]?.questions ?? []);
    setReviewDirty(false);
    setReviewJsonErrors({});
    setReviewJsonTexts({});
  };

  const invalidReviewAnswer = reviewDrafts.some((draft, index) => importAnswerShapeError(draft.answer_key?.standard_answer,
    paperImports[0]?.questions.find((item) => item.candidate_id && item.candidate_id === draft.candidate_id)?.answer_key?.standard_answer
      ?? paperImports[0]?.questions[index]?.answer_key?.standard_answer));

  return {
    uploadProps, parsing, reviewDrafts, reviewDirty, discardImportReviewDraft, savingImportReview, updatingImportSources,
    stoppingImport, retryingParse, invalidReviewRubric: reviewDrafts.some(reviewRubricHasScoreMismatch),
    invalidReviewScore: reviewDrafts.some((draft) => !Number.isFinite(draft.score) || draft.score <= 0),
    invalidReviewAnswer, invalidReviewJson: Object.keys(reviewJsonErrors).length > 0,
    reviewJsonTexts, setReviewJsonText, clearReviewJsonTexts,
    replaceImportSources, stopPaperImport, retryImportParse, removeImportSource,
    openImportSource, confirmPaperImport, saveImportReview, updateReviewDraft, importPastedText
  };
}
