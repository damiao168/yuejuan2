import { useEffect, useRef, useState } from "react";
import { App, Form } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import type { Exam } from "../../../api/exams";
import {
  createQuestion,
  deleteQuestion,
  updateQuestion,
  type PaperVersion,
  type Question,
  type QuestionPayload
} from "../../../api/papers";

export interface QuestionFormValues {
  exam_paper_id?: string;
  question_no: string;
  question_type: string;
  score: number;
  stem: string;
  options?: string[];
  knowledge_points: string[];
  answer_area_page: number;
  answer_area_x: number;
  answer_area_y: number;
  answer_area_w: number;
  answer_area_h: number;
  sort_order: number;
  standard_answer: string;
  equivalent_answers: string[];
  tolerance_absolute?: number | null;
  tolerance_relative?: number | null;
}

const toleranceQuestionTypes = new Set(["numeric", "formula", "calculation"]);
export interface PreparedQuestionSave { examId: string; questionId?: string; payload: QuestionPayload }

export function questionAnswerText(value: unknown): string {
  if (value === null || value === undefined) return "";
  // 这是表单内部传值；界面按题型编辑，序列化保留原数组和对象结构。
  switch (typeof value) {
    // 浏览器兼容值的 typeof 可能为 undefined，却不等于 undefined，仍沿用 String 转换。
    case "undefined":
    case "string":
    case "number":
    case "boolean":
    case "bigint":
    case "symbol":
    case "function":
      return String(value);
    default:
      return JSON.stringify(value, null, 2);
  }
}

// 原本为数组或对象的答案必须保留结构，不能因文本框编辑变成普通字符串。
export function questionAnswerValue(text: string, original: unknown, questionType?: string): unknown {
  if (original !== undefined && text === questionAnswerText(original)) return original;
  if (original !== null && typeof original === "object") {
    try {
      const parsed: unknown = JSON.parse(text);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed) !== Array.isArray(original)) throw new Error("标准答案结构与原值不一致");
      return parsed;
    } catch {
      throw new Error("标准答案暂不可保存，请核对各空答案或联系题目负责人检查历史数据。");
    }
  }
  // 选择控件以序列化字符串传值；只有明确的结构化题型才解析，简答题中的文字保持原样。
  if (["multiple_choice", "fill_blank", "true_false", "numeric"].includes(questionType ?? "")) {
    try {
      const value: unknown = JSON.parse(text);
      if (Array.isArray(value) || typeof value === "boolean" || typeof value === "number") return value;
    } catch { /* 普通填空文本无需序列化，按原文本提交。 */ }
  }
  return text;
}

function questionTolerance(values: QuestionFormValues, rawTolerance: unknown): Record<string, unknown> {
  const tolerance: Record<string, unknown> = rawTolerance && typeof rawTolerance === "object" && !Array.isArray(rawTolerance)
    ? { ...rawTolerance as Record<string, unknown> }
    : {};
  if (!toleranceQuestionTypes.has(values.question_type)) return tolerance;
  // 表单相对容差用百分数，接口用比例；仅更新可编辑字段，保留其他容差扩展项。
  if (values.tolerance_absolute === undefined || values.tolerance_absolute === null) delete tolerance.absolute;
  else tolerance.absolute = values.tolerance_absolute;
  if (values.tolerance_relative === undefined || values.tolerance_relative === null) delete tolerance.relative;
  else tolerance.relative = Number((values.tolerance_relative / 100).toFixed(8));
  return tolerance;
}

