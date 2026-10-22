// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../../api/businessCommand";
import * as api from "../../api/referenceMaterials";
import { ProtectedReferenceImage, ReferenceMaterialEditor, orderReferenceItems, validReferenceBox, validReferenceSelection } from "./ReferenceMaterialEditor";

vi.mock("../../api/referenceMaterials", () => ({
  getReferenceMaterials: vi.fn(), getReferenceSources: vi.fn(), getReferenceSourceImage: vi.fn(), getReferenceCropImage: vi.fn(),
  createReferenceCrop: vi.fn(), saveReferenceMaterials: vi.fn(), withdrawReferenceMaterials: vi.fn(), resumeReferenceCommand: vi.fn(),
}));
vi.mock("antd", () => ({
  Alert: ({ message }: { message: ReactNode }) => <p role="alert">{message}</p>,
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick?: () => void }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Drawer: ({ children, open, onClose }: { children: ReactNode; open: boolean; onClose: () => void }) => open ? <div><button onClick={onClose}>关闭抽屉</button>{children}</div> : null,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Select: ({ options, value, onChange, "aria-label": label }: { options: Array<{ value: string; label: string; disabled?: boolean }>; value?: string; onChange: (value: string) => void; "aria-label": string }) =>
    <select aria-label={label} value={value ?? ""} onChange={event => onChange(event.target.value)}><option value="" />{options.map(option => <option key={option.value} value={option.value} disabled={option.disabled}>{option.label}</option>)}</select>,
  InputNumber: ({ value, onChange }: { value: number; onChange: (value: number) => void }) => <input type="number" value={value} onChange={event => onChange(Number(event.target.value))} />,
  Input: { TextArea: ({ value, onChange, "aria-label": label }: { value: string; onChange: (event: React.ChangeEvent<HTMLTextAreaElement>) => void; "aria-label": string }) => <textarea aria-label={label} value={value} onChange={onChange} /> },
}));

const source: api.ReferenceSource = { page_ref: "page-a", name: "合成资料", kind: "image", page_no: 1, sha256: "page-hash", width: 1200, height: 1000 };
const crop = (ref = "crop-a", role: api.ReferenceRole = "question_diagram"): api.ReferenceItem => ({ id: ref, crop_ref: ref, role, ordinal: 1, required: true, sha256: "crop-hash", width: 300, height: 200, size_bytes: 100, pixel_bbox: { x: 10, y: 20, width: 300, height: 200 }, source });
const materials = (items: api.ReferenceItem[] = []): api.ReferenceMaterials => ({ mode: items.length ? "reference_images" : "legacy_unrecorded", revision: 1, frozen: false, items });
const pendingKey = (question = "question-a", actor = "teacher") => `reference-material-command:tenant:${actor}:exam:${question}`;
const blobResult = () => ({ blob: new Blob(["synthetic"], { type: "image/png" }), contentType: "image/png", filename: undefined, watermark: undefined });
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
const button = (name: string) => screen.getByRole<HTMLButtonElement>("button", { name });
async function openEditor() { fireEvent.click(button("核对本题参考图")); await screen.findByText("本题尚未记录材料模式"); }
async function chooseSource() { fireEvent.change(screen.getByLabelText("选择已有资料页"), { target: { value: source.page_ref } }); return screen.findByAltText("参考图来源页"); }
async function generateCrop() { const image = await chooseSource(); fireEvent.load(image); fireEvent.click(button("生成实际裁图")); await screen.findByRole("heading", { name: "核对将发送的实际图片" }); }

beforeEach(() => {
  vi.clearAllMocks(); sessionStorage.clear(); setBusinessCommandScope("tenant", "teacher");
  vi.stubGlobal("URL", Object.assign(class extends URL {}, { createObjectURL: vi.fn(() => `blob:synthetic-${Math.random()}`), revokeObjectURL: vi.fn() }));
  vi.stubGlobal("PointerEvent", class extends MouseEvent { pointerId: number; constructor(type: string, init: PointerEventInit = {}) { super(type, init); this.pointerId = init.pointerId ?? 0; } });
  vi.mocked(api.getReferenceMaterials).mockResolvedValue({ materials: materials() });
  vi.mocked(api.getReferenceSources).mockResolvedValue({ sources: [source] });
  vi.mocked(api.getReferenceSourceImage).mockResolvedValue(blobResult());
  vi.mocked(api.getReferenceCropImage).mockResolvedValue(blobResult());
  vi.mocked(api.createReferenceCrop).mockResolvedValue({ crop: crop() });
  vi.mocked(api.saveReferenceMaterials).mockResolvedValue({ materials: { ...materials(), mode: "text_only", revision: 2 } });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); setBusinessCommandScope("", ""); });

