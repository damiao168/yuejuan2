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
  type PaperVersion
} from "../../api/papers";
import {
  filesFromClipboard,
  isSupportedPaperImportFile,
  isTextPasteTarget,
  markImportFieldConfirmed,
  orderedSourcesAfterRemoval
} from "./materials";

function formatError(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function pointTotal(points: NonNullable<PaperImportDraftQuestion["rubric"]>["points"]) {
  return points.reduce((sum, point) => sum + (Number(point.score) || 0), 0);
}

function reviewRubricHasScoreMismatch(draft: PaperImportDraftQuestion) {
  return Boolean(draft.rubric
    && (Math.abs(draft.rubric.max_score - draft.score) > 0.0001
      || Math.abs(pointTotal(draft.rubric.points) - draft.score) > 0.0001));
}

function confirmedImportDrafts(drafts: PaperImportDraftQuestion[]) {
  return drafts.map((draft) => {
    const fields = new Set(draft.human_confirmed_fields ?? []);
    for (const field of ["question_no", "question_type", "score", "stem"]) fields.add(field);
    if (draft.answer_key) fields.add("answer");
    if (draft.solution) fields.add("solution");
    if (draft.rubric) fields.add("rubric");
    return { ...draft, human_confirmed_fields: [...fields] };
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
  const [savingImportReview, setSavingImportReview] = useState(false);
  const [updatingImportSources, setUpdatingImportSources] = useState(false);
  const [stoppingImport, setStoppingImport] = useState(false);
  const [retryingParse, setRetryingParse] = useState(false);

  useEffect(() => { setReviewDrafts(paperImports[0]?.questions ?? []); }, [paperImports]);

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

  const handleMaterialFiles = async (files: File[]) => {
    if (!selectedExam || !files.length) { message.error("请先选择考试，再添加考试资料"); return; }
    if (paperImports.some((item) => item.status === "processing")) { message.warning("当前资料仍在识别，请完成后再继续添加"); return; }
    const supportedFiles = files.filter(isSupportedPaperImportFile);
    if (supportedFiles.length !== files.length) message.warning("已忽略不支持的文件，仅接受 PDF、Word、PNG、JPG、JPEG、TIFF");
    if (!supportedFiles.length) return;
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
      const startIndex = activeImport?.sources.length ?? 0;
      const sources = uploaded.map((file, index) => ({
        file_asset_id: file.id,
        document_index: startIndex + index,
        role_hint: "auto" as const
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
      } else {
        message.success(`已添加 ${supportedFiles.length} 份资料，系统正在识别和匹配`);
      }
      await loadConfig(selectedExam.id, { silent: true });
    } catch (error) {
      await loadConfig(selectedExam.id, { silent: true }).catch(() => undefined);
      message.error(formatError(error));
    } finally {
      setParsing(false);
    }
  };
  materialUploadRef.current = (files) => { void handleMaterialFiles(files); };

  const uploadProps: UploadProps = {
    showUploadList: false,
    multiple: true,
    accept: ".pdf,.docx,.png,.jpg,.jpeg,.tif,.tiff,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document,image/png,image/jpeg,image/tiff",
    beforeUpload: (file, fileList) => {
      if (file.uid === fileList[0]?.uid) void handleMaterialFiles(fileList as File[]);
      return false;
    }
  };

  const replaceImportSources = async (job: PaperImportJob, sources: PaperImportJob["sources"]) => {
    if (!["processing", "review_required", "failed", "cancelled"].includes(job.status)) return;
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
    if (job.status !== "failed" || job.error_code !== "ai_parse_failed" || retryingParse) return;
    setRetryingParse(true);
    try {
      await retryPaperImportParse(job.id, job.generation);
      message.success("已复用文字和公式识别结果，正在重新解析题目结构");
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
    const invalidRubric = reviewDrafts.find(reviewRubricHasScoreMismatch);
    if (invalidRubric) { message.error(`第${invalidRubric.question_no}题评分细则分值不一致，不能确认`); return; }
    setParsing(true);
    try {
      await savePaperImportReview(job.id, job.generation, confirmedImportDrafts(reviewDrafts));
      await applyPaperImport(job.id);
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
    const invalidRubric = reviewDrafts.find(reviewRubricHasScoreMismatch);
    if (invalidRubric) { message.error(`第${invalidRubric.question_no}题评分细则分值不一致，不能确认`); return; }
    setSavingImportReview(true);
    try {
      await savePaperImportReview(job.id, job.generation, confirmedImportDrafts(reviewDrafts));
      message.success("人工核对结果已保存，后续追加资料不会覆盖已确认字段");
      await loadConfig(selectedExam.id);
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setSavingImportReview(false);
    }
  };

  const updateReviewDraft = (index: number, field: string, patch: Partial<PaperImportDraftQuestion>) => {
    setReviewDrafts((current) => current.map((draft, draftIndex) =>
      draftIndex === index ? markImportFieldConfirmed(draft, field, patch) : draft
    ));
  };

  return {
    uploadProps, parsing, reviewDrafts, savingImportReview, updatingImportSources,
    stoppingImport, retryingParse, invalidReviewRubric: reviewDrafts.some(reviewRubricHasScoreMismatch),
    replaceImportSources, stopPaperImport, retryImportParse, removeImportSource,
    openImportSource, confirmPaperImport, saveImportReview, updateReviewDraft
  };
}
