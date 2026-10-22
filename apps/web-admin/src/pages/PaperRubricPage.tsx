import { useEffect, useMemo, useRef, useState } from "react";
import { App, Form } from "antd";
import { validatePaperConfig, type Question, type RubricPayload } from "../api/papers";
import { usePaperConfigData } from "../features/paper-import/usePaperConfigData";
import { useScoringRules } from "../features/paper-import/useScoringRules";
import { usePaperImportWorkflow } from "../features/paper-import/usePaperImportWorkflow";
import { useQuestionEditor, type PreparedQuestionSave } from "../features/paper-rubric/hooks/useQuestionEditor";
import { useGroupedQuestionScores } from "../features/paper-rubric/hooks/useGroupedQuestionScores";
import { useUnsavedChangesGuard } from "../router/useUnsavedChangesGuard";
import { useRubricEditor } from "../features/paper-rubric/hooks/useRubricEditor";
import { formatPaperError } from "../features/paper-rubric/paperPresentation";
import { filterPaperQuestions, fixedScoringQuestionTypes, isUnmatchedPlaceholder, questionEditingAccess, type QuestionFilter, type QuestionIssueTarget } from "../features/paper-rubric/paperQuestionPresentation";
import { PaperRubricView } from "../features/paper-rubric/PaperRubricView";
import type { PaperRubricPageProps, PaperRubricViewContext } from "../features/paper-rubric/paperRubricViewTypes";

type QuestionSaveCheckpoint = { owner: string; payload: string; questionId: string };
type ScoringSaveCheckpoint = { owner: string; payload: string };
type AcceptedSaveReceipt = { owner: string; examId: string; questionId: string; kind: "whole" | "rubric"; version?: string; snapshot?: RubricPayload; rubricStatus?: string };
type PreparedScoringRuleSave = ReturnType<ReturnType<typeof useScoringRules>["prepareScoringRuleSave"]>;

function matchingSaveReceipt<T extends Readonly<{ owner: string; payload: string }>>(checkpoint: T | null, owner: string, payload: string): T | null {
  // 只有同一题目且快照完全相同的回执才能跳过写入，不能把上次部分成功当作新草稿已保存。
  if (checkpoint?.owner !== owner) return null;
  if (checkpoint.payload !== payload) return null;
  return checkpoint;
}

function questionIdForWholeSave(checkpoint: QuestionSaveCheckpoint | null, owner: string, payload: string, preparedQuestionId: string | undefined, formDirty: boolean) {
  const receipt = matchingSaveReceipt(checkpoint, owner, payload);
  if (receipt) return receipt.questionId;
  if (!formDirty) return preparedQuestionId;
  return undefined;
}

function questionSaveInput(prepared: PreparedQuestionSave, checkpoint: QuestionSaveCheckpoint | null, owner: string): PreparedQuestionSave {
  // 新题的评分失败后可能又修改题目内容；仍更新已经创建的编号，不能再次创建同一题。
  if (checkpoint?.owner === owner) return { ...prepared, questionId: checkpoint.questionId };
  return prepared;
}

function scoringReceiptKey(questionId: string, rubric: RubricPayload | null, rule: PreparedScoringRuleSave | null) {
  let savedRule: Pick<PreparedScoringRuleSave, "type" | "config"> | null = null;
  // 回执比较实际评分内容，不把写入后改变的草稿修订号加入本次快照。
  if (rule) savedRule = { type: rule.type, config: rule.config };
  return JSON.stringify({ questionId, rubric, rule: savedRule });
}

function wholeSaveFailureMessage(error: unknown, contentSaved: boolean, scoringSaved: boolean) {
  if (scoringSaved) return `题目与答案、评分配置已保存；列表刷新失败，已保存的配置仍显示在本页。请点击“重新读取保存结果”确认，无需再次保存。${formatPaperError(error)}`;
  if (contentSaved) return `题目与答案已保存；评分配置尚未全部保存，输入已保留，请核对后再次保存本题全部修改。${formatPaperError(error)}`;
  const detail = error && typeof error === "object" && "errorFields" in error ? "请修正表单标注的字段。" : formatPaperError(error);
  return `本题尚未保存，请核对题目与评分配置后重试。${detail}`;
}

function rubricVersionLabel(version?: string) {
  if (version) return `评分细则第 ${version} 版`;
  return "评分细则";
}

