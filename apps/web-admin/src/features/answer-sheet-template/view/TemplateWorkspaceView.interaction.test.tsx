// @vitest-environment jsdom
import { act, useRef, useState, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { answerQuestion, answerRegion, answerSource, answerTemplate } from "../testing/answerSheetFixtures";
import { TemplateBindingPanel, TemplateCanvas, TemplateSettingsDrawer, TemplateToolbar } from "./TemplateWorkspaceView";
import type { TemplatePageViewState } from "./templateViewTypes";

vi.mock("./OMRCalibrationView", () => ({ OMRProfilePanel: () => <div data-omr-panel />, CalibrationPanel: () => null }));
vi.mock("./StudentPrintingView", () => ({ StudentPrintPanel: () => null }));
vi.mock("./ClearTemplateQuestionRegionsButton", () => ({ ClearTemplateQuestionRegionsButton: () => null }));
vi.mock("antd", async () => {
  const { MockTemplateSourceDropdown, MockTemplatePopover } = await import("../testing/MockTemplateSourceDropdown");
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    Alert: Box, Checkbox: Box, Dropdown: { Button: MockTemplateSourceDropdown }, Input: Box, InputNumber: Box, List: Box, Progress: Box, Space: Box, Spin: Box, Tag: Box, Upload: Box,
    Popover: MockTemplatePopover,
    Drawer: ({ open, children, title, onClose }: { open: boolean; children: ReactNode; title: string; onClose: () => void }) => open ? <section role="dialog" aria-label={title}>{children}<button onClick={onClose}>关闭模板设置</button></section> : null,
    Button: ({ children, icon, disabled, onClick, ...props }: { children?: ReactNode; icon?: ReactNode; disabled?: boolean; onClick?: () => void } & Record<string, unknown>) =>
      <button disabled={disabled} onClick={onClick} aria-label={props["aria-label"] as string} aria-pressed={props["aria-pressed"] as boolean}>{icon}{children}</button>,
    Select: ({ value, onChange, options, ...props }: { value: string | number; onChange: (value: string) => void; options: Array<{ value: string; label: string }> } & Record<string, unknown>) =>
      <select aria-label={props["aria-label"] as string} value={value} onChange={event => onChange(event.target.value)}>{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>
  };
});

describe("answer-sheet workbench interaction", () => {
  let root: Root, host: HTMLDivElement, view: TemplatePageViewState;
  const save = vi.fn(), cancel = vi.fn(), move = vi.fn(), up = vi.fn(), begin = vi.fn(), update = vi.fn(), remove = vi.fn(), createIdentity = vi.fn(), deleteSource = vi.fn(), openGrid = vi.fn();
  const objectiveBlock = { id: "objective-block", label: "选择题区域", x: .1, y: .3, width: .7, height: .2 };
  const discardObjectiveGroup = vi.fn();
  function Probe({ withIdentity = false, readonly = false, withObjective = false, automatic = false } = {}) {
    const canvasRef = useRef<HTMLDivElement>(null);
    const [zoom, setZoom] = useState(1);
    const [settingsOpen, setSettingsOpen] = useState(false);
    const [selectedRegionId, setSelectedRegionId] = useState(answerRegion.id);
    const identity = { id: "identity-name", label: "姓名", x: .2, y: .1, width: .2, height: .06 };
    const page = { ...answerTemplate.layout.pages[0], question_regions: [{ ...answerRegion, height: .04,
      ...(automatic ? { origin: "model" as const, review_state: "suggested" as const, semantic_check: "supported" as const } : {}) }], identity_regions: withIdentity ? [identity] : [] };
    const selectedRegion = [...page.question_regions, ...page.identity_regions].find(region => region.id === selectedRegionId);
    view = {
      papers: [answerSource], questions: [answerQuestion], selectedPaperId: answerSource.id, selectedTemplate: answerTemplate,
      canManage: true, canEditSources: true, templateDirty: false,
      settingsOpen, setSettingsOpen,
      uploadProps: {}, sourceUpload: { uploading: false }, binding: { bindingBusy: false }, printing: { printBusy: false },
      sourceManagement: { deleting: false, canDelete: !readonly, removeCurrent: deleteSource },
      previewController: { pageNo: 1, setPageNo: vi.fn(), preview: { sourceId: answerSource.id, loading: false, pageCount: 1, width: 100, height: 200, imageUrl: "answer.png" } },
      editor: { layout: { ...answerTemplate.layout, pages: [page] }, currentPage: page, name: answerTemplate.name,
        selectedRegionId, selectedRegion, selectedRegionKind: selectedRegion ? selectedRegion.id === identity.id ? "identity" : "question" : undefined,
        selectedQuestionId: answerQuestion.id, selectedQuestion: selectedRegion?.id === identity.id ? undefined : answerQuestion, coveredQuestions: new Set([answerQuestion.id]),
        setSelectedRegionId, setSelectedQuestionId: vi.fn(), readonly, canvasRef, zoom, setZoom, unassignedRegions: new Map(),
        objectiveGroups: withObjective ? new Map([[1, [objectiveBlock]]]) : new Map(), discardObjectiveGroup,
        updateRegion: update, removeRegion: remove, createIdentityRegion: createIdentity,
        objectiveGrid: { openGrid },
        saving: false, suggestingRegions: false, cancelInteraction: cancel, saveDraft: save, onCanvasPointerMove: move,
        onCanvasPointerUp: up, beginRegionInteraction: (event: { stopPropagation: () => void }, region: { id: string }, ...args: unknown[]) => {
          event.stopPropagation(); begin(event, region, ...args); setSelectedRegionId(region.id);
        }, discardSuggestedRegion: vi.fn() }
    } as unknown as TemplatePageViewState;
    return <div className="template-editor-page"><TemplateToolbar view={view} /><TemplateCanvas view={view} /><TemplateSettingsDrawer view={view} /><input aria-label="template name" /></div>;
  }
  beforeEach(async () => {
    vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    await act(async () => root.render(<Probe />));
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  const button = (label: string) => host.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)!;
  const openSettings = async () => {
    await act(async () => button("更多操作").click());
    await act(async () => button("模板设置").click());
  };
  const key = (target: EventTarget, type: string, options: KeyboardEventInit) => {
    const event = new KeyboardEvent(type, { bubbles: true, cancelable: true, ...options }); target.dispatchEvent(event); return event;
  };
  const pointer = (target: Element, type: string) => {
    const event = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientX: 60, clientY: 70 });
    Object.defineProperty(event, "pointerId", { value: 1 });
    return target.dispatchEvent(event);
  };

  it("moves and finishes a selected region in select mode and exposes only selected resize handles", async () => {
    await act(async () => button("选择区域").click());
    const region = host.querySelector("[data-region-id]")!;
    await act(async () => pointer(region, "pointerdown"));
    expect(begin).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ id: answerRegion.id }), "move");
    await act(async () => pointer(view.editor.canvasRef.current!, "pointermove"));
    await act(async () => pointer(view.editor.canvasRef.current!, "pointerup"));
    expect(move).toHaveBeenCalledOnce(); expect(up).toHaveBeenCalledOnce();
    expect(host.querySelectorAll(".template-resize-handle")).toHaveLength(8);
    const visibility = host.querySelector<HTMLSelectElement>('select[aria-label="显示题区标注"]')!;
    await act(async () => { visibility.value = "hidden"; visibility.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(host.querySelector("[data-region-id]")).toBeNull();
  });

  it("shows successful automatic regions as editable without requiring per-region review controls", async () => {
    await act(async () => root.render(<Probe automatic />));
    expect(host.querySelector('[data-region-kind="question"]')).not.toBeNull();
    expect(host.textContent).not.toContain("确认本页");
    await openSettings();
    expect(host.textContent).not.toContain("确认此区域");
    expect(host.textContent).not.toContain("确认此异常区域");
    await act(async () => pointer(host.querySelector('[data-region-kind="question"]')!, "pointerdown"));
    expect(begin).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ id: answerRegion.id }), "move");
  });

  it.each([
    { containsNumber: true, selected: false }, { containsNumber: true, selected: true },
    { containsNumber: false, selected: false }, { containsNumber: false, selected: true }
  ])("preserves printed choice content without a duplicate blue number: %j", async ({ containsNumber, selected }) => {
    // 同时覆盖含题号和只含选项的实测框；隐藏重复标签不能改变保存的题区或选项坐标。
    const choice = { ...answerRegion, x: containsNumber ? .1 : .12, width: containsNumber ? .3 : .28,
      option_regions: [{ id: "option-a", label: "A", x: containsNumber ? .1 : 0, y: .2, width: .1, height: .3 }] };
    const other = { ...answerRegion, id: "subjective", question_id: "q12", label: "12", x: .6 };
    const page = { ...view.editor.currentPage!, question_regions: [choice, other] };
    const updated = { ...view, editor: { ...view.editor, currentPage: page, layout: { pages: [page] },
      selectedRegionId: selected ? choice.id : "" } };
    await act(async () => root.render(<TemplateCanvas view={updated} />));
    const outline = host.querySelector<HTMLElement>(`[data-region-id="${choice.id}"]`)!;
    expect(outline.querySelector(":scope > span")).toBeNull();
    expect(outline.getAttribute("aria-label")).toBe("第 1 题区域");
    expect(outline.title).toBe("第 1 题区域");
    expect(outline.style.left).toBe(containsNumber ? "10%" : "12%");
    expect(parseFloat(outline.style.width)).toBeCloseTo(containsNumber ? 30 : 28);
    expect(outline.classList.contains("selected")).toBe(selected);
    expect(outline.querySelector<HTMLElement>(".template-option-region")!.style.left).toBe(containsNumber ? "10%" : "0%");
    expect(host.querySelector('[data-region-id="subjective"] > span')?.textContent).toBe("12");
    expect(updated.editor.layout.pages[0].question_regions).toEqual([choice, other]);
  });

  it("shows one shared fill area while retaining three question ROIs and exposes the grading mode switch", async () => {
    const official = [12, 13, 14].map(number => ({ ...answerQuestion, id: `q${number}`, question_no: `${number}`, question_type: "fill_blank" }));
    const box = { x: .1, y: .2, width: .8, height: .2 };
    const rows = official.map((question, index) => ({ ...answerRegion, id: `fill-${index}`, question_id: question.id,
      label: question.question_no, x: .12 + index * .23, y: .22, width: .18, height: .1,
      grading_group_id: "fill-section", grading_bbox: box }));
    const page = { ...view.editor.currentPage!, question_regions: rows };
    const setMode = vi.fn();
    const grouped: TemplatePageViewState = { ...view, questions: official, editor: { ...view.editor,
      layout: { pages: [page] }, currentPage: page, selectedRegion: rows[0], selectedRegionId: rows[0].id,
      selectedQuestionId: official[0].id, selectedQuestion: official[0],
      fillGradingGroup: { mode: "shared", questionLabels: ["12", "13", "14"], missingQuestionLabels: [], canShare: true, bbox: box,
        questionIds: official.map(question => question.id), options: rows.map(region => ({ value: region.question_id, label: region.label, bbox: region })) },
      setFillGradingMode: setMode } };
    await act(async () => root.render(<TemplateCanvas view={grouped} />));
    const outline = host.querySelector<HTMLElement>('[data-grading-group="fill-section"]')!;
    expect(host.querySelectorAll('[data-region-kind="question"]')).toHaveLength(1);
    expect(outline.style.width).toBe("80%");
    expect(outline.textContent).toBe("12、13、14");
    expect(grouped.editor.layout.pages[0].question_regions).toHaveLength(3);
    await act(async () => pointer(outline, "pointerdown"));
    expect(begin).not.toHaveBeenCalled();
    expect(host.querySelectorAll(".template-resize-handle")).toHaveLength(0);
    const mode = host.querySelector<HTMLSelectElement>('select[aria-label="填空题批阅方式"]')!;
    await act(async () => { mode.value = "individual"; mode.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(setMode).toHaveBeenCalledWith("individual");
  });

  it("temporarily pans with Space, restores the previous tool, and leaves input and dialog keys alone", async () => {
    const viewport = host.querySelector<HTMLDivElement>(".template-canvas-scroll")!;
    const outside = await act(async () => key(document.body, "keydown", { key: " ", code: "Space" }));
    expect(outside.defaultPrevented).toBe(false); expect(cancel).not.toHaveBeenCalled();
    const down = await act(async () => key(viewport, "keydown", { key: " ", code: "Space" }));
    expect(down.defaultPrevented).toBe(true); expect(button("拖动画布").getAttribute("aria-pressed")).toBe("true");
    await act(async () => key(document.body, "keyup", { key: " ", code: "Space" }));
    expect(button("选择区域").getAttribute("aria-pressed")).toBe("true");
    const input = host.querySelector("input")!;
    const typing = await act(async () => key(input, "keydown", { key: " ", code: "Space" }));
    expect(typing.defaultPrevented).toBe(false); expect(button("拖动画布").getAttribute("aria-pressed")).toBe("false");
    const dialog = document.createElement("div"); dialog.setAttribute("role", "dialog"); document.body.append(dialog);
    const escape = await act(async () => key(dialog, "keydown", { key: "Escape" }));
    expect(escape.defaultPrevented).toBe(false); dialog.remove();
    cancel.mockClear();
    const outsideEscape = await act(async () => key(document.body, "keydown", { key: "Escape" }));
    expect(outsideEscape.defaultPrevented).toBe(false); expect(cancel).not.toHaveBeenCalled();
    await act(async () => key(viewport, "keydown", { key: "Escape" }));
    expect(cancel).toHaveBeenCalledOnce(); expect(button("选择区域").getAttribute("aria-pressed")).toBe("true");
  });

  it("saves with Ctrl+S even from a text field but never submits the background editor from a dialog", async () => {
    const event = await act(async () => key(host.querySelector("input")!, "keydown", { key: "s", ctrlKey: true }));
    expect(event.defaultPrevented).toBe(true); expect(save).toHaveBeenCalledOnce();
    const outside = await act(async () => key(document.body, "keydown", { key: "s", ctrlKey: true }));
    expect(outside.defaultPrevented).toBe(false); expect(save).toHaveBeenCalledOnce();
    await act(async () => key(host.querySelector("input")!, "keydown", { key: "s", ctrlKey: true, repeat: true }));
    expect(save).toHaveBeenCalledOnce();
    const dialog = document.createElement("div"); dialog.setAttribute("role", "dialog"); document.body.append(dialog);
    await act(async () => key(dialog, "keydown", { key: "s", ctrlKey: true }));
    expect(save).toHaveBeenCalledOnce(); dialog.remove();
  });

  it("keeps pointer interaction keyboard focus inside the canvas", async () => {
    await act(async () => pointer(view.editor.canvasRef.current!, "pointerdown"));
    expect(document.activeElement).toBe(host.querySelector(".template-canvas-scroll"));
    expect(host.querySelector('button[aria-label="打开全屏工作台"]')).toBeNull();
    expect(host.querySelector('button[aria-label="返回考试管理"]')).toBeNull();
    expect(host.querySelector('button[aria-label="切换题目列表"]')).toBeNull();
    expect(host.querySelector('button[aria-label="切换属性面板"]')).toBeNull();
  });

  it("keeps one upload action and places the delete icon immediately after the current filename", async () => {
    const uploads = [...host.querySelectorAll("button")].filter(item => item.textContent?.includes("上传答题卡"));
    expect(uploads).toHaveLength(1);
    expect(host.querySelector('[role="menuitem"]')).toBeNull();
    expect(host.querySelector('[aria-label="答题卡文件操作"]')).toBeNull();
    const deletion = button("删除当前文件");
    expect(deletion.closest(".template-source-file")).not.toBeNull();
    expect(deletion.previousElementSibling?.getAttribute("aria-label")).toBe("答题卡文件");
    await act(async () => deletion.click());
    expect(deleteSource).toHaveBeenCalledOnce();
    expect(host.textContent).not.toContain("合并答题卡");
  });

  it("disables current-file deletion when the source controller refuses it", async () => {
    await act(async () => root.render(<Probe readonly />));
    expect(button("删除当前文件").disabled).toBe(true);
    expect(deleteSource).not.toHaveBeenCalled();
  });

  it("opens settings and identity tools only on explicit requests, and starts choice recognition directly", async () => {
    expect(host.querySelector(".template-settings-content")).toBeNull();
    expect(button("模板设置")).toBeNull();
    expect(button("撤销").textContent).toBe("");
    expect(button("重做").textContent).toBe("");
    expect(host.querySelector('[aria-label="个人信息区域"]')).toBeNull();
    await act(async () => pointer(host.querySelector('[data-region-kind="question"]')!, "pointerdown"));
    expect(host.querySelector(".template-settings-content")).toBeNull();
    await act(async () => button("识别选择题").click());
    expect(openGrid).toHaveBeenCalledOnce();
    expect(view.settingsOpen).toBe(false);
    await act(async () => button("个人信息").click());
    expect(host.querySelector('[aria-label="个人信息区域"]')).not.toBeNull();
    expect(view.settingsOpen).toBe(false);
    await openSettings();
    expect(host.querySelector(".template-more-actions")).toBeNull();
    expect(host.querySelector('[role="dialog"][aria-label="模板设置"]')).not.toBeNull();
    expect(host.querySelector("#template-exam-binding")).not.toBeNull();
    expect(host.querySelector(".template-region-facts")).toBeNull();
    const close = [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === "关闭模板设置")!;
    await act(async () => close.click());
    expect(host.querySelector(".template-settings-content")).toBeNull();
  });

  it("shows a choice block as one large region and opens review with its complete page coordinates", async () => {
    await act(async () => root.render(<Probe withObjective />));
    const block = button("选择题区域 1，点击核对题号和选项");
    expect(block.classList.contains("template-objective-group")).toBe(true);
    expect(block.style.left).toBe("10%"); expect(block.style.top).toBe("30%");
    expect(block.style.width).toBe("70%"); expect(block.style.height).toBe("20%");
    expect(host.querySelectorAll(".template-objective-group")).toHaveLength(1);
    expect(host.querySelector('[aria-label="未关联区域操作"]')).toBeNull();
    await act(async () => pointer(block, "pointerdown"));
    expect(begin).not.toHaveBeenCalled();
    await act(async () => block.click());
    expect(openGrid).toHaveBeenCalledWith(objectiveBlock);
    const discard = [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === "丢弃此区域")!;
    await act(async () => discard.click()); expect(discardObjectiveGroup).toHaveBeenCalledWith(objectiveBlock.id);
  });

  it("keeps a readonly choice block visible without allowing new review", async () => {
    await act(async () => root.render(<Probe withObjective readonly />));
    const block = button("选择题区域 1，点击核对题号和选项");
    expect(block.disabled).toBe(true); await act(async () => block.click()); expect(openGrid).not.toHaveBeenCalled();
  });

  it("explains pending choice review and disables final confirmation even when every question has a region", async () => {
    const pendingView = { ...view, editor: { ...view.editor, objectiveGroups: new Map([[1, [objectiveBlock]]]) } };
    await act(async () => root.render(<TemplateBindingPanel view={pendingView} />));
    expect(host.textContent).toContain("选择题区域尚未核对，请点击大框核对题号和选项。");
    const confirm = [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === "确认答题卡设置")!;
    expect(confirm.disabled).toBe(true);
  });

  it("fits a small question on double click without changing its saved normalized coordinates", async () => {
    const viewport = host.querySelector<HTMLDivElement>(".template-canvas-scroll")!;
    Object.defineProperties(viewport, { clientWidth: { value: 600 }, clientHeight: { value: 400 } });
    const original = structuredClone(view.editor.currentPage!.question_regions[0]);
    await act(async () => host.querySelector("[data-region-id]")!.dispatchEvent(new MouseEvent("dblclick", { bubbles: true })));
    expect(view.editor.zoom).toBeGreaterThan(2); expect(view.editor.zoom).toBeLessThanOrEqual(4);
    expect(view.editor.currentPage!.question_regions[0]).toEqual(original);
  });

  it("selects identity boxes separately, exposes their resize handles, and hides question-only controls", async () => {
    await act(async () => root.render(<Probe withIdentity />));
    const identity = host.querySelector('[data-region-kind="identity"]')!;
    expect(identity.getAttribute("aria-label")).toBe("姓名区域");
    expect(identity.textContent).not.toContain("题");
    await act(async () => pointer(identity, "pointerdown"));
    expect(begin).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ id: "identity-name" }), "move");
    expect(identity.querySelectorAll(".template-resize-handle")).toHaveLength(8);
    expect(host.querySelector('[data-region-kind="question"]')!.querySelector(".template-resize-handle")).toBeNull();
    await act(async () => pointer(button("调整姓名区域左上角"), "pointerdown"));
    expect(begin).toHaveBeenLastCalledWith(expect.anything(), expect.objectContaining({ id: "identity-name" }), "resize", "nw");
    expect(host.querySelector("[data-omr-panel]")).toBeNull();
    expect(host.querySelector(".template-advanced-options")).toBeNull();
    expect(view.editor.coveredQuestions.size).toBe(1);
  });

  it("keeps only the selected identity in current-region mode and hides it with all annotations", async () => {
    await act(async () => root.render(<Probe withIdentity />));
    await act(async () => button("个人信息").click());
    await act(async () => button("定位第 1 页姓名区域").click());
    const visibility = host.querySelector<HTMLSelectElement>('select[aria-label="显示题区标注"]')!;
    await act(async () => { visibility.value = "current"; visibility.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(host.querySelectorAll("[data-region-id]")).toHaveLength(1);
    expect(host.querySelector('[data-region-kind="identity"]')).not.toBeNull();
    await act(async () => { visibility.value = "hidden"; visibility.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(host.querySelector("[data-region-id]")).toBeNull();
  });

  it("offers fixed identity field names for manual creation and changes while preserving question coverage", async () => {
    await act(async () => root.render(<Probe withIdentity />));
    await act(async () => button("个人信息").click());
    const field = host.querySelector<HTMLSelectElement>('select[aria-label="新增个人信息字段"]')!;
    await act(async () => { field.value = "准考证号"; field.dispatchEvent(new Event("change", { bubbles: true })); });
    await act(async () => button("添加个人信息区域").click());
    expect(createIdentity).toHaveBeenCalledWith("准考证号");
    await act(async () => button("定位第 1 页姓名区域").click());
    const label = host.querySelector<HTMLSelectElement>('select[aria-label="个人信息字段类型"]')!;
    await act(async () => { label.value = "学号"; label.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(update).toHaveBeenCalledWith("identity-name", { label: "学号" });
    await act(async () => button("删除第 1 页姓名区域").click());
    expect(remove).toHaveBeenCalledWith("identity-name");
    expect(view.editor.coveredQuestions.size).toBe(1);
  });

  it("keeps identity boxes selectable in a locked template without mutation handles", async () => {
    await act(async () => root.render(<Probe withIdentity readonly />));
    const identity = host.querySelector('[data-region-kind="identity"]')!;
    await act(async () => pointer(identity, "pointerdown"));
    expect(view.editor.selectedRegionKind).toBe("identity");
    expect(begin).not.toHaveBeenCalled(); expect(identity.querySelector(".template-resize-handle")).toBeNull();
    await act(async () => button("个人信息").click());
    expect(button("添加个人信息区域").disabled).toBe(true);
    expect(button("删除第 1 页姓名区域").disabled).toBe(true);
  });

  it("zooms to an identity field on double click without changing template coordinates", async () => {
    await act(async () => root.render(<Probe withIdentity />));
    const viewport = host.querySelector<HTMLDivElement>(".template-canvas-scroll")!;
    Object.defineProperties(viewport, { clientWidth: { value: 600 }, clientHeight: { value: 400 } });
    const original = structuredClone(view.editor.currentPage!.identity_regions[0]);
    await act(async () => host.querySelector('[data-region-kind="identity"]')!.dispatchEvent(new MouseEvent("dblclick", { bubbles: true })));
    expect(view.editor.selectedRegionKind).toBe("identity");
    expect(view.editor.zoom).toBeGreaterThan(2); expect(view.editor.zoom).toBeLessThanOrEqual(4);
    expect(view.editor.currentPage!.identity_regions[0]).toEqual(original);
    expect(update).not.toHaveBeenCalled();
  });
});
