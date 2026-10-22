import { usePaperImportContentFill } from "./usePaperImportContentFill";
import { createElement, useCallback, useEffect, useRef, useState } from "react";
import { App } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import {
  applyPaperImport,
  getPaperImport,
  savePaperImportReview,
  type PaperImportDraftQuestion,
  type PaperImportJob,
} from "../../api/papers";
import { markImportFieldConfirmed } from "./materials";
import { importAnswerShapeError } from "./answerShape";
import { isReviewQuestionConfirmed, mergeDirtyReviewDrafts, olderReviewTimestamp, remapReviewJsonFields, reviewDraftIndex, sameReviewValue } from "./reviewConfirmation";
import { PaperImportReviewComparison } from "./PaperImportReviewComparison";
import { confirmedImportDrafts, remainingReviewJson, reviewJobKey, reviewRubricHasScoreMismatch,
  reviewValidationNotice, type ReviewSubmission, type ReviewReceiptDecision } from "./paperImportReview.model";
import { usePaperImportMaterials } from "./usePaperImportMaterials";
import type { PaperImportWorkflowOptions } from "./paperImportWorkflow.types";

function formatError(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

export function usePaperImportWorkflow({
  canManage,
  selectedExam,
  papers,
  paperImports,
  loadConfig,
  onChanged
}: PaperImportWorkflowOptions) {
  const { message, modal } = App.useApp();
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
    // 写入回执可能先于旧轮询返回；已知时间更早的同代次结果不能撤销刚保存的确认事实。
    if (sameJob && olderReviewTimestamp(activeReviewJob?.updated_at, previous?.updated_at)) return;
    const current = reviewDraftsRef.current;
    // 同一任务回读只合并未改动题；考试、任务或资料版本改变后，才重建本地复核草稿。
    const drafts = sameJob ? mergeDirtyReviewDrafts(current, previous?.questions ?? [], activeReviewJob?.questions ?? []) : activeReviewJob?.questions ?? [];
    authoritativeReviewRef.current = activeReviewJob;
    loadedReviewJobKey.current = activeReviewJobKey;
    replaceReviewState(drafts, sameJob ? remapReviewJsonFields(reviewJsonTextsRef.current, current, drafts) : {}, sameJob ? remapReviewJsonFields(reviewJsonErrorsRef.current, current, drafts) : {});
  }, [activeReviewJob, activeReviewJobKey, replaceReviewState]);

  const materials = usePaperImportMaterials({ canManage, selectedExam, papers, paperImports, loadConfig,
    reviewDirty, setParsing });

  const { fillingImportContent, fillRecognizedContent, contentFillResult } = usePaperImportContentFill({
    canManage, selectedExam, loadConfig, onChanged, activeReviewJobKey, activeReviewJobKeyRef
  });
  const confirmPaperImport = async (job: PaperImportJob) => { await saveReview(job, undefined, true); };

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
    ...materials, parsing, reviewDrafts, reviewDirty, discardImportReviewDraft, savingImportReview,
    fillingImportContent, fillRecognizedContent, contentFillResult,
    confirmedReviewCount: reviewDrafts.filter(isReviewQuestionConfirmed).length,
    invalidReviewRubric: reviewDrafts.some(reviewRubricHasScoreMismatch),
    invalidReviewScore: reviewDrafts.some((draft) => !Number.isFinite(draft.score) || draft.score <= 0),
    invalidReviewAnswer, invalidReviewJson: Object.keys(reviewJsonErrors).length > 0,
    reviewJsonTexts, setReviewJsonText, clearReviewJsonTexts,
    confirmPaperImport, saveImportReview, updateReviewDraft
  };
}
