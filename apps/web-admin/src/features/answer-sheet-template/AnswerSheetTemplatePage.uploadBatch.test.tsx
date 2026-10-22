// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { UploadProps } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AnswerSheetTemplatePage } from "../../pages/AnswerSheetTemplatePage";
import { answerQuestion, answerSource, answerTemplate } from "./testing/answerSheetFixtures";
import type { TemplatePageViewState } from "./view/templateViewTypes";
import type { useTemplateEditor } from "./editor/useTemplateEditor";
import type { useAnswerSheetSourceUpload } from "./editor/useAnswerSheetSourceUpload";
import type { useTemplatePreview } from "./editor/useTemplatePreview";

const mocks = vi.hoisted(() => ({
  view: undefined as TemplatePageViewState | undefined,
  papers: vi.fn(), questions: vi.fn(), templates: vi.fn(), exam: vi.fn(), getBinding: vi.fn(),
  message: { warning: vi.fn(), error: vi.fn(), success: vi.fn() }, confirm: vi.fn(),
  setBindingState: vi.fn(), uploadBatch: vi.fn(), editor: {} as TemplatePageViewState["editor"],
  editorInput: undefined as Parameters<typeof useTemplateEditor>[0] | undefined,
  onCreated: undefined as Parameters<typeof useAnswerSheetSourceUpload>[0]["onCreated"] | undefined,
  deleteSource: vi.fn(),
  libraryResolve: vi.fn(), libraryList: vi.fn(),
  preview: {} as ReturnType<typeof useTemplatePreview>
}));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: mocks.message, modal: { confirm: mocks.confirm } }) },
  Alert: () => null, Upload: { LIST_IGNORE: "ignore-file" },
  Modal: ({ children, onOk, okButtonProps }: { children: ReactNode; onOk: () => void; okButtonProps: { disabled: boolean } }) =>
    <div>{children}<button disabled={okButtonProps.disabled} onClick={onOk}>合并为一套答题卡</button></div>,
  Select: ({ value, options, onChange, mode, ...rest }: { value?: string[]; options: { value: string; label: string }[]; onChange: (value: string[]) => void; mode?: string; "aria-label": string }) =>
    <select aria-label={rest["aria-label"]} multiple={mode === "multiple"} value={value ?? []}
      onChange={event => onChange([...event.target.selectedOptions].map(option => option.value))}>
      {options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
    </select>
}));
vi.mock("../../api/answerSheetSources", () => ({ deleteAnswerSheetSource: mocks.deleteSource }));
vi.mock("../../api/papers", () => ({ listPapers: mocks.papers, listQuestions: mocks.questions }));
vi.mock("../../api/configuration", () => ({ listAnswerSheetTemplates: mocks.templates, getExamTemplateBinding: mocks.getBinding }));
vi.mock("../../api/exams", () => ({ getExam: mocks.exam }));
vi.mock("../../api/answerSheetTemplateLibrary", () => ({
  listAnswerSheetTemplateLibrary: mocks.libraryList, resolveAnswerSheetTemplate: mocks.libraryResolve,
  instantiateAnswerSheetTemplate: vi.fn(), publishAnswerSheetTemplate: vi.fn()
}));
vi.mock("./library/TemplateLibraryPanel", () => ({ TemplateLibraryPanel: () => null }));
vi.mock("../../components/PageState", () => ({ EmptyState: () => null, LoadingState: () => null, ErrorState: () => null }));
vi.mock("../../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: () => undefined }));
vi.mock("./editor/useTemplateEditor", () => ({ useTemplateEditor: (input: Parameters<typeof useTemplateEditor>[0]) => {
  mocks.editorInput = input;
  return mocks.editor;
} }));
vi.mock("./binding/useTemplateBinding", () => ({ useTemplateBinding: () => ({ setBindingState: mocks.setBindingState, bindingBusy: false }) }));
vi.mock("./printing/useStudentPrinting", () => ({ useStudentPrinting: () => ({ printBusy: false }) }));
vi.mock("./calibration/useOMRCalibration", () => ({ useOMRCalibration: () => ({}) }));
vi.mock("./editor/useTemplatePreview", () => ({ useTemplatePreview: () => mocks.preview }));
vi.mock("./editor/useAnswerSheetSourceUpload", () => ({ useAnswerSheetSourceUpload: (input: Parameters<typeof useAnswerSheetSourceUpload>[0]) => {
  mocks.onCreated = input.onCreated;
  return { uploading: false, uploadBatch: mocks.uploadBatch };
} }));
vi.mock("./view/TemplateWorkspaceView", () => ({
  TemplateToolbar: ({ view }: { view: TemplatePageViewState }) => { mocks.view = view; return null; },
  EmptyAnswerSheetSources: ({ view }: { view: TemplatePageViewState }) => { mocks.view = view; return null; },
  SourceUploadStatus: () => null, TemplateWorkspace: () => null
}));
vi.mock("./view/OMRCalibrationView", () => ({ CalibrationActionModal: () => null, CalibrationDrawer: () => null }));
vi.mock("./view/StudentPrintingView", () => ({ StudentPrintModal: () => null }));

type BeforeUploadFile = Parameters<NonNullable<UploadProps["beforeUpload"]>>[0];
function file(name: string): BeforeUploadFile {
  return Object.assign(new File(["synthetic sheet"], name, { type: "image/png" }), { uid: name, lastModifiedDate: new Date() });
}

describe("answer-sheet upload batch entry", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.view = undefined;
    mocks.editorInput = undefined;
    mocks.onCreated = undefined;
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.papers.mockImplementation((_examId: string, purpose?: string) => Promise.resolve({ papers: purpose ? [answerSource] : [] }));
    mocks.questions.mockResolvedValue({ questions: [answerQuestion] });
    mocks.templates.mockResolvedValue({ templates: [answerTemplate] });
    mocks.exam.mockResolvedValue({ exam: { status: "configured" } });
    mocks.getBinding.mockResolvedValue({ binding: null, revision: 0 });
    mocks.libraryList.mockResolvedValue({ libraries: [] });
    mocks.libraryResolve.mockResolvedValue({ status: "unknown", candidates: [] });
    mocks.editor = { name: answerTemplate.name, layout: answerTemplate.layout, setLayout: vi.fn(), saving: false,
      suggestingRegions: false, createDraft: vi.fn(), suggestQuestionRegions: vi.fn() } as unknown as TemplatePageViewState["editor"];
    mocks.preview = {
      pdfRef: { current: undefined }, pageNo: 1, setPageNo: vi.fn(),
      preview: { loading: false, width: 100, height: 200, pageCount: 1, sourceId: "source" }
    };
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render(canManage = true) { await act(async () => root.render(<AnswerSheetTemplatePage examId="exam" canManage={canManage} />)); }
  async function choose(files: BeforeUploadFile[]) {
    const outcomes: unknown[] = [];
    await act(async () => {
      // 与AntD一致：同次选择会逐文件回调，批次只应登记一次。
      for (const item of files) outcomes.push(await mocks.view!.uploadProps.beforeUpload!(item, files));
    });
    return outcomes;
  }

  it("allows multiple images and passes the selected batch once despite per-file callbacks", async () => {
    await render();
    expect(mocks.view!.uploadProps.multiple).toBe(true);
    const files = [file("02.png"), file("01.jpg"), file("03.png")];
    expect(await choose(files)).toEqual(["ignore-file", "ignore-file", "ignore-file"]);
    expect(mocks.uploadBatch).toHaveBeenCalledOnce();
    expect(mocks.uploadBatch).toHaveBeenCalledWith(files);
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("asks once before replacing an unsaved region draft and keeps the whole batch until confirmed", async () => {
    mocks.editor.name = "未保存区域模板";
    await render();
    const files = [file("01.png"), file("02.png")];
    await choose(files);
    expect(mocks.confirm).toHaveBeenCalledOnce();
    expect(mocks.uploadBatch).not.toHaveBeenCalled();
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(mocks.uploadBatch).toHaveBeenCalledOnce();
    expect(mocks.uploadBatch).toHaveBeenCalledWith(files);
  });

  it("rejects an invalid file anywhere in the batch without starting a partial upload", async () => {
    await render();
    await choose([file("01.png"), file("02.exe")]);
    expect(mocks.message.error).toHaveBeenCalledOnce();
    expect(mocks.uploadBatch).not.toHaveBeenCalled();
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("does not upload old selected files when their discard confirmation outlives an exam change", async () => {
    mocks.editor.name = "未保存内容";
    await render();
    await choose([file("正面.png"), file("反面.png")]);
    const confirmOldSelection = mocks.confirm.mock.calls[0][0].onOk;
    await act(async () => root.render(<AnswerSheetTemplatePage examId="next-exam" canManage />));
    await act(async () => confirmOldSelection());
    expect(mocks.uploadBatch).not.toHaveBeenCalled();
  });

  it.each(["viewer", "frozen"])("disables the batch picker for %s", async boundary => {
    if (boundary === "frozen") mocks.exam.mockResolvedValue({ exam: { status: "ready" } });
    await render(boundary !== "viewer");
    expect(mocks.view!.uploadProps.disabled).toBe(true);
  });

  it("matches a new upload, then creates one draft and automatically recognizes it after preview is ready", async () => {
    await render();
    const paper = { ...answerSource, id: "uploaded-source", file_asset_id: "uploaded-asset" };
    // 登记返回先于新底图解析；旧预览即使显示空闲，也不能用于新文件的模板页数。
    await act(async () => mocks.onCreated!(paper));
    expect(mocks.view!.selectedPaperId).toBe(paper.id);
    expect(mocks.editor.createDraft).not.toHaveBeenCalled();
    expect(mocks.editor.suggestQuestionRegions).not.toHaveBeenCalled();
    mocks.preview.preview = { ...mocks.preview.preview, sourceId: paper.id, pageCount: 3 };
    await render();
    expect(mocks.editor.createDraft).toHaveBeenCalledOnce();
    expect(mocks.libraryResolve).toHaveBeenCalledWith("exam", paper.id);
    await render();
    expect(mocks.editor.createDraft).toHaveBeenCalledOnce();

    const layout = { pages: Array.from({ length: 3 }, (_, index) => ({
      ...answerTemplate.layout.pages[0], page_no: index + 1, question_regions: []
    })) };
    const template = { ...answerTemplate, id: "uploaded-template", exam_paper_id: paper.id, page_count: 3, layout };
    mocks.editor.layout = layout;
    await act(async () => {
      mocks.editorInput!.setTemplates([template]);
      mocks.editorInput!.setSelectedTemplateId(template.id);
    });
    expect(mocks.editor.suggestQuestionRegions).toHaveBeenCalledOnce();
    await render();
    expect(mocks.editor.suggestQuestionRegions).toHaveBeenCalledOnce();
  });

  it.each(["ambiguous", "failure"])("does not create a draft when matching is %s", async status => {
    mocks.templates.mockResolvedValue({ templates: [] });
    if (status === "failure") mocks.libraryResolve.mockRejectedValue(new Error("network"));
    else mocks.libraryResolve.mockResolvedValue({ status: "ambiguous", candidates: [] });
    await render();
    expect(mocks.libraryResolve).toHaveBeenCalledOnce();
    expect(mocks.editor.createDraft).not.toHaveBeenCalled();
    expect(mocks.editor.suggestQuestionRegions).not.toHaveBeenCalled();
    expect(mocks.view!.sourceRecognitionBlocked).toBe(true);
  });

  it("uses a matched library instance without creating or analyzing a new template", async () => {
    mocks.templates.mockResolvedValue({ templates: [] });
    const instance = { template: answerTemplate, paper: answerSource, reused: false };
    mocks.libraryResolve.mockResolvedValue({ status: "matched", candidates: [], instance });
    await render();
    expect(mocks.view!.selectedTemplate?.id).toBe(answerTemplate.id);
    expect(mocks.editor.createDraft).not.toHaveBeenCalled();
    expect(mocks.editor.suggestQuestionRegions).not.toHaveBeenCalled();
  });

  it("keeps existing draft regions without automatically recalculating them on open or rerender", async () => {
    await render();
    await render();
    expect(mocks.view!.selectedTemplate?.id).toBe(answerTemplate.id);
    expect(mocks.editor.createDraft).not.toHaveBeenCalled();
    expect(mocks.editor.suggestQuestionRegions).not.toHaveBeenCalled();
  });

  it("never starts recognition when an existing empty draft or its preview becomes ready", async () => {
    const layout = { pages: answerTemplate.layout.pages.map(page => ({ ...page, question_regions: [] })) };
    mocks.templates.mockResolvedValue({ templates: [{ ...answerTemplate, layout }] });
    mocks.editor.layout = layout;
    mocks.preview.preview = { ...mocks.preview.preview, sourceId: "previous-source" };
    await render();
    expect(mocks.editor.suggestQuestionRegions).not.toHaveBeenCalled();
    mocks.preview.preview = { ...mocks.preview.preview, sourceId: answerSource.id };
    await render();
    expect(mocks.editor.suggestQuestionRegions).not.toHaveBeenCalled();
    await render();
    expect(mocks.editor.suggestQuestionRegions).not.toHaveBeenCalled();
  });

  it("automatically creates one draft when opening an existing answer-sheet source without a template", async () => {
    mocks.templates.mockResolvedValue({ templates: [] });
    const editingCapabilities: boolean[] = [];
    mocks.editor.createDraft = vi.fn(async () => { editingCapabilities.push(mocks.editorInput!.canManage); });
    await render();
    expect(mocks.view!.selectedPaperId).toBe(answerSource.id);
    expect(mocks.view!.selectedTemplate).toBeUndefined();
    expect(mocks.editor.createDraft).toHaveBeenCalledOnce();
    // 匹配结束先解除编辑暂停，再调用生命周期；否则真实 hook 会拒绝创建但自动标记已消耗。
    expect(editingCapabilities).toEqual([true]);
    await render();
    expect(mocks.editor.createDraft).toHaveBeenCalledOnce();
  });

  it("deletes the selected source only after a dangerous confirmation and reloads remaining sources", async () => {
    const first = { ...answerSource, id: "first", file_asset_id: "first-asset", file: { ...answerSource.file, original_name: "01.png", content_type: "image/png" } };
    const second = { ...first, id: "second", file_asset_id: "second-asset", file: { ...first.file, original_name: "02.pdf", content_type: "application/pdf" } };
    mocks.papers.mockImplementation((_examId: string, purpose?: string) => Promise.resolve({ papers: purpose ? [first, second] : [] }));
    mocks.templates.mockResolvedValue({ templates: [] });
    mocks.confirm.mockReturnValue({ destroy: vi.fn() });
    mocks.deleteSource.mockResolvedValue({ deleted: true, paper_id: first.id, deleted_template_ids: [] });
    await render();
    expect(mocks.view!.sourceManagement?.canDelete).toBe(true);
    expect("sourceCombination" in mocks.view!).toBe(false);
    await act(async () => mocks.view!.sourceManagement!.removeCurrent());
    expect(mocks.deleteSource).not.toHaveBeenCalled();
    const confirmation = mocks.confirm.mock.calls[0][0];
    expect(confirmation.okButtonProps).toEqual({ danger: true });
    mocks.papers.mockImplementation((_examId: string, purpose?: string) => Promise.resolve({ papers: purpose ? [second] : [] }));
    await act(async () => confirmation.onOk());
    expect(mocks.deleteSource).toHaveBeenCalledWith("exam", first.id);
    expect(mocks.uploadBatch).not.toHaveBeenCalled();
    expect(mocks.view!.papers.map(paper => paper.id)).toEqual(["second"]);
    expect(mocks.view!.selectedPaperId).toBe("second");
    expect(host.querySelector('[aria-label="选择要合并的答题卡文件"]')).toBeNull();
  });
});