export function useQuestionEditor({
  selectedExam,
  selectedQuestion,
  papers,
  questionCount,
  editorMode,
  setEditorMode,
  setSelectedQuestionId,
  preserveUnsaved,
  loadConfig,
  onChanged,
  isMutationBlocked,
  runMutation
}: {
  selectedExam?: Exam;
  selectedQuestion: Question | null;
  papers: PaperVersion[];
  questionCount: number;
  editorMode: "create" | "edit";
  setEditorMode: (mode: "create" | "edit") => void;
  setSelectedQuestionId: (id: string | null) => void;
  preserveUnsaved?: boolean;
  loadConfig: (examId: string, options?: { silent?: boolean }) => Promise<void>;
  onChanged?: () => void;
  isMutationBlocked?: () => boolean;
  runMutation?: (action: () => Promise<unknown>) => Promise<void>;
}) {
  const { message, modal } = App.useApp();
  const [form] = Form.useForm<QuestionFormValues>();
  const [savingQuestion, setSavingQuestion] = useState(false);
  const [showQuestionEditor, setShowQuestionEditor] = useState(false);
  const loadedQuestionKey = useRef<string | null>(null);
  const saveInFlight = useRef(false);

  useEffect(() => {
    const questionKey = selectedQuestion?.id ?? "create";
    if (preserveUnsaved && loadedQuestionKey.current === questionKey) return;
    loadedQuestionKey.current = questionKey;
    if (selectedQuestion) {
      const answerArea = selectedQuestion.answer_area ?? {};
      const rawTolerance = selectedQuestion.answer_key?.tolerance;
      const tolerance: Record<string, unknown> = rawTolerance && typeof rawTolerance === "object" && !Array.isArray(rawTolerance)
        ? rawTolerance as Record<string, unknown>
        : {};
      form.setFieldsValue({
        exam_paper_id: selectedQuestion.exam_paper_id,
        question_no: selectedQuestion.question_no,
        question_type: selectedQuestion.question_type,
        score: selectedQuestion.score > 0 ? selectedQuestion.score : undefined,
        stem: selectedQuestion.stem ?? "",
        options: selectedQuestion.options ?? [],
        knowledge_points: selectedQuestion.knowledge_points,
        answer_area_page: Number(answerArea.page ?? 1),
        answer_area_x: Number(answerArea.x ?? 0),
        answer_area_y: Number(answerArea.y ?? 0),
        answer_area_w: Number(answerArea.w ?? 0),
        answer_area_h: Number(answerArea.h ?? 0),
        sort_order: selectedQuestion.sort_order,
        standard_answer: questionAnswerText(selectedQuestion.answer_key?.standard_answer),
        equivalent_answers: (selectedQuestion.answer_key?.equivalent_answers ?? []).map(String),
        tolerance_absolute: tolerance.absolute === undefined || tolerance.absolute === null ? undefined : Number(tolerance.absolute),
        tolerance_relative: tolerance.relative === undefined || tolerance.relative === null
          ? undefined
          : Number((Number(tolerance.relative) * 100).toFixed(6))
      });
      return;
    }
    form.setFieldsValue({
      exam_paper_id: papers[0]?.id,
      question_no: "",
      question_type: "short_answer",
      score: 10,
      stem: "",
      options: [],
      knowledge_points: [],
      answer_area_page: 1,
      answer_area_x: 0,
      answer_area_y: 0,
      answer_area_w: 0,
      answer_area_h: 0,
      sort_order: questionCount + 1,
      standard_answer: "",
      equivalent_answers: [],
      tolerance_absolute: undefined,
      tolerance_relative: undefined
    });
  }, [form, papers, questionCount, preserveUnsaved, selectedQuestion]);

  const prepareQuestionSave = async (): Promise<PreparedQuestionSave> => {
    if (!selectedExam) throw new Error("请先选择考试");
    const values = await form.validateFields();
    const standardAnswer = questionAnswerValue(values.standard_answer, editorMode === "edit" ? selectedQuestion?.answer_key?.standard_answer : undefined, values.question_type);
    if (!Number.isFinite(values.score) || values.score <= 0) throw new Error("请填写大于 0 的题目分值");
    const answerAreaBase = editorMode === "edit" ? selectedQuestion?.answer_area ?? {} : {};
    const answerArea: Record<string, unknown> = {
      ...answerAreaBase,
      page: values.answer_area_page ?? answerAreaBase.page ?? 1,
      x: values.answer_area_x ?? answerAreaBase.x ?? 0,
      y: values.answer_area_y ?? answerAreaBase.y ?? 0,
      w: values.answer_area_w ?? answerAreaBase.w ?? 0,
      h: values.answer_area_h ?? answerAreaBase.h ?? 0
    };
    const rawTolerance = editorMode === "edit" ? selectedQuestion?.answer_key?.tolerance : undefined;
    const tolerance = questionTolerance(values, rawTolerance);
    const payload: QuestionPayload = {
      exam_paper_id: values.exam_paper_id ?? selectedQuestion?.exam_paper_id ?? papers[0]?.id,
      question_no: values.question_no,
      question_type: values.question_type,
      score: values.score,
      stem: values.stem,
      options: ["single_choice", "multiple_choice"].includes(values.question_type) ? values.options ?? [] : [],
      parent_question_no: selectedQuestion?.parent_question_no,
      subquestion_no: selectedQuestion?.subquestion_no,
      knowledge_points: values.knowledge_points ?? selectedQuestion?.knowledge_points ?? [],
      answer_area: answerArea,
      sort_order: values.sort_order ?? selectedQuestion?.sort_order ?? questionCount + 1,
      answer_key: {
        standard_answer: standardAnswer,
        equivalent_answers: values.equivalent_answers ?? [],
        tolerance
      }
    };
    return { examId: selectedExam.id, questionId: editorMode === "edit" ? selectedQuestion?.id : undefined, payload };
  };

  const persistQuestionSave = async (prepared: PreparedQuestionSave, options: { refresh?: boolean; notify?: boolean } = {}) => {
    if (saveInFlight.current) return null;
    saveInFlight.current = true; setSavingQuestion(true);
    try {
      let questionId = prepared.questionId;
      if (questionId) {
        await updateQuestion(questionId, prepared.payload);
        if (options.notify !== false) message.success("题目已更新，标准答案将产生新版本");
      } else {
        const created = await createQuestion(prepared.examId, prepared.payload);
        questionId = created.question.id;
        if (options.refresh !== false) setSelectedQuestionId(questionId);
        if (options.notify !== false) message.success("题目已创建");
      }
      // 整题保存由页面在评分写入后统一刷新，不能在此重置尚未提交的评分草稿。
      if (options.refresh !== false) { await loadConfig(prepared.examId); onChanged?.(); }
      return { questionId, payload: prepared.payload };
    } finally {
      saveInFlight.current = false; setSavingQuestion(false);
    }
  };

  const saveQuestion = async () => {
    try { return Boolean(await persistQuestionSave(await prepareQuestionSave())); }
    catch (error) {
      if (!(error && typeof error === "object" && "errorFields" in error)) message.error(getUserErrorMessage(error, "标准答案或题目暂不可保存"));
      return false;
    }
  };

  const confirmDeleteQuestion = () => {
    if (!selectedQuestion || !selectedExam || isMutationBlocked?.()) return;
    modal.confirm({
      title: "删除题目",
      content: `确认删除 ${selectedQuestion.question_no}？`,
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        // 确认框可能早于保存打开，确认时仍须经过页面同步锁，避免删除与整题保存并发。
        if (isMutationBlocked?.()) return;
        const remove = async () => {
          await deleteQuestion(selectedQuestion.id);
          message.success("题目已删除");
          setEditorMode("create");
          setSelectedQuestionId(null);
          await loadConfig(selectedExam.id);
          onChanged?.();
        };
        if (runMutation) await runMutation(remove); else await remove();
      }
    });
  };

  return {
    form,
    savingQuestion,
    showQuestionEditor,
    setShowQuestionEditor,
    saveQuestion,
    prepareQuestionSave,
    persistQuestionSave,
    confirmDeleteQuestion
  };
}