describe("参考图选择和像素边界", () => {
  it("limits each role to two unique crops and always sends question diagrams first", () => {
    const items = [crop("s1", "reference_solution"), crop("q1"), crop("s2", "reference_solution"), crop("q2")];
    expect(validReferenceSelection(items)).toBe(true);
    expect(orderReferenceItems(items).map(item => [item.crop_ref, item.ordinal])).toEqual([["q1", 1], ["q2", 2], ["s1", 3], ["s2", 4]]);
    expect(validReferenceSelection([crop(), crop()])).toBe(false);
    expect(validReferenceSelection([crop("q1"), crop("q2"), crop("q3")])).toBe(false);
    expect(validReferenceSelection([crop("", "reference_solution")])).toBe(false);
    expect(validReferenceSelection([{ crop_ref: "bad-role", role: "unknown" as api.ReferenceRole }])).toBe(false);
    expect(validReferenceSelection([])).toBe(false);
  });
  it.each([
    [{ x: 0, y: 0, width: 1200, height: 1000 }, true],
    [{ x: 1199, y: 999, width: 1, height: 1 }, true],
    [{ x: 1200, y: 0, width: 1, height: 1 }, false],
    [{ x: 0.5, y: 0, width: 1, height: 1 }, false],
    [{ x: 0, y: -1, width: 1, height: 1 }, false],
    [{ x: 0, y: 0, width: 0, height: 1 }, false],
    [{ x: 0, y: 0, width: 2001, height: 2000 }, false],
  ])("checks source bounds and the four-million-pixel limit: %j", (box, valid) => {
    expect(validReferenceBox(box, box.width === 2001 ? { width: 3000, height: 3000 } : source)).toBe(valid);
  });
});

describe("来源拖框与实际裁图确认", () => {
  it("waits for displayed images and the final drag coordinates before confirming the crop", async () => {
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor();
    const image = await chooseSource();
    expect(button("生成实际裁图").disabled).toBe(true);
    fireEvent.load(image);
    const surface = screen.getByRole("group");
    Object.assign(surface, { setPointerCapture: vi.fn(), hasPointerCapture: () => true, releasePointerCapture: vi.fn() });
    vi.spyOn(image, "getBoundingClientRect").mockReturnValue({ x: 100, y: 50, left: 100, top: 50, right: 700, bottom: 550, width: 600, height: 500, toJSON: () => ({}) });
    fireEvent.pointerDown(surface, { pointerId: 7, button: 0, clientX: 150, clientY: 100 });
    fireEvent.pointerMove(surface, { pointerId: 7, clientX: 250, clientY: 200 });
    expect(button("生成实际裁图").disabled).toBe(true);
    // 抬手坐标可能晚于最后一次 move；裁图必须使用实际结束位置。
    fireEvent.pointerUp(surface, { pointerId: 7, clientX: 350, clientY: 250 });
    fireEvent.click(button("生成实际裁图"));
    await screen.findByRole("heading", { name: "核对将发送的实际图片" });
    expect(api.createReferenceCrop).toHaveBeenCalledWith("exam", "question-a", expect.objectContaining({ pixel_bbox: { x: 100, y: 100, width: 400, height: 300 } }));
    const actual = await screen.findByAltText("待确认的实际裁图");
    expect(button("已核对，加入本题").disabled).toBe(true);
    fireEvent.load(actual);
    expect(button("已核对，加入本题").disabled).toBe(false);
    fireEvent.click(button("已核对，加入本题"));
    expect(screen.getByText("题目图 1")).toBeTruthy();
  });
  it("does not allow a failed actual image to be confirmed", async () => {
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor(); await generateCrop();
    fireEvent.error(await screen.findByAltText("待确认的实际裁图"));
    expect(button("已核对，加入本题").disabled).toBe(true);
    expect(screen.getByRole("alert").textContent).toContain("图片暂不可用");
  });
  it("does not allow a source image with no visible dimensions to start selection", async () => {
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor();
    fireEvent.load(await chooseSource());
    fireEvent.pointerDown(screen.getByRole("group"), { pointerId: 7, button: 0 });
    expect(button("生成实际裁图").disabled).toBe(false);
  });
  it("prevents a third crop in either role and preserves role order in the saved payload", async () => {
    vi.mocked(api.getReferenceMaterials).mockResolvedValue({ materials: materials([crop("q1"), crop("q2"), crop("s1", "reference_solution")]) });
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); fireEvent.click(button("核对本题参考图")); await screen.findByText("已保存 3 张参考图 · 第 1 版");
    const roleSelector = screen.getByLabelText<HTMLSelectElement>("第3张图片角色");
    expect(roleSelector.querySelector<HTMLOptionElement>('option[value="question_diagram"]')?.disabled).toBe(true);
    fireEvent.change(roleSelector, { target: { value: "question_diagram" } });
    expect(roleSelector.value).toBe("reference_solution");
    await generateCrop(); fireEvent.load(await screen.findByAltText("待确认的实际裁图"));
    expect(button("已核对，加入本题").disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("待添加图片角色"), { target: { value: "reference_solution" } });
    fireEvent.click(button("已核对，加入本题")); fireEvent.click(button("保存参考图清单"));
    await waitFor(() => expect(api.saveReferenceMaterials).toHaveBeenCalled());
    expect(vi.mocked(api.saveReferenceMaterials).mock.calls[0][2].items.map(item => [item.role, item.ordinal])).toEqual([["question_diagram", 1], ["question_diagram", 2], ["reference_solution", 3], ["reference_solution", 4]]);
  });
});

