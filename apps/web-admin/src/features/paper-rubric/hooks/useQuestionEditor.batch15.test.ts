// @vitest-environment jsdom
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../../../api/exams";
import type { Question } from "../../../api/papers";
import { questionAnswerText, useQuestionEditor, type QuestionFormValues } from "./useQuestionEditor";

const mocks = vi.hoisted(() => ({
  message: { error: vi.fn(), success: vi.fn() },
  form: { setFieldsValue: vi.fn(), validateFields: vi.fn() },
  updateQuestion: vi.fn(),
}));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: mocks.message }) },
  Form: { useForm: () => [mocks.form] },
}));
vi.mock("../../../api/papers", () => ({
  createQuestion: vi.fn(), deleteQuestion: vi.fn(), updateQuestion: mocks.updateQuestion,
}));

const exam: Exam = {
  id: "exam", tenant_id: "tenant", school_id: "school", name: "数学", subject: "数学", exam_type: "期中",
  total_score: 100, status: "draft", grading_mode: "human_review_required", appeal_enabled: false,
  publish_policy: "manual", created_by: "teacher", class_ids: [], revision: 1,
};
const standardAnswer = { value: 2 };
const oldTolerance = { absolute: 0.1, relative: 0.02, unit: "cm" };
const question: Question = {
  id: "question", tenant_id: "tenant", exam_id: exam.id, question_no: "1", question_type: "numeric", score: 5,
  options: [], knowledge_points: [], answer_area: { page: 1, source: "template" }, sort_order: 1, status: "draft",
  answer_key: { id: "answer", question_id: "question", answer_version: "v1", standard_answer: standardAnswer,
    equivalent_answers: [], tolerance: oldTolerance },
};

describe("题目编辑容差提交", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.clearAllMocks();
    mocks.updateQuestion.mockResolvedValue({});
    host = document.createElement("div");
    document.body.appendChild(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  it.each([
    { type: "numeric", absolute: 0.5, relative: 3, expected: { absolute: 0.5, relative: 0.03, unit: "cm" } },
    { type: "formula", absolute: null, relative: null, expected: { unit: "cm" } },
    { type: "short_answer", absolute: 0.5, relative: 3, expected: oldTolerance },
  ])("$type 保留容差字段规则和提交后刷新顺序", async ({ type, absolute, relative, expected }) => {
    const values: QuestionFormValues = {
      question_no: "1", question_type: type, score: 5, stem: "题目", knowledge_points: [],
      answer_area_page: 1, answer_area_x: 0, answer_area_y: 0, answer_area_w: 10, answer_area_h: 10,
      sort_order: 1, standard_answer: questionAnswerText(standardAnswer), equivalent_answers: [],
      tolerance_absolute: absolute, tolerance_relative: relative,
    };
    mocks.form.validateFields.mockResolvedValue(values);
    const loadConfig = vi.fn().mockResolvedValue(undefined);
    const onChanged = vi.fn();
    let editor: ReturnType<typeof useQuestionEditor>;
    function Harness() {
      editor = useQuestionEditor({ selectedExam: exam, selectedQuestion: question, papers: [], questionCount: 1,
        editorMode: "edit", setEditorMode: vi.fn(), setSelectedQuestionId: vi.fn(), loadConfig, onChanged });
      return null;
    }
    await act(async () => root.render(createElement(Harness)));
    await act(async () => { expect(await editor.saveQuestion()).toBe(true); });
    expect(mocks.updateQuestion).toHaveBeenCalledOnce();
    const [id, payload] = mocks.updateQuestion.mock.calls[0];
    expect(id).toBe(question.id);
    expect(payload.answer_key.tolerance).toEqual(expected);
    expect(payload.answer_key.standard_answer).toBe(standardAnswer);
    expect(payload.answer_area).toEqual({ page: 1, source: "template", x: 0, y: 0, w: 10, h: 10 });
    expect(oldTolerance).toEqual({ absolute: 0.1, relative: 0.02, unit: "cm" });
    expect(loadConfig).toHaveBeenCalledExactlyOnceWith(exam.id);
    expect(onChanged).toHaveBeenCalledOnce();
    expect(mocks.updateQuestion.mock.invocationCallOrder[0]).toBeLessThan(loadConfig.mock.invocationCallOrder[0]);
    expect(loadConfig.mock.invocationCallOrder[0]).toBeLessThan(onChanged.mock.invocationCallOrder[0]);
    expect(mocks.message.error).not.toHaveBeenCalled();
  });
});
