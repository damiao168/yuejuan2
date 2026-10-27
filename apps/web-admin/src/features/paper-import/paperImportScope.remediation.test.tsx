// @vitest-environment jsdom
import { createElement } from "react";
import { act, cleanup, render, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { Question } from "../../api/papers";
import { useQuestionEditor } from "../../features/paper-rubric/hooks/useQuestionEditor";
import { usePaperConfigData } from "../../features/paper-import/usePaperConfigData";
import { usePaperImportMaterials } from "../../features/paper-import/usePaperImportMaterials";
import { PaperRubricPage } from "../../pages/PaperRubricPage";
import type { PaperRubricViewContext } from "../../features/paper-rubric/paperRubricViewTypes";

// 合成题目；只 mock API 和表单宿主，使用真实 hook 的加载、组装和写入路径。
const mocks = vi.hoisted(() => ({ values: {} as Record<string, unknown>, update: vi.fn(), upload: vi.fn(), createImport: vi.fn(),
  context: null as PaperRubricViewContext | null, message: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() }, modal: { confirm: vi.fn() } }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: mocks.message, modal: mocks.modal }) }, Form: { useWatch: () => undefined, useForm: () => [{
  setFieldsValue: (values: Record<string, unknown>) => { mocks.values = values; }, validateFields: async () => mocks.values,
}] } }));
vi.mock("../../api/papers", () => ({ updateQuestion: mocks.update, createQuestion: vi.fn(), deleteQuestion: vi.fn(),
  listPapers: async () => ({ papers: [] }), listPaperImports: async () => ({ imports: [] }),
  listQuestions: async (exam: string) => ({ questions: [{ id: `question-${exam}`, exam_id: exam, score: 5, stem: exam }] }),
  uploadFile: mocks.upload, createPaperImport: mocks.createImport,
  addPaperImportSources: vi.fn(), cancelPaperImport: vi.fn(), replacePaperImportSources: vi.fn(), retryPaperImportParse: vi.fn(),
  listScoringRules: async () => ({ scoring_rules: [] }), validatePaperConfig: vi.fn(), createRubric: vi.fn(),
}));
vi.mock("../../api/exams", () => ({ listExams: async () => ({ exams: [{ id: "exam-A", subject: "math" }, { id: "exam-B", subject: "math" }] }) }));
vi.mock("../../features/paper-import/usePaperImportPolling", () => ({ usePaperImportPolling: () => undefined }));
vi.mock("../../features/paper-rubric/PaperRubricView", () => ({ PaperRubricView: ({ context }: { context: PaperRubricViewContext }) => { mocks.context = context; return null; } }));
afterEach(cleanup);

it("preserves structured equivalent answers during an unrelated stem edit", async () => {
  const original = [{ value: 2, unit: "cm" }, ["A", "C"]];
  const question = { id: "synthetic-question", tenant_id: "synthetic-tenant", exam_id: "synthetic-exam", question_no: "1", question_type: "short_answer", score: 5,
    stem: "原题干", options: [], knowledge_points: [], sort_order: 1, status: "draft",
    answer_key: { standard_answer: "主答案", equivalent_answers: original, tolerance: {} } } as unknown as Question;
  mocks.update.mockResolvedValue({ question });
  const { result } = renderHook(() => useQuestionEditor({ selectedExam: { id: question.exam_id } as never, selectedQuestion: question,
    papers: [], questionCount: 1, editorMode: "edit", setEditorMode: vi.fn(), setSelectedQuestionId: vi.fn(), loadConfig: vi.fn().mockResolvedValue(undefined) }));
  mocks.values = { ...mocks.values, stem: "只修正题干" };
  await act(async () => { expect(await result.current.saveQuestion()).toBe(true); });
  const payload = mocks.update.mock.calls[0][1];
  expect(payload.answer_key.standard_answer).toBe("主答案");
  expect(payload.answer_key.equivalent_answers).toEqual(original);
  expect(question.answer_key!.equivalent_answers).toEqual(original);
});