describe("命令恢复与会话归属", () => {
  it("persists only the opaque ID and resumes a failed request without resending edited inputs", async () => {
    vi.mocked(api.saveReferenceMaterials).mockRejectedValueOnce(new Error("lost response"));
    const changed = vi.fn();
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable onChanged={changed} />); await openEditor();
    fireEvent.click(button("明确仅使用文字参考"));
    await screen.findByText(/操作结果尚未确认/);
    const input = vi.mocked(api.saveReferenceMaterials).mock.calls[0][2];
    expect(sessionStorage.getItem(pendingKey())).toBe(input.command_id);
    expect(sessionStorage.getItem(pendingKey())).not.toContain("items");
    vi.mocked(api.resumeReferenceCommand).mockResolvedValue({ command: { command_id: input.command_id, operation: "reference.save", target_id: "exam/question-a", status: "succeeded", result: { ...materials(), mode: "text_only", revision: 2 } } });
    vi.mocked(api.getReferenceMaterials).mockResolvedValue({ materials: { ...materials(), mode: "text_only", revision: 2 } });
    fireEvent.click(button("核对原操作结果"));
    await screen.findByText("已明确使用文字参考");
    expect(api.resumeReferenceCommand).toHaveBeenCalledWith("exam", "question-a", input.command_id);
    expect(api.saveReferenceMaterials).toHaveBeenCalledTimes(1);
    expect(sessionStorage.getItem(pendingKey())).toBeNull(); expect(changed).toHaveBeenCalledTimes(1);
  });
  it("uses current frozen authorization after a successful old save receipt", async () => {
    sessionStorage.setItem(pendingKey(), "original");
    vi.mocked(api.resumeReferenceCommand).mockResolvedValue({ command: { command_id: "original", operation: "reference.save", status: "succeeded", result: { ...materials(), mode: "text_only", revision: 2 } } });
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor();
    vi.mocked(api.getReferenceMaterials).mockResolvedValue({ materials: { ...materials(), mode: "text_only", revision: 2, frozen: true, use_state: "withdrawn" } });
    fireEvent.click(button("核对原操作结果"));
    await screen.findByText("已明确使用文字参考；已随考试冻结；已撤回使用授权");
    expect(screen.queryByRole("button", { name: "明确仅使用文字参考" })).toBeNull(); expect(sessionStorage.getItem(pendingKey())).toBeNull();
  });
  it("keeps editing hidden if the confirmed command's current-state refresh fails", async () => {
    sessionStorage.setItem(pendingKey(), "original");
    vi.mocked(api.resumeReferenceCommand).mockResolvedValue({ command: { command_id: "original", operation: "reference.save", status: "succeeded", result: materials() } });
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor();
    vi.mocked(api.getReferenceMaterials).mockRejectedValue(new Error("current state unavailable")); fireEvent.click(button("核对原操作结果"));
    await screen.findByText("原操作已确认，当前材料读取失败，请重新打开核对。");
    expect(screen.queryByRole("button", { name: "明确仅使用文字参考" })).toBeNull(); expect(sessionStorage.getItem(pendingKey())).toBeNull();
  });
  it("restores a server crop after remount and still waits for visible-image confirmation", async () => {
    sessionStorage.setItem(pendingKey(), "original-crop");
    vi.mocked(api.resumeReferenceCommand).mockResolvedValue({ command: { command_id: "original-crop", operation: "reference.crop", status: "succeeded", result: crop() } });
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor();
    fireEvent.click(button("核对原操作结果"));
    const image = await screen.findByAltText("待确认的实际裁图");
    expect(button("已核对，加入本题").disabled).toBe(true);
    fireEvent.load(image); expect(button("已核对，加入本题").disabled).toBe(false);
    expect(api.createReferenceCrop).not.toHaveBeenCalled(); expect(sessionStorage.getItem(pendingKey())).toBeNull();
  });
  it("unblocks a closed rejected command only after reading current materials", async () => {
    sessionStorage.setItem(pendingKey(), "original-save");
    vi.mocked(api.resumeReferenceCommand).mockResolvedValue({ command: { command_id: "original-save", operation: "reference.closed", status: "rejected" } });
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor();
    fireEvent.click(button("核对原操作结果"));
    await screen.findByText(/原操作未提交，旧命令已封闭/);
    expect(api.getReferenceMaterials).toHaveBeenCalledTimes(2);
    expect(sessionStorage.getItem(pendingKey())).toBeNull(); expect(button("明确仅使用文字参考").disabled).toBe(false);
  });
  it.each([
    { command_id: "other", operation: "reference.save", status: "succeeded", result: materials() },
    { command_id: "original", operation: "reference.save", target_id: "exam/other-question", status: "succeeded", result: materials() },
    { command_id: "original", operation: "reference.unexpected", status: "succeeded", result: materials() },
    { command_id: "original", operation: "reference.save", status: "processing" },
  ])("keeps pending when a receipt is unknown or does not match: %j", async command => {
    sessionStorage.setItem(pendingKey(), "original"); vi.mocked(api.resumeReferenceCommand).mockResolvedValue({ command });
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor(); fireEvent.click(button("核对原操作结果"));
    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    expect(sessionStorage.getItem(pendingKey())).toBe("original"); expect(screen.queryByRole("button", { name: "明确仅使用文字参考" })).toBeNull();
  });
  it("does not accept old recovery results after switching questions", async () => {
    sessionStorage.setItem(pendingKey(), "original"); const response = deferred<{ command: api.ReferenceCommand }>();
    vi.mocked(api.resumeReferenceCommand).mockReturnValue(response.promise); const changed = vi.fn();
    const view = render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable onChanged={changed} />); await openEditor(); fireEvent.click(button("核对原操作结果"));
    view.rerender(<ReferenceMaterialEditor examId="exam" questionId="question-b" editable onChanged={changed} />); await openEditor();
    await act(async () => response.resolve({ command: { command_id: "original", operation: "reference.crop", status: "succeeded", result: crop() } }));
    expect(screen.queryByAltText("待确认的实际裁图")).toBeNull(); expect(sessionStorage.getItem(pendingKey())).toBe("original"); expect(changed).not.toHaveBeenCalled();
  });
  it("preserves both actors' pending state when an old generation returns", async () => {
    sessionStorage.setItem(pendingKey(), "original"); const response = deferred<{ command: api.ReferenceCommand }>(); vi.mocked(api.resumeReferenceCommand).mockReturnValue(response.promise);
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor(); fireEvent.click(button("核对原操作结果"));
    sessionStorage.setItem(pendingKey("question-a", "another-teacher"), "new-command");
    await act(async () => setBusinessCommandScope("tenant", "another-teacher")); await openEditor();
    await act(async () => response.resolve({ command: { command_id: "original", operation: "reference.save", status: "succeeded", result: { ...materials(), mode: "text_only" } } }));
    expect(sessionStorage.getItem(pendingKey())).toBe("original"); expect(sessionStorage.getItem(pendingKey("question-a", "another-teacher"))).toBe("new-command");
    expect(screen.getByText("本题尚未记录材料模式")).toBeTruthy();
  });
  it("does not accept an old generation after the same actor logs out and back in", async () => {
    sessionStorage.setItem(pendingKey(), "original"); const response = deferred<{ command: api.ReferenceCommand }>(); vi.mocked(api.resumeReferenceCommand).mockReturnValue(response.promise);
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor(); fireEvent.click(button("核对原操作结果"));
    await act(async () => { setBusinessCommandScope("", ""); setBusinessCommandScope("tenant", "teacher"); }); await openEditor();
    await act(async () => response.resolve({ command: { command_id: "original", operation: "reference.crop", status: "succeeded", result: crop() } }));
    expect(sessionStorage.getItem(pendingKey())).toBe("original"); expect(screen.queryByAltText("待确认的实际裁图")).toBeNull();
  });
  it("does not create a second command during a double click", async () => {
    const response = deferred<{ materials: api.ReferenceMaterials }>(); vi.mocked(api.saveReferenceMaterials).mockReturnValue(response.promise);
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor();
    const save = button("明确仅使用文字参考"); fireEvent.click(save); fireEvent.click(save);
    expect(api.saveReferenceMaterials).toHaveBeenCalledTimes(1);
    await act(async () => response.resolve({ materials: { ...materials(), mode: "text_only" } }));
  });
  it("does not overwrite a command created by another editor for the same question", async () => {
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor();
    sessionStorage.setItem(pendingKey(), "another-editor-command"); fireEvent.click(button("明确仅使用文字参考"));
    expect(api.saveReferenceMaterials).not.toHaveBeenCalled(); expect(sessionStorage.getItem(pendingKey())).toBe("another-editor-command");
    expect(screen.getByText("本题已有待确认操作，请先核对原操作结果。")).toBeTruthy();
  });
  it("keeps read-only materials visible without fetching source pages or offering writes", async () => {
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable={false} />); await openEditor();
    expect(api.getReferenceSources).not.toHaveBeenCalled(); expect(screen.queryByRole("button", { name: "明确仅使用文字参考" })).toBeNull();
  });
  it("does not POST a pending recovery from a read-only editor", async () => {
    sessionStorage.setItem(pendingKey(), "original");
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable={false} />); await openEditor();
    expect(button("核对原操作结果").disabled).toBe(true); fireEvent.click(button("核对原操作结果")); expect(api.resumeReferenceCommand).not.toHaveBeenCalled();
  });
  it("does not POST a pending recovery before business session scope is known", async () => {
    setBusinessCommandScope("", ""); sessionStorage.setItem("reference-material-command::exam:question-a", "original");
    render(<ReferenceMaterialEditor examId="exam" questionId="question-a" editable />); await openEditor();
    expect(button("核对原操作结果").disabled).toBe(true); fireEvent.click(button("核对原操作结果")); expect(api.resumeReferenceCommand).not.toHaveBeenCalled();
  });
});

describe("受保护图片的会话和资源生命周期", () => {
  it("discards late blobs and revokes displayed object URLs when identity changes", async () => {
    const old = deferred<{ blob: Blob }>(); const ready = vi.fn();
    const view = render(<ProtectedReferenceImage identity="old-question" alt="合成图" load={() => old.promise} onReadyChange={ready} />);
    view.rerender(<ProtectedReferenceImage identity="new-question" alt="合成图" load={async () => blobResult()} onReadyChange={ready} />);
    const image = await screen.findByAltText("合成图"); fireEvent.load(image);
    expect(ready).toHaveBeenLastCalledWith(true);
    await act(async () => old.resolve(blobResult()));
    expect(URL.createObjectURL).toHaveBeenCalledTimes(1);
    const displayed = image.getAttribute("src"); view.unmount(); expect(URL.revokeObjectURL).toHaveBeenCalledWith(displayed);
  });
});
