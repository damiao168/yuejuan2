// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CapturePage, MatchingQueue, MatchingSubmission } from "../../api/capture";
import { MatchingWorkspace } from "./MatchingWorkspace";

const mocks = vi.hoisted(() => ({ confirm: vi.fn() }));

vi.mock("../../components/StatusTag", () => ({
  StatusTag: ({ tone, children }: { tone: string; children: ReactNode }) => <span data-tone={tone}>{children}</span>,
}));
vi.mock("antd", () => {
  const Wrap = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) },
    Alert: ({ type, message, action }: { type: string; message: ReactNode; action?: ReactNode }) => <div data-alert={type}>{message}{action}</div>,
    Button: ({ children, onClick, disabled, loading, "aria-label": label }: {
      children?: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean; "aria-label"?: string;
    }) => <button type="button" onClick={onClick} disabled={disabled} data-loading={String(Boolean(loading))} aria-label={label}>{children}</button>,
    Empty: ({ description }: { description: ReactNode }) => <div>{description}</div>,
    InputNumber: ({ value, disabled, onChange, onPressEnter, "aria-label": label }: {
      value?: number | null; disabled?: boolean; onChange: (value: number | null) => void; onPressEnter: () => void; "aria-label"?: string;
    }) => <input type="number" value={value ?? ""} disabled={disabled} aria-label={label} onChange={event => onChange(event.target.value === "" ? null : Number(event.target.value))} onKeyDown={event => { if (event.key === "Enter") onPressEnter(); }} />,
    Select: ({ value, onChange, options, placeholder, disabled }: {
      value?: string; onChange: (value: string) => void; options: { value: string; label: string }[];
      placeholder: string; disabled?: boolean;
    }) => <select value={value ?? ""} onChange={(event) => onChange(event.currentTarget.value)} aria-label={placeholder} disabled={disabled}>
      <option value="" />
      {options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
    </select>,
    Space: Wrap,
  };
});

function submission(overrides: Partial<MatchingSubmission> = {}): MatchingSubmission {
  return { id: "submission-38", identity_status: "unassigned", identity_revision: 11, identity_evidence: {}, pages: [], ...overrides };
}

function queue(submissions: MatchingSubmission[]): MatchingQueue {
  return {
    batch_id: "batch-38", exam_id: "exam-38", submissions,
    candidates: [
      { id: "shared", name: "当前学生", student_no: "0381", class_id: "class-38", class_name: "一班" },
      { id: "free", name: "另选学生", student_no: "0382", class_id: "class-38", class_name: "一班" },
      { id: "taken", name: "其他已匹配学生", student_no: "0383", class_id: "class-38", class_name: "一班" },
    ],
  };
}

function page(id: string, sequenceNo: number, assignedPageNo?: number): CapturePage {
  return { id, capture_batch_id: "batch-38", capture_file_id: "file-38", source_index: sequenceNo,
    sequence_no: sequenceNo, assigned_page_no: assignedPageNo, rotation_degrees: 0, decoded_file_asset_id: `asset-${id}`,
    status: "ready", revision: 7, page_identity: {}, match_candidates: [] };
}