function savedRubricOutcome(receipt: AcceptedSaveReceipt) {
  const label = rubricVersionLabel(receipt.version);
  if (receipt.snapshot?.status === "locked") return `${label}已确认并锁定`;
  if (receipt.snapshot?.status === "pending_review") return `${label}已提交审批，待确认并锁定`;
  if (receipt.snapshot?.status === "approved") return `${label}已批准，待确认并锁定`;
  return `${label}草稿已保存，待确认并锁定`;
}

function acceptedReadFailureMessage(error: unknown, receipt: AcceptedSaveReceipt) {
  if (receipt.kind === "whole") return wholeSaveFailureMessage(error, true, true);
  return `${savedRubricOutcome(receipt)}；页面暂时无法读取保存结果，已保存内容和其他区域草稿仍保留。请点击“重新读取保存结果”，无需再次保存评分细则。${formatPaperError(error)}`;
}

function acceptedReadSuccessMessage(receipt: AcceptedSaveReceipt, pending: string) {
  if (receipt.kind === "rubric") return `${savedRubricOutcome(receipt)}，保存结果已读取；${pending}`;
  return "已重新读取保存结果；之后继续编辑的修改仍保留，请核对后保存。";
}

export function PaperRubricPage({
  canManage,
  canManageAssessment,
  canReadQuestionBank = false,
  canImportQuestionBank = false,
  questionBankScope = "",
  initialExamId = "",
  fixedExamId,
  mode = "standalone",
  initialView = "materials",
  onExamChanged,
  onNavigate
}: Readonly<PaperRubricPageProps>) {
  const { message, modal } = App.useApp();
  const [formDirty, setFormDirty] = useState(false);
  const [saveBusy, setSaveBusy] = useState(false);
  const [groupedRefreshPending, setGroupedRefreshPending] = useState(false);
  const saveInFlight = useRef(false);
  const [allSaveMessage, setAllSaveMessage] = useState<string | null>(null);
  const [allSaveCompleted, setAllSaveCompleted] = useState(false);
  const completedSaveOwner = useRef<string | null>(null);
  const savedQuestionCheckpoint = useRef<QuestionSaveCheckpoint | null>(null);
  const savedScoringCheckpoint = useRef<ScoringSaveCheckpoint | null>(null);
  const [acceptedSaveReceipt, setAcceptedSaveReceipt] = useState<AcceptedSaveReceipt | null>(null);
  const acceptedSaveReceiptRef = useRef<AcceptedSaveReceipt | null>(null);
  acceptedSaveReceiptRef.current = acceptedSaveReceipt;
  const [activeCandidateIndex, setActiveCandidateIndex] = useState<number | null>(null);
  const [detailTab, setDetailTab] = useState("content");
  const [questionFilter, setQuestionFilter] = useState<QuestionFilter>("all");
  const [questionSearch, setQuestionSearch] = useState("");
  const [sourceOpen, setSourceOpen] = useState(false);
  const [indexOpen, setIndexOpen] = useState(false);
  const [issueFocus, setIssueFocus] = useState<{ questionId: string; target: QuestionIssueTarget } | null>(null);
  const focusedIssue = useRef<typeof issueFocus>(null);
  const config = usePaperConfigData({ initialExamId, fixedExamId });
  const {
    exams, selectedExamId, papers, paperImports, questions,
    selectedQuestionId, setSelectedQuestionId, editorMode, setEditorMode,
    configLoading, setValidation, loadConfig
  } = config;
  const groupedPendingOwner = useRef<{ examId: string; version: number } | null>(null);
  const mutationScope = useRef({ examId: selectedExamId, version: 0 });
  if (mutationScope.current.examId !== selectedExamId) mutationScope.current = { examId: selectedExamId, version: mutationScope.current.version + 1 };
  const mutationVersion = mutationScope.current.version;
  const isCurrentMutationScope = () => mutationScope.current.examId === selectedExamId && mutationScope.current.version === mutationVersion;
  const hasGroupedReadPending = () => groupedPendingOwner.current?.examId === mutationScope.current.examId
    && groupedPendingOwner.current?.version === mutationScope.current.version;
  const groupedReadScope = useRef(selectedExamId);
  useEffect(() => {
    // 分值回读失败只阻止所属考试；换科后由新考试自己的配置读取决定可编辑状态。
    if (groupedReadScope.current !== selectedExamId) { groupedReadScope.current = selectedExamId; setGroupedRefreshPending(false); }
  }, [selectedExamId]);
  const selectedExam = useMemo(() => exams.find((exam) => exam.id === selectedExamId), [exams, selectedExamId]);
  const importWorkflow = usePaperImportWorkflow({
    canManage, selectedExam, papers, paperImports, loadConfig, onChanged: onExamChanged
  });
  const selectedQuestion = useMemo(
    () => (editorMode === "edit" ? questions.find((question) => question.id === selectedQuestionId) ?? null : null),
    [editorMode, questions, selectedQuestionId]
  );
  const activeSaveOwner = useRef("");
  activeSaveOwner.current = `${selectedExamId}:${selectedQuestion?.id ?? "create"}`;
  useEffect(() => () => { activeSaveOwner.current = ""; }, []);
  const latestPaperImport = paperImports[0];
  const structureConfirmed = latestPaperImport && latestPaperImport.structure_confirmed_generation === latestPaperImport.generation;
  const pendingDrafts = latestPaperImport?.status !== "applied" && !structureConfirmed ? importWorkflow.reviewDrafts : [];
  const visibleQuestions = useMemo(
    () => filterPaperQuestions(questions, questionFilter, questionSearch),
    [questionFilter, questionSearch, questions]
  );
  const selectedQuestionIndex = questions.findIndex((question) => question.id === selectedQuestionId);
  useEffect(() => {
    setActiveCandidateIndex(latestPaperImport?.id && latestPaperImport.status === "review_required" && !structureConfirmed && latestPaperImport.questions.length ? 0 : null);
  }, [latestPaperImport?.id, latestPaperImport?.status, latestPaperImport?.questions.length, structureConfirmed]);
  useEffect(() => {
    setFormDirty(false); savedQuestionCheckpoint.current = null; savedScoringCheckpoint.current = null;
    setAcceptedSaveReceipt(null);
    // 新建题保存后会切入其正式编号，此次切换仍展示同一写入回执；其他换题须清除旧成功提示。
    if (completedSaveOwner.current !== `${selectedExamId}:${selectedQuestion?.id ?? "create"}`) { setAllSaveMessage(null); setAllSaveCompleted(false); completedSaveOwner.current = null; }
  }, [selectedExamId, selectedQuestion?.id]);
  const questionWorkflow = useQuestionEditor({
    selectedExam, selectedQuestion, papers, questionCount: questions.length,
    editorMode, setEditorMode, setSelectedQuestionId, preserveUnsaved: formDirty || saveBusy || Boolean(acceptedSaveReceipt), loadConfig,
    onChanged: onExamChanged, isMutationBlocked: () => !isCurrentMutationScope() || saveInFlight.current || hasGroupedReadPending() || Boolean(acceptedSaveReceiptRef.current), runMutation: saveSection
  });
  const { form, saveQuestion } = questionWorkflow;
  const watchedQuestionType = Form.useWatch("question_type", form);
  const rubricWorkflow = useRubricEditor({
    selectedQuestion, selectedExamId, preserveUnsaved: saveBusy || Boolean(savedQuestionCheckpoint.current) || Boolean(acceptedSaveReceipt), loadConfig, onChanged: onExamChanged
  });
  const { rubricDirty, discardRubricDraft } = rubricWorkflow;
  const editingAccess = questionEditingAccess(canManage, watchedQuestionType, selectedQuestion, selectedExam);
  // 锁定写入成功后，即使回读失败，也不能继续编辑服务器已锁定的题目。
  const acceptedRubricLock = acceptedSaveReceipt?.owner === activeSaveOwner.current
    && acceptedSaveReceipt.kind === "rubric" && acceptedSaveReceipt.snapshot?.status === "locked";
  const access = { ...editingAccess, selectedLocked: editingAccess.selectedLocked || acceptedRubricLock,
    questionDisabled: editingAccess.questionDisabled || acceptedRubricLock || saveBusy || groupedRefreshPending };
  const rubricSaveAction = useRef<{
    owner: string; source: string; disabled: boolean; contentDirty: boolean;
    prepare: () => RubricPayload; save: (status: "draft" | "locked") => Promise<void>;
  } | null>(null);
  rubricSaveAction.current = {
    owner: activeSaveOwner.current, source: JSON.stringify(selectedQuestion),
    disabled: access.questionDisabled || configLoading || Boolean(acceptedSaveReceipt) || !selectedQuestion,
    contentDirty: formDirty, prepare: rubricWorkflow.prepareRubricSave, save: saveCurrentRubric
  };
  const objectiveRuleType = access.objectiveRuleType;
  const scoringRuleWorkflow = useScoringRules({ selectedQuestion, objectiveRuleType, preserveUnsaved: saveBusy || Boolean(savedQuestionCheckpoint.current) || Boolean(acceptedSaveReceipt), onChanged: onExamChanged });
  const { scoringRuleDirty, discardScoringRuleDraft, saveScoringRule } = scoringRuleWorkflow;
  const unsavedWork = formDirty || rubricDirty || scoringRuleDirty;
  const groupedScores = useGroupedQuestionScores({
    selectedExam, questions: questions.filter((question) => !isUnmatchedPlaceholder(question, latestPaperImport, structureConfirmed)),
    canManage: canManage && (!latestPaperImport || Boolean(structureConfirmed) || latestPaperImport.status === "applied"),
    hasUnsavedWork: unsavedWork || importWorkflow.reviewDirty || importWorkflow.savingImportReview || importWorkflow.parsing || importWorkflow.fillingImportContent,
    isMutationBlocked: () => !isCurrentMutationScope() || saveInFlight.current || hasGroupedReadPending() || Boolean(acceptedSaveReceiptRef.current) || configLoading
      || importWorkflow.savingImportReview || importWorkflow.parsing || importWorkflow.fillingImportContent,
    runMutation: saveSection, runRefresh: (action) => saveSection(action, true),
    onRefreshPending: (pending) => {
      if (!isCurrentMutationScope()) return;
      // 旧删除确认与快速保存回调仍可能持有上一帧 state；先同步阻止写入，再更新提示。
      groupedPendingOwner.current = pending ? { examId: selectedExamId, version: mutationVersion } : null;
      setGroupedRefreshPending(pending);
      setAllSaveCompleted(false);
      setAllSaveMessage(pending ? "统一分值已提交，保存结果尚未读取；请先重新读取，避免旧表单覆盖已更新分值。" : null);
    }, loadConfig, onChanged: onExamChanged
  });
  useEffect(() => {
    if (allSaveCompleted && unsavedWork) { setAllSaveCompleted(false); setAllSaveMessage(null); }
  }, [allSaveCompleted, unsavedWork]);
  useUnsavedChangesGuard({ dirty: unsavedWork || groupedScores.dirty || importWorkflow.reviewDirty, busy: saveBusy || importWorkflow.savingImportReview || importWorkflow.parsing,
    description: "题目、评分配置或识别核对还有未保存修改。请保存后离开，或明确放弃。" });
  const autoFilledImports = useRef(new Set<string>());
  useEffect(() => {
    // 每轮识别只自动尝试一次；已有编辑草稿时暂缓，避免刷新覆盖尚未保存的表单。
    if (!canManage || !structureConfirmed || !selectedExam || !questions.length || configLoading || saveBusy || groupedRefreshPending || acceptedSaveReceipt || unsavedWork || groupedScores.dirty || importWorkflow.reviewDirty || latestPaperImport?.status !== "review_required" || importWorkflow.fillingImportContent) return;
    const key = `${latestPaperImport.id}:${latestPaperImport.generation}`;
    if (autoFilledImports.current.has(key)) return;
    autoFilledImports.current.add(key);
    void importWorkflow.fillRecognizedContent(latestPaperImport, true);
  }, [canManage, structureConfirmed, selectedExam, questions, configLoading, saveBusy, groupedRefreshPending, acceptedSaveReceipt, unsavedWork, groupedScores.dirty, latestPaperImport, importWorkflow]);
  useEffect(() => {
    if (importWorkflow.contentFillResult?.filled_questions.length) setActiveCandidateIndex(null);
  }, [importWorkflow.contentFillResult]);

  function navigateAfterDirtyCheck(action: () => void) {
    if (saveInFlight.current) { message.info("正在保存当前题目，请等待保存结果后再切换。"); return; }
    if (!unsavedWork && !groupedScores.dirty) { action(); return; }
    modal.confirm({
      title: "当前题目有未保存修改",
      content: "切换会放弃题目、评分配置和批量分值中尚未保存的修改。",
      okText: "放弃修改并切换",
      okButtonProps: { danger: true },
      cancelText: "继续编辑",
      onOk: () => {
        if (saveInFlight.current) { return; }
        setFormDirty(false);
        discardRubricDraft();
        discardScoringRuleDraft();
        groupedScores.discardChanges();
        action();
      }
    });
  }

  function openQuestion(question: Question) {
    navigateAfterDirtyCheck(() => {
      setActiveCandidateIndex(null);
      setEditorMode("edit");
      setSelectedQuestionId(question.id);
      setDetailTab("content");
      setSourceOpen(false);
      setIndexOpen(false);
    });
  }

  function openQuestionIssue(question: Question, target: QuestionIssueTarget) {
    const open = () => {
      setActiveCandidateIndex(null); setEditorMode("edit"); setSelectedQuestionId(question.id);
      setDetailTab(target === "rubric" ? "scoring" : "content"); setSourceOpen(false);
      setQuestionFilter("all"); setQuestionSearch(""); setIndexOpen(false);
      setIssueFocus({ questionId: question.id, target });
    };
    // 同题内切到缺项字段不丢弃其他区域草稿；换题须等教师完成原有的未保存确认。
    if (selectedQuestion?.id === question.id && activeCandidateIndex === null && !saveInFlight.current) open();
    else navigateAfterDirtyCheck(open);
  }

  useEffect(() => {
    if (!issueFocus || focusedIssue.current === issueFocus || configLoading || selectedQuestion?.id !== issueFocus.questionId
      || detailTab !== (issueFocus.target === "rubric" ? "scoring" : "content")) return;
    let target: HTMLElement | null = null;
    let clearHighlight: ReturnType<typeof setTimeout> | undefined;
    // 先完成题目与标签切换，再定位实际字段，不能把高亮加到上一题或隐藏的评分区。
    const timer = setTimeout(() => {
      const selector = { score: ".paper-question-score-target", answer: ".paper-question-answer-target", rubric: "[data-paper-scoring-target]" }[issueFocus.target];
      target = document.getElementById("paper-question-workbench")?.querySelector<HTMLElement>(selector) ?? null;
      if (!target) return;
      focusedIssue.current = issueFocus;
      target.scrollIntoView?.({ block: "center", behavior: "smooth" });
      const input = target.querySelector<HTMLElement>('input:not([disabled]), textarea:not([disabled]), button:not([disabled])');
      (input ?? target).focus({ preventScroll: true });
      target.setAttribute("data-paper-issue-focus", "true");
      clearHighlight = setTimeout(() => target?.removeAttribute("data-paper-issue-focus"), 2400);
    }, 0);
    return () => { clearTimeout(timer); clearTimeout(clearHighlight); target?.removeAttribute("data-paper-issue-focus"); };
  }, [issueFocus, selectedQuestion?.id, detailTab, configLoading]);

  // 各编辑器按题目编号保留其他区域的草稿，允许分别保存；同一时刻只提交一处，避免刷新乱序和保存期间换题。
  async function saveSection(action: () => Promise<unknown>, allowGroupedRefresh = false) {
    if (!isCurrentMutationScope()) return;
    if (saveInFlight.current) return;
    if (acceptedSaveReceiptRef.current) { message.info("上次修改已保存，请先重新读取保存结果，再保存后续修改。"); return; }
    if (hasGroupedReadPending() && !allowGroupedRefresh) { message.info("统一分值的保存结果尚未读取，请先重新读取分值结果。"); return; }
    saveInFlight.current = true;
    setSaveBusy(true);
    try {
      await action();
    } catch (error) {
      // 表单校验失败由字段提示；请求错误由各编辑器提示，草稿继续保留。
      if (!(error && typeof error === "object" && "errorFields" in error)) message.error(formatPaperError(error));
    } finally {
      saveInFlight.current = false;
      setSaveBusy(false);
    }
  }

  function discardActiveDraft() {
    if (saveInFlight.current) return;
    const content = detailTab === "content";
    modal.confirm({
      title: content ? "放弃题目与答案的修改？" : "放弃评分配置的修改？",
      content: "只恢复当前区域最后保存的内容，其他区域的草稿会保留。",
      okText: "放弃本区修改", cancelText: "继续编辑", okButtonProps: { danger: true },
      onOk: () => {
        if (saveInFlight.current) { return; }
        if (content) {
          setFormDirty(false);
        } else {
          discardRubricDraft();
          discardScoringRuleDraft();
        }
      }
    });
  }

  async function runValidation() {
    if (!selectedExam) {
      return;
    }
    try {
      const result = await validatePaperConfig(selectedExam.id);
      setValidation(result.result);
      if (result.result.valid) {
        message.success("试卷配置检查通过");
      } else {
        message.warning("试卷配置存在问题，请查看下方详情");
      }
    } catch (currentError) {
      message.error(formatPaperError(currentError));
    }
  }

  function createQuestion() {
    navigateAfterDirtyCheck(() => {
      setActiveCandidateIndex(null);
      setEditorMode("create");
      setSelectedQuestionId(null);
      setDetailTab("content");
      setSourceOpen(false);
      setIndexOpen(false);
    });
  }

  function openCandidate(index: number) {
    navigateAfterDirtyCheck(() => {
      setActiveCandidateIndex(index);
      setDetailTab("content");
      setSourceOpen(false);
      setIndexOpen(false);
    });
  }

  function saveCurrentQuestion() {
    void saveSection(async () => { if (await saveQuestion()) setFormDirty(false); });
  }

  function prepareScoringChanges(prepared: PreparedQuestionSave) {
    const fixedType = fixedScoringQuestionTypes.has(prepared.payload.question_type) ? prepared.payload.question_type : "";
    if (fixedType && rubricDirty || !fixedType && scoringRuleDirty) throw new Error("切换题型后仍有原评分方式的未保存修改，请恢复原题型保存，或明确放弃评分配置后重试。");
    const needRubric = !fixedType && (rubricDirty || prepared.payload.score !== selectedQuestion?.score);
    const needRule = Boolean(fixedType && (scoringRuleDirty || prepared.payload.score !== selectedQuestion?.score || prepared.payload.question_type !== selectedQuestion?.question_type));
    const rubric = needRubric ? rubricWorkflow.prepareRubricSave(prepared.payload.score) : null;
    const rule = needRule ? scoringRuleWorkflow.prepareScoringRuleSave(prepared.payload.score, fixedType) : null;
    return { rubric, rule };
  }

  function recordQuestionReceipt(saved: { questionId: string } | null, owner: string, payload: string) {
    if (!saved) throw new Error("题目正在保存，请等待结果");
    const questionId = saved.questionId;
    savedQuestionCheckpoint.current = { owner, payload, questionId };
    return questionId;
  }

  function finishAcceptedSave(receipt: AcceptedSaveReceipt) {
    completedSaveOwner.current = `${receipt.examId}:${receipt.questionId}`;
    if (receipt.kind === "rubric") {
      // 分区保存只确认评分细则，不能把题目与答案的未保存草稿也标成已保存。
      setAcceptedSaveReceipt(null);
      const confirmed = receipt.snapshot?.status === "locked";
      setAllSaveCompleted(confirmed && !formDirty && !scoringRuleDirty);
      setAllSaveMessage(`${savedRubricOutcome(receipt)}，保存结果已读取。${formDirty || scoringRuleDirty ? "其他区域尚有未保存修改，请继续核对。" : confirmed ? "可继续下一题。" : "核对后点击“确认并锁定细则”，再用于正式阅卷。"}`);
      onExamChanged?.();
      return;
    }
    if (receipt.owner.endsWith(":create")) { setEditorMode("edit"); setSelectedQuestionId(receipt.questionId); }
    setFormDirty(false); setAllSaveCompleted(true); savedQuestionCheckpoint.current = null;
    setAcceptedSaveReceipt(null);
    const pendingRubric = receipt.rubricStatus && receipt.rubricStatus !== "locked";
    setAllSaveMessage(pendingRubric
      ? "本题题目与答案、已修改的评分配置全部保存；评分细则仍待确认，核对后点击“确认并锁定细则”。"
      : "本题题目与答案、已修改的评分配置全部保存。可继续下一题；固定规则发布仍需单独确认。");
    onExamChanged?.();
  }

  async function rereadSavedResult() {
    if (groupedRefreshPending) { await groupedScores.rereadSavedResults(); return; }
    const receipt = acceptedSaveReceipt;
    if (receipt?.owner !== activeSaveOwner.current || saveInFlight.current) return;
    saveInFlight.current = true; setSaveBusy(true);
    try {
      // 写入已有回执；恢复操作只读取，不重放任何题目或评分写请求。
      await loadConfig(receipt.examId, { silent: true, propagateError: true });
      if (receipt.owner !== activeSaveOwner.current) return;
      if (formDirty || rubricDirty || scoringRuleDirty) {
        setAcceptedSaveReceipt(null);
        const pending = formDirty || scoringRuleDirty ? "其他区域尚有未保存修改，输入已保留，请核对后保存。" : "之后继续编辑的修改仍保留，请核对后保存。";
        setAllSaveMessage(acceptedReadSuccessMessage(receipt, pending));
      } else finishAcceptedSave(receipt);
    } catch (error) {
      if (receipt.owner === activeSaveOwner.current) setAllSaveMessage(acceptedReadFailureMessage(error, receipt));
    } finally { saveInFlight.current = false; setSaveBusy(false); }
  }

  async function confirmWholeSaveReceipt(receipt: AcceptedSaveReceipt) {
    // 回执先阻止新的写入，再读取保存结果；失败仍由整题保存报告，锁由外层 finally 释放。
    setAcceptedSaveReceipt(receipt); setFormDirty(false);
    await loadConfig(receipt.examId, { silent: true, propagateError: true });
    // 读取期间题目范围可能已被外部更新；旧题的回执不能完成当前题的保存提示。
    if (receipt.owner !== activeSaveOwner.current) return;
    finishAcceptedSave(receipt);
  }

  async function saveAllQuestionChanges() {
    if (!isCurrentMutationScope() || saveInFlight.current || hasGroupedReadPending() || access.questionDisabled || !selectedExam) return;
    if (acceptedSaveReceiptRef.current) { message.info("上次修改已保存，请先重新读取保存结果，再保存后续修改。"); return; }
    saveInFlight.current = true; setSaveBusy(true); setAllSaveCompleted(false); setAllSaveMessage(null);
    const owner = `${selectedExam.id}:${selectedQuestion?.id ?? "create"}`;
    const stillCurrent = () => activeSaveOwner.current === owner;
    let contentSaved = false;
    let scoringSaved = false;
    try {
      // 全部区域先校验并冻结快照，再发出第一条写请求；评分必须用本次表单满分而非旧题目闭包。
      const prepared = await questionWorkflow.prepareQuestionSave();
      if (!stillCurrent()) return;
      const { rubric, rule } = prepareScoringChanges(prepared);
      const payloadKey = JSON.stringify(prepared.payload);
      const checkpoint = savedQuestionCheckpoint.current;
      let questionId = questionIdForWholeSave(checkpoint, owner, payloadKey, prepared.questionId, formDirty);
      if (!questionId) {
        const saved = await questionWorkflow.persistQuestionSave(questionSaveInput(prepared, checkpoint, owner), { refresh: false, notify: false });
        if (!stillCurrent()) return;
        questionId = recordQuestionReceipt(saved, owner, payloadKey);
      }
      contentSaved = true;
      const scoringKey = scoringReceiptKey(questionId, rubric, rule);
      const alreadySaved = Boolean(matchingSaveReceipt(savedScoringCheckpoint.current, owner, scoringKey));
      if (!alreadySaved && rubric && !await rubricWorkflow.persistRubricSave(questionId, rubric, { refresh: false, notify: false })) throw new Error("评分细则未保存");
      if (!stillCurrent()) return;
      // 整题保存只保存固定规则草稿，发布仍由教师单独确认，不能顺带冻结版本。
      if (!alreadySaved && rule && !await scoringRuleWorkflow.persistScoringRuleSave(questionId, rule, false, { notify: false, reload: false })) throw new Error("评分规则未保存");
      if (!stillCurrent()) return;
      // 两区写入已成功而最终读取失败时，保留回执，重试刷新不再创建评分版本。
      savedScoringCheckpoint.current = { owner, payload: scoringKey }; scoringSaved = true;
      // 即使题目内容未修改，也独立保留评分写入事实；旧缓存不能在解除 busy 后回填到编辑器。
      const receipt: AcceptedSaveReceipt = { owner, examId: prepared.examId, questionId, kind: "whole",
        rubricStatus: fixedScoringQuestionTypes.has(prepared.payload.question_type) ? undefined : rubric?.status ?? selectedQuestion?.rubric?.status ?? "draft" };
      await confirmWholeSaveReceipt(receipt);
    } catch (error) {
      if (!stillCurrent()) return;
      // 两个接口不提供跨区域事务；记录题目已保存的事实，不刷新或清空剩余评分输入。
      setAllSaveMessage(wholeSaveFailureMessage(error, contentSaved, scoringSaved));
    } finally { saveInFlight.current = false; setSaveBusy(false); }
  }

  async function saveCurrentRubric(status: "draft" | "locked" = "draft") {
    if (!selectedQuestion || !selectedExam) return;
    const owner = activeSaveOwner.current;
    // 保存与确认是两个明确动作；不能靠未展示的状态值把普通保存变成锁定。
    const snapshot = { ...rubricWorkflow.prepareRubricSave(), status };
    const receipt: AcceptedSaveReceipt = { owner, examId: selectedExam.id, questionId: selectedQuestion.id, kind: "rubric", snapshot };
    const accepted = await rubricWorkflow.persistRubricSave(selectedQuestion.id, snapshot, {
      refresh: false, notify: false, onAccepted: result => { receipt.version = result.rubric?.version; }
    });
    if (!accepted || owner !== activeSaveOwner.current) return;
    // 写成功后保持编辑器挂载；重读只发 GET，不重放新版本创建，也不覆盖后续编辑。
    setAcceptedSaveReceipt(receipt);
    setAllSaveCompleted(false);
    setAllSaveMessage(`${savedRubricOutcome(receipt)}，正在重新读取保存结果。其他区域草稿继续保留。`);
    try {
      await loadConfig(receipt.examId, { silent: true, propagateError: true });
      if (owner === activeSaveOwner.current) finishAcceptedSave(receipt);
    } catch (error) {
      if (owner === activeSaveOwner.current) setAllSaveMessage(acceptedReadFailureMessage(error, receipt));
    }
  }

  function requestRubricSave(status: "draft" | "locked" = "draft") {
    const current = rubricSaveAction.current;
    if (!current || current.disabled || current.owner !== activeSaveOwner.current || saveInFlight.current || !selectedQuestion) return;
    if (status === "draft") { void saveSection(() => current.save("draft")); return; }
    if (current.contentDirty) { message.info("请先保存题目与答案，再确认评分细则。"); return; }
    try {
      const snapshotKey = JSON.stringify(current.prepare());
      modal.confirm({
        title: "确认本题评分细则？",
        content: `第 ${selectedQuestion.question_no} 题，满分 ${selectedQuestion.score} 分。锁定后，本题内容和评分细则将不可修改。`,
        okText: "确认并锁定", cancelText: "继续检查",
        onOk: () => {
          const latest = rubricSaveAction.current;
          // 全局确认框可能晚于换题或编辑；只允许提交用户刚刚核对过的同一份内容。
          if (!latest || latest.disabled || latest.owner !== activeSaveOwner.current || latest.owner !== current.owner || latest.source !== current.source) return;
          try {
            if (latest.contentDirty || JSON.stringify(latest.prepare()) !== snapshotKey) {
              message.info("评分内容已变化，请重新核对后确认。"); return;
            }
          } catch (error) { message.error(formatPaperError(error)); return; }
          return saveSection(() => latest.save("locked"));
        }
      });
    } catch (error) { message.error(formatPaperError(error)); }
  }

  const context: PaperRubricViewContext = {
    options: {
      canManage, canManageAssessment, canReadQuestionBank, canImportQuestionBank,
      questionBankScope, initialExamId, fixedExamId, mode, initialView, onExamChanged, onNavigate
    },
    config: { ...config, setSelectedExamId: (next) => navigateAfterDirtyCheck(() => config.setSelectedExamId(next)),
      loadExams: async () => { if (!saveInFlight.current) await config.loadExams(); } }, importWorkflow, questionWorkflow, rubricWorkflow, scoringRuleWorkflow, groupedScores,
    selectedExam, selectedQuestion, latestPaperImport, structureConfirmed, pendingDrafts, access,
    state: {
      formDirty, unsavedWork, saveBusy, allSaveMessage, allSaveCompleted, saveRefreshPending: Boolean(acceptedSaveReceipt) || groupedRefreshPending, activeCandidateIndex, detailTab, questionFilter, questionSearch,
      sourceOpen, indexOpen, visibleQuestions, selectedQuestionIndex, watchedQuestionType
    },
    actions: {
      setDetailTab, setQuestionFilter, setQuestionSearch, setSourceOpen, setIndexOpen,
      markQuestionDirty: () => { setFormDirty(true); setAllSaveCompleted(false); },
      openQuestion, openQuestionIssue, createQuestion, openCandidate, saveQuestion: saveCurrentQuestion, discardActiveDraft,
      saveRubric: requestRubricSave,
      saveScoringRule: (publish) => { void saveSection(() => saveScoringRule(publish)); },
      saveAllQuestionChanges: () => { void saveAllQuestionChanges(); },
      rereadSavedResult: () => { void rereadSavedResult(); },
      runValidation: () => { void runValidation(); },
      onBankCopied: async () => { if (selectedExam) { await loadConfig(selectedExam.id); onExamChanged?.(); } },
      onHistoryImported: async () => { if (selectedExam) await loadConfig(selectedExam.id, { silent: true }); },
      onStructureAdopted: async () => {
        if (!isCurrentMutationScope() || !selectedExam) return;
        setActiveCandidateIndex(null);
        // 资料确认需要区分登记成功与回读失败；导航检查仍要刷新实际已登记的服务端事实。
        try { await loadConfig(selectedExam.id, { silent: true, propagateError: true }); }
        finally { if (isCurrentMutationScope()) onExamChanged?.(); }
      },
      openTemplate: () => {
        if (onNavigate && selectedExam) navigateAfterDirtyCheck(() => onNavigate(`/exams/${encodeURIComponent(selectedExam.id)}/template`));
      }
    }
  };

  // 展示组件不持有业务状态；切换题目、保存草稿和识别填充的顺序仍由当前页面控制。
  return <PaperRubricView context={context} />;
}
