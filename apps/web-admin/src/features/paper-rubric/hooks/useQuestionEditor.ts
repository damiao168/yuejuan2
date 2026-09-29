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

const toleranceQuestionTypes = ["numeric", "formula", "calculation"];

export function questionAnswerText(value: unknown): string {
  if (value === null || value === undefined) return "";
  return typeof value === "object" ? JSON.stringify(value, null, 2) : String(value);
}

// 原本为数组或对象的答案必须保留结构，不能因文本框编辑变成普通字符串。
export function questionAnswerValue(text: string, original: unknown): unknown {
  if (original !== undefined && text === questionAnswerText(original)) return original;
  if (original !== null && typeof original === "object") {
    try {
      const parsed: unknown = JSON.parse(text);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed) !== Array.isArray(original)) throw new Error();
      return parsed;
    } catch {
      throw new Error("结构化标准答案必须保持有效 JSON 格式；请保留数组或对象结构");
    }
  }
  return text;
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
  onChanged
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
}) {
  const { message, modal } = App.useApp();
  const [form] = Form.useForm<QuestionFormValues>();
  const [savingQuestion, setSavingQuestion] = useState(false);
  const [showQuestionEditor, setShowQuestionEditor] = useState(false);
  const loadedQuestionKey = useRef<string | null>(null);

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
        score: selectedQuestion.score,
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

  const saveQuestion = async () => {
    if (!selectedExam) { message.error("请先选择考试"); return; }
    const values = await form.validateFields();
    let standardAnswer: unknown;
    try {
      standardAnswer = questionAnswerValue(values.standard_answer, editorMode === "edit" ? selectedQuestion?.answer_key?.standard_answer : undefined);
    } catch (error) {
      message.error(getUserErrorMessage(error, "标准答案格式不正确"));
      return false;
    }
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
    const tolerance: Record<string, unknown> = rawTolerance && typeof rawTolerance === "object" && !Array.isArray(rawTolerance)
      ? { ...rawTolerance as Record<string, unknown> }
      : {};
    // 表单相对容差用百分数，接口用比例；仅更新可编辑字段，保留其他容差扩展项。
    if (toleranceQuestionTypes.includes(values.question_type)) {
      if (values.tolerance_absolute === undefined || values.tolerance_absolute === null) delete tolerance.absolute;
      else tolerance.absolute = values.tolerance_absolute;
      if (values.tolerance_relative === undefined || values.tolerance_relative === null) delete tolerance.relative;
      else tolerance.relative = Number((values.tolerance_relative / 100).toFixed(8));
    }
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
    setSavingQuestion(true);
    try {
      if (editorMode === "edit" && selectedQuestion) {
        await updateQuestion(selectedQuestion.id, payload);
        message.success("题目已更新，标准答案将产生新版本");
      } else {
        const created = await createQuestion(selectedExam.id, payload);
        setSelectedQuestionId(created.question.id);
        message.success("题目已创建");
      }
      await loadConfig(selectedExam.id);
      onChanged?.();
      return true;
    } catch (error) {
      message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
      return false;
    } finally {
      setSavingQuestion(false);
    }
  };

  const confirmDeleteQuestion = () => {
    if (!selectedQuestion || !selectedExam) return;
    modal.confirm({
      title: "删除题目",
      content: `确认删除 ${selectedQuestion.question_no}？`,
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        await deleteQuestion(selectedQuestion.id);
        message.success("题目已删除");
        setEditorMode("create");
        setSelectedQuestionId(null);
        await loadConfig(selectedExam.id);
        onChanged?.();
      }
    });
  };

  return {
    form,
    savingQuestion,
    showQuestionEditor,
    setShowQuestionEditor,
    saveQuestion,
    confirmDeleteQuestion
  };
}
