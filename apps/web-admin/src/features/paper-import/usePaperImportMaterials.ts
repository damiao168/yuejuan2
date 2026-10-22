import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { App, type UploadProps } from "antd";
import { getPaperImportUserMessage, getUserErrorMessage } from "../../api/client";
import { downloadFileBlob } from "../../api/files";
import { addPaperImportSources, cancelPaperImport, createPaperImport, replacePaperImportSources, retryPaperImportParse,
  uploadFile, type PaperImportJob, type PaperImportRole } from "../../api/papers";
import { filesFromClipboard, isSupportedPaperImportFile, isTextPasteTarget, MAX_PASTED_MATERIAL_CHARS,
  normalizePastedMarkdown, orderedSourcesAfterRemoval, pastedMarkdownFile } from "./materials";
import type { PaperImportWorkflowOptions } from "./paperImportWorkflow.types";

function formatError(error: unknown) { return getUserErrorMessage(error, "操作失败，请稍后重试"); }

// 上传、重排和解析重试共用资料生命周期；人工核对未保存时不能启动新的识别代次。
export function usePaperImportMaterials({ canManage, selectedExam, papers, paperImports, loadConfig, reviewDirty, setParsing }:
  PaperImportWorkflowOptions & { reviewDirty: boolean; setParsing: Dispatch<SetStateAction<boolean>> }) {
  const { message, modal } = App.useApp();
  const materialUploadRef = useRef<(files: File[]) => void>(() => undefined);
  const [updatingImportSources, setUpdatingImportSources] = useState(false);
  const [stoppingImport, setStoppingImport] = useState(false);
  const [retryingParse, setRetryingParse] = useState(false);

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

  return { uploadProps, updatingImportSources, stoppingImport, retryingParse, replaceImportSources,
    stopPaperImport, retryImportParse, removeImportSource, openImportSource, importPastedText };
}
