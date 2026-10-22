import { useEffect, useRef, useState } from "react";
import { App } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import { downloadFileBlob, uploadFileWithProgress, type FileAsset } from "../../../api/files";
import { registerPaperFromFile, type PaperVersion } from "../../../api/papers";
import { mergeAnswerSheetSources } from "./mergeAnswerSheetSources";
import { readAnswerSheetSourceHashes } from "./answerSheetSourceManifest";

export type AnswerSheetUploadPhase = "idle" | "checking" | "ordering" | "merging" | "uploading" | "registering";

export function useAnswerSheetSourceUpload({ examId, canManage, identityKey = "", existingPapers = [], onCreated }: {
  examId: string; canManage: boolean; identityKey?: string; existingPapers?: readonly PaperVersion[]; onCreated: (paper: PaperVersion) => void;
}) {
  const { message } = App.useApp();
  const context = useRef({ examId, canManage, identityKey });
  context.current = { examId, canManage, identityKey };
  const onCreatedRef = useRef(onCreated);
  onCreatedRef.current = onCreated;
  const papersRef = useRef(existingPapers);
  papersRef.current = existingPapers;
  const sourceHashCache = useRef(new Map<string, string[]>());
  const generation = useRef(0);
  const mounted = useRef(true);
  const busy = useRef(false);
  const pendingAsset = useRef<FileAsset | undefined>(undefined);
  const abortController = useRef<AbortController | undefined>(undefined);
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);
  const [error, setError] = useState<string>();
  const [canRetry, setCanRetry] = useState(false);
  const [phase, setPhase] = useState<AnswerSheetUploadPhase>("idle");

  // 换考试、身份或失去权限均废弃旧批次；权限恢复也不能复活原身份的待登记资产。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 作用域字段用于取消旧批次，即使请求读取的是最新 ref，也不能删除这些触发依赖。
  useEffect(() => {
    mounted.current = true;
    generation.current++;
    abortController.current?.abort(); abortController.current = undefined;
    busy.current = false;
    pendingAsset.current = undefined;
    sourceHashCache.current = new Map();
    setUploading(false); setProgress(0); setError(undefined); setCanRetry(false); setPhase("idle");
    return () => { mounted.current = false; generation.current++; abortController.current?.abort(); abortController.current = undefined; };
  }, [examId, identityKey, canManage]);

  async function run(files?: readonly File[], options: { allowExistingSourceFiles?: boolean } = {}) {
    if (!mounted.current || !context.current.examId || !context.current.canManage || busy.current || (!files && !pendingAsset.current)) return;
    const requestExamId = context.current.examId;
    const requestIdentity = context.current.identityKey;
    const requestGeneration = generation.current;
    const current = () => mounted.current && generation.current === requestGeneration && context.current.examId === requestExamId && context.current.identityKey === requestIdentity && context.current.canManage;
    busy.current = true;
    const abort = new AbortController();
    abortController.current = abort;
    setUploading(true); setError(undefined); setProgress(0);
    let sameName = false;
    try {
      if (files) {
        pendingAsset.current = undefined;
        setCanRetry(false);
        // 仅比较本场答题卡用途；同一个文件用于试题资料或其他考试不算此入口重复。
        const papers = papersRef.current.filter(paper => paper.exam_id === requestExamId && paper.purpose === "answer_sheet");
        sameName = files.some(file => papers.some(paper => paper.file.original_name === file.name));
        const existingSourceHashes: string[] = [];
        if (!options.allowExistingSourceFiles) {
          const pdfs = papers.filter(paper => paper.file.content_type?.toLowerCase() === "application/pdf");
          const cache = sourceHashCache.current;
          setPhase("checking");
          // 只在用户上传时读取已有 PDF 的原图指纹；缓存不保存文件内容，换考试或身份即清空。
          for (let index = 0; index < pdfs.length; index++) {
            const paper = pdfs[index];
            const key = `${paper.file_asset_id}:${paper.file.hash_sha256}`;
            let hashes = cache.get(key);
            if (!hashes) {
              const downloaded = await downloadFileBlob(paper.file_asset_id);
              if (!current()) return;
              hashes = await readAnswerSheetSourceHashes(downloaded.blob);
              if (!current()) return;
              cache.set(key, hashes);
            }
            existingSourceHashes.push(...hashes);
            setProgress(Math.round((index + 1) / pdfs.length * 100));
          }
        }
        setPhase("merging");
        setProgress(0);
        // 合并也占用 busy，避免双击产生两份来源；合并期间切换身份后不再发出网络上传。
        const file = await mergeAnswerSheetSources(files, {
          isCurrent: current,
          existingHashes: papers.map(paper => paper.file.hash_sha256),
          existingSourceHashes,
          // 兼容显式来源复用调用；普通上传默认阻止原图重复，整套打包后仍须校验指纹。
          allowExistingSourceFiles: options.allowExistingSourceFiles,
          resolvePageOrder: files.length > 1 ? async sourceFiles => {
            if (!current()) throw new DOMException("答题卡上传已取消", "AbortError");
            setPhase("ordering"); setProgress(0);
            const { orderAnswerSheetSourcePages } = await import("./orderAnswerSheetSourcePages");
            const order = await orderAnswerSheetSourcePages(sourceFiles, { examId: requestExamId, signal: abort.signal,
              isCurrent: current, onProgress: (processed, total) => { if (current()) setProgress(Math.round(processed / total * 100)); } });
            if (current()) { setPhase("merging"); setProgress(0); }
            return order;
          } : undefined,
          onProgress: (processed, total) => { if (current()) setProgress(Math.round(processed / total * 100)); }
        });
        if (!current()) return;
        setPhase("uploading"); setProgress(0);
        const result = await uploadFileWithProgress(file, { owner_type: "exam", owner_id: requestExamId, exam_id: requestExamId }, percent => {
          if (current()) setProgress(percent);
        });
        // 切换考试、账号或权限后不能继续提交旧文件，也不能把旧结果写入当前页面。
        if (!current()) return;
        pendingAsset.current = result.file;
      }
      const asset = pendingAsset.current;
      if (!asset || !current()) return;
      setPhase("registering");
      // 文件上传和答题卡登记分两步；登记失败保留资产 ID，重试只补登记，避免重复上传。
      const response = await registerPaperFromFile(requestExamId, asset.id, "answer_sheet");
      if (!current()) return;
      pendingAsset.current = undefined;
      setCanRetry(false);
      onCreatedRef.current(response.paper);
      if (response.duplicate) message.info(`“${response.paper.file.original_name}”已上传过，已切换到已有答题卡，未新增来源。`);
      else message.success(`整套答题卡已上传，可以识别并自动对应题目，发现错误可直接修改${sameName ? "；同名文件内容不同，已作为新答题卡保留" : ""}`);
    } catch (uploadError) {
      if (current()) {
        const notice = getUserErrorMessage(uploadError, "答题卡上传失败，请重试。");
        setError(notice);
        if (/已经上传过|内容重复/.test(notice)) message.warning(notice);
        setCanRetry(Boolean(pendingAsset.current));
      }
    } finally {
      if (generation.current === requestGeneration) {
        busy.current = false;
        if (abortController.current === abort) abortController.current = undefined;
        setUploading(false);
        setPhase("idle");
      }
    }
  }

  return { uploading, progress, phase, error, canRetry, uploadBatch: (files: readonly File[], options?: { allowExistingSourceFiles?: boolean }) => run([...files], options), upload: (file: File) => run([file]), retry: () => run() };
}