describe("匹配工作区身份展示与答卷选择", () => {
  let host: HTMLDivElement;
  let root: Root;
  const onConfirmStudent = vi.fn().mockResolvedValue(undefined);
  const onUnknown = vi.fn().mockResolvedValue(undefined);
  const props = {
    canManage: true, actioning: false, onPreview: vi.fn().mockResolvedValue(undefined),
    onConfirmStudent, onUnknown, onConfirmPage: vi.fn().mockResolvedValue(undefined),
  };

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  async function renderWorkspace(current: MatchingQueue) {
    await act(async () => root.render(<MatchingWorkspace {...props} queue={current} />));
  }

  it.each([
    ["matched", "success", "已匹配"],
    ["unknown", "danger", "未知"],
    ["unassigned", "warning", "待确认"],
    ["conflict", "warning", "待确认"],
    ["constructor", "warning", "待确认"],
  ] as const)("身份状态 %s 保留 %s 和 %s", async (status, tone, label) => {
    const current = queue([submission({ identity_status: status as MatchingSubmission["identity_status"] })]);
    await renderWorkspace(current);
    const row = host.querySelector(".matching-submissions button");
    expect(row?.querySelector("strong")?.textContent).toBe("答卷 1");
    expect(row?.querySelector("[data-tone]")?.getAttribute("data-tone")).toBe(tone);
    expect(row?.querySelector("[data-tone]")?.textContent).toBe(label);
  });

  it("答卷标签优先显示名册身份，再回退识别学号和未确认提示", async () => {
    await renderWorkspace(queue([
      submission({ id: "known", student_id: "shared", candidate_no: "9999" }),
      submission({ id: "candidate", candidate_no: "0389" }),
      submission({ id: "missing" }),
    ]));
    const rows = host.querySelectorAll(".matching-submissions button");
    expect(rows[0].textContent).toContain("当前学生 · 0381 · 0 页");
    expect(rows[0].textContent).not.toContain("9999");
    expect(rows[1].textContent).toContain("识别学号 0389（待确认） · 0 页");
    expect(rows[2].textContent).toContain("尚未确认学生 · 0 页");
  });

  it("按正确答题卡页码排列，已确认页只显示一个页码并提供图片入口", async () => {
    const scannedFirst = page("scanned-first", 1, 2);
    const scannedSecond = page("scanned-second", 2, 1);
    const current = queue([submission({ pages: [scannedFirst, scannedSecond] })]);
    await renderWorkspace(current);
    const rows = [...host.querySelectorAll<HTMLElement>(".matching-page-row")];
    expect(rows.map(row => row.dataset.pageId)).toEqual(["scanned-second", "scanned-first"]);
    expect(rows.map(row => row.querySelector("strong")?.textContent)).toEqual(["第 1 页", "第 2 页"]);
    expect(host.querySelectorAll(".matching-page-row input")).toHaveLength(0);
    expect(rows.map(row => row.querySelector("button")?.textContent)).toEqual(["查看图片", "查看图片"]);
    await act(async () => rows[0].querySelector<HTMLButtonElement>("button")!.click());
    expect(props.onPreview).toHaveBeenCalledExactlyOnceWith(scannedSecond);
    expect(current.submissions[0].pages).toEqual([scannedFirst, scannedSecond]);
  });

  it("未确认页排在确认页后，不以扫描序号冒充正确页码，确认后去除输入并重新排序", async () => {
    const waiting = page("waiting", 1);
    const assigned = page("assigned", 8, 2);
    await renderWorkspace(queue([submission({ pages: [waiting, assigned] })]));
    const rows = [...host.querySelectorAll<HTMLElement>(".matching-page-row")];
    expect(rows.map(row => row.dataset.pageId)).toEqual(["assigned", "waiting"]);
    expect(rows[1].textContent).toContain("页码待确认");
    expect(rows[1].textContent).not.toContain("第 1 页");
    const input = rows[1].querySelector<HTMLInputElement>('input[aria-label="设置答卷页码"]')!;
    const confirm = [...rows[1].querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === "确认页码")!;
    expect(confirm.disabled).toBe(true);
    await act(async () => fireEvent.change(input, { target: { value: "1.5" } }));
    expect(confirm.disabled).toBe(true);
    await act(async () => fireEvent.change(input, { target: { value: "1" } }));
    await act(async () => confirm.click());
    expect(props.onConfirmPage).toHaveBeenCalledExactlyOnceWith(waiting, 1);
    await renderWorkspace(queue([submission({ pages: [assigned, { ...waiting, assigned_page_no: 1, revision: 8 }] })]));
    expect([...host.querySelectorAll<HTMLElement>(".matching-page-row")].map(row => row.dataset.pageId)).toEqual(["waiting", "assigned"]);
    expect(host.querySelector(".matching-page-row input")).toBeNull();
  });

  it.each([{ canManage: false, actioning: false }, { canManage: true, actioning: true }])("%j 禁止设置待确认页码，图片仍可查看", async state => {
    const waiting = page("waiting", 17, 0);
    const current = queue([submission({ pages: [waiting] })]);
    await act(async () => root.render(<MatchingWorkspace {...props} {...state} queue={current} />));
    expect(host.querySelector<HTMLInputElement>('input[aria-label="设置答卷页码"]')?.disabled).toBe(true);
    const buttons = [...host.querySelectorAll<HTMLButtonElement>(".matching-page-row button")];
    expect(buttons.find(button => button.textContent === "确认页码")?.disabled).toBe(true);
    await act(async () => buttons.find(button => button.textContent === "查看图片")!.click());
    expect(props.onPreview).toHaveBeenCalledWith(waiting);
    expect(props.onConfirmPage).not.toHaveBeenCalled();
  });

  it("默认分配页码仍须确认，视觉或人工识别完成的页面无需重复输入", async () => {
    const defaults = { ...page("default", 1, 2), status: "quality_checking" };
    const visual = { ...page("visual", 2, 1), status: "normalized", page_identity: { page_no_source: "visual_template_match" } };
    const manual = { ...page("manual", 3, 3), status: "normalized", page_identity: { barcode_status: "manual_override" } };
    await renderWorkspace(queue([submission({ pages: [defaults, visual, manual] })]));
    expect(host.querySelector('[data-page-id="visual"] input')).toBeNull();
    expect(host.querySelector('[data-page-id="manual"] input')).toBeNull();
    const defaultRow = host.querySelector('[data-page-id="default"]')!;
    expect(defaultRow.textContent).toContain("页码待确认");
    expect(defaultRow.querySelector<HTMLInputElement>("input")?.value).toBe("2");
    await act(async () => [...defaultRow.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === "确认页码")!.click());
    expect(props.onConfirmPage).toHaveBeenCalledExactlyOnceWith(defaults, 2);
  });

  it("确认页默认没有输入，明确调整后可更正；显示排序不改变拆分对象", async () => {
    const firstScan = page("first-scan", 1, 2);
    const secondScan = page("second-scan", 2, 1);
    const split = vi.fn();
    await act(async () => root.render(<MatchingWorkspace {...props} queue={queue([submission({ pages: [firstScan, secondScan] })])} onSplit={split} />));
    const secondRow = host.querySelector('[data-page-id="second-scan"]')!;
    const firstRow = host.querySelector('[data-page-id="first-scan"]')!;
    expect(firstRow.querySelector('[aria-label="拆分为新答卷"]')).toBeNull();
    await act(async () => secondRow.querySelector<HTMLButtonElement>('[aria-label="拆分为新答卷"]')!.click());
    expect(split).toHaveBeenCalledExactlyOnceWith("submission-38", "second-scan");
    await act(async () => [...secondRow.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === "调整页序")!.click());
    const input = secondRow.querySelector<HTMLInputElement>("input")!;
    await act(async () => fireEvent.change(input, { target: { value: "3" } }));
    await act(async () => fireEvent.keyDown(input, { key: "Enter" }));
    expect(props.onConfirmPage).toHaveBeenCalledExactlyOnceWith(secondScan, 3);
    await act(async () => [...secondRow.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === "取消调整")!.click());
    expect(secondRow.querySelector("input")).toBeNull();
  });

  it("切换同一学生的答卷会清理手动候选，并提交当前答卷 revision", async () => {
    const current = queue([
      submission({ id: "source", student_id: "shared" }),
      submission({ id: "target", student_id: "shared", identity_status: "matched", identity_revision: 23 }),
      submission({ id: "occupied", student_id: "taken", identity_status: "matched" }),
    ]);
    await renderWorkspace(current);
    const select = host.querySelector<HTMLSelectElement>('select[aria-label="按姓名或学号查找"]')!;
    expect([...select.options].map((option) => option.value)).toEqual(["", "shared", "free"]);
    expect(select.value).toBe("shared");
    await act(async () => {
      select.value = "free";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(select.value).toBe("free");
    const rows = host.querySelectorAll<HTMLButtonElement>(".matching-submissions button");
    await act(async () => rows[1].click());
    expect(select.value).toBe("shared");
    expect(rows[1].querySelector("strong")?.textContent).toBe("答卷 2");
    const buttons = [...host.querySelectorAll<HTMLButtonElement>(".matching-candidates button")];
    await act(async () => buttons.find((button) => button.textContent === "确认学生")!.click());
    expect(onConfirmStudent).toHaveBeenCalledWith("target", "shared", 23);
    await act(async () => buttons.find((button) => button.textContent === "标记未知")!.click());
    expect(onUnknown).toHaveBeenCalledWith("target", 23);
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("只读批次说明锁定原因，重开仅发起确认，预览不受影响", async () => {
    const pages: CapturePage[] = [1, 2].map(sequence_no => ({ id: `page-${sequence_no}`, capture_batch_id: "batch-38", capture_file_id: "file-38",
      source_index: sequence_no, sequence_no, assigned_page_no: sequence_no, rotation_degrees: 0, decoded_file_asset_id: `asset-${sequence_no}`,
      status: "ready", revision: 1, page_identity: {}, match_candidates: [] }));
    const reopen = vi.fn();
    const split = vi.fn();
    const current = queue([submission({ student_id: "shared", pages })]);
    await act(async () => root.render(<MatchingWorkspace {...props} queue={current} canManage={false}
      readOnlyReason="本批次已完成，请先重开批次后匹配。" onReopen={reopen} onSplit={split} />));
    expect(host.textContent).toContain("本批次已完成，请先重开批次后匹配。");
    expect(host.textContent).toContain("确认匹配后，名册会自动显示该生已交卷；未匹配到答卷的学生自动显示缺考。");
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="按姓名或学号查找"]')?.disabled).toBe(true);
    const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === label)!;
    expect(button("确认学生").disabled).toBe(true);
    expect(button("标记未知").disabled).toBe(true);
    expect(host.querySelector<HTMLButtonElement>('[aria-label="拆分为新答卷"]')?.disabled).toBe(true);
    await act(async () => host.querySelector<HTMLButtonElement>(".matching-page-row button")!.click());
    expect(props.onPreview).toHaveBeenCalledWith(pages[0]);
    expect(reopen).not.toHaveBeenCalled();
    await act(async () => button("重开批次后匹配").click());
    expect(reopen).toHaveBeenCalledOnce();
    expect(onConfirmStudent).not.toHaveBeenCalled();
    expect(onUnknown).not.toHaveBeenCalled();
    expect(split).not.toHaveBeenCalled();
    await act(async () => root.render(<MatchingWorkspace {...props} queue={current} />));
    expect(host.querySelector('[data-alert="info"]')).toBeNull();
    expect(button("确认学生").disabled).toBe(false);
  });
});