it("keeps the selected exam after an upload finishes through the production page", async () => {
  let finishUpload!: (value: unknown) => void;
  mocks.upload.mockReturnValue(new Promise(resolve => { finishUpload = resolve; }));
  mocks.createImport.mockResolvedValue({ import: { status: "processing" } });
  render(createElement(PaperRubricPage, { canManage: true, canManageAssessment: false, initialExamId: "exam-A" }));
  await waitFor(() => expect(mocks.context?.config.questions[0]?.exam_id).toBe("exam-A"));
  let uploading!: Promise<boolean>;
  act(() => { uploading = mocks.context!.importWorkflow.importPastedText("这是一段足够长的合成试卷文字材料用于考试切换复现", "question"); });
  expect(mocks.context!.importWorkflow.parsing).toBe(true);
  act(() => mocks.context!.config.setSelectedExamId("exam-B"));
  await waitFor(() => expect(mocks.context?.config.questions[0]?.exam_id).toBe("exam-B"));
  await act(async () => { finishUpload({ file: { id: "synthetic-file" } }); expect(await uploading).toBe(true); });
  expect(mocks.context!.selectedExam?.id).toBe("exam-B");
  expect(mocks.context!.selectedQuestion?.exam_id).toBe("exam-B");
});

it("keeps exam B data when an upload for exam A completes", async () => {
  let finishUpload!: (value: unknown) => void;
  mocks.upload.mockReturnValue(new Promise(resolve => { finishUpload = resolve; }));
  mocks.createImport.mockResolvedValue({ import: { status: "processing" } });
  const { result } = renderHook(() => {
    const config = usePaperConfigData({ initialExamId: "exam-A" });
    const materials = usePaperImportMaterials({ canManage: true,
      selectedExam: config.exams.find(exam => exam.id === config.selectedExamId), papers: config.papers,
      paperImports: config.paperImports, loadConfig: config.loadConfig, reviewDirty: false, setParsing: vi.fn() });
    return { config, materials };
  });
  await waitFor(() => expect(result.current.config.questions[0]?.exam_id).toBe("exam-A"));
  let uploading!: Promise<boolean>;
  act(() => { uploading = result.current.materials.importPastedText("这是一段足够长的合成试卷文字材料用于考试切换复现", "question"); });
  act(() => result.current.config.setSelectedExamId("exam-B"));
  await waitFor(() => expect(result.current.config.questions[0]?.exam_id).toBe("exam-B"));
  await act(async () => { finishUpload({ file: { id: "synthetic-file" } }); expect(await uploading).toBe(true); });
  expect(result.current.config.selectedExamId).toBe("exam-B");
  expect(result.current.config.questions[0]?.exam_id).toBe("exam-B");
  expect(mocks.createImport).toHaveBeenCalledWith("exam-A", expect.anything(), expect.any(String));
});

it("keeps exam B data when an upload for exam A fails and its recovery refresh runs", async () => {
  let failUpload!: (error: Error) => void;
  mocks.upload.mockReturnValue(new Promise((_resolve, reject) => { failUpload = reject; }));
  mocks.createImport.mockResolvedValue({ import: { status: "processing" } });
  const { result } = renderHook(() => {
    const config = usePaperConfigData({ initialExamId: "exam-A" });
    const materials = usePaperImportMaterials({ canManage: true,
      selectedExam: config.exams.find(exam => exam.id === config.selectedExamId), papers: config.papers,
      paperImports: config.paperImports, loadConfig: config.loadConfig, reviewDirty: false, setParsing: vi.fn() });
    return { config, materials };
  });
  await waitFor(() => expect(result.current.config.questions[0]?.exam_id).toBe("exam-A"));
  let uploading!: Promise<boolean>;
  act(() => { uploading = result.current.materials.importPastedText("这是一段足够长的合成试卷文字材料用于考试切换复现", "question"); });
  act(() => result.current.config.setSelectedExamId("exam-B"));
  await waitFor(() => expect(result.current.config.questions[0]?.exam_id).toBe("exam-B"));
  await act(async () => { failUpload(new Error("synthetic upload failure")); expect(await uploading).toBe(false); });
  expect(result.current.config.selectedExamId).toBe("exam-B");
  expect(result.current.config.questions[0]?.exam_id).toBe("exam-B");

});
