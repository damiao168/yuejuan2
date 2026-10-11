// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MatchingQueue, MatchingSubmission } from "../../api/capture";
import { MatchingWorkspace } from "./MatchingWorkspace";

const mocks = vi.hoisted(() => ({ confirm: vi.fn() }));

vi.mock("../../components/StatusTag", () => ({
  StatusTag: ({ tone, children }: { tone: string; children: ReactNode }) => <span data-tone={tone}>{children}</span>,
}));
vi.mock("antd", () => {
  const Wrap = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) },
    Alert: ({ type, message }: { type: string; message: ReactNode }) => <div data-alert={type}>{message}</div>,
    Button: ({ children, onClick, disabled, loading, "aria-label": label }: {
      children?: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean; "aria-label"?: string;
    }) => <button type="button" onClick={onClick} disabled={disabled} data-loading={String(Boolean(loading))} aria-label={label}>{children}</button>,
    Empty: ({ description }: { description: ReactNode }) => <div>{description}</div>,
    InputNumber: ({ defaultValue, disabled, "aria-label": label }: {
      defaultValue?: number; disabled?: boolean; "aria-label"?: string;
    }) => <input type="number" defaultValue={defaultValue} disabled={disabled} aria-label={label} />,
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
});
