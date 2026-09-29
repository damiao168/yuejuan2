// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Appeal } from "../api/appeals";
import type { SessionUser } from "../auth/session";
import { AppealCenterPage } from "./AppealCenterPage";

const mocks = vi.hoisted(() => ({
  listAppeals: vi.fn(), getAppeal: vi.fn(), submitAppealRecommendation: vi.fn(),
  success: vi.fn(), error: vi.fn()
}));

vi.mock("../api/appeals", () => ({
  listAppeals: mocks.listAppeals,
  getAppeal: mocks.getAppeal,
  submitAppealRecommendation: mocks.submitAppealRecommendation
}));
vi.mock("../components/PageState", async () => {
  const React = await import("react");
  return {
    LoadingState: ({ label }: { label: string }) => React.createElement("div", null, label),
    EmptyState: () => React.createElement("div"),
    ErrorState: () => React.createElement("div")
  };
});
vi.mock("../components/ResponsiveTable", async () => {
  const React = await import("react");
  return {
    ResponsiveTable: ({ dataSource, onRow }: { dataSource: Appeal[]; onRow: (appeal: Appeal) => { onClick: () => void } }) =>
      React.createElement("div", null, dataSource.map((appeal) => React.createElement("button", {
        key: appeal.id, "data-row-id": appeal.id, onClick: onRow(appeal).onClick
      }, appeal.id)))
  };
});
vi.mock("antd", async () => {
  const React = await import("react");
  const Input = Object.assign(
    ({ value, onChange, placeholder }: { value?: string; onChange?: (event: React.ChangeEvent<HTMLInputElement>) => void; placeholder?: string }) =>
      React.createElement("input", { value, onChange, placeholder }),
    { TextArea: ({ value, onChange, placeholder }: { value?: string; onChange?: (event: React.ChangeEvent<HTMLTextAreaElement>) => void; placeholder?: string }) =>
      React.createElement("textarea", { value, onChange, placeholder }) }
  );
  return {
    App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error }, modal: { confirm: vi.fn() } }) },
    Alert: () => null,
    Button: ({ children, onClick, disabled }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean }) =>
      React.createElement("button", { onClick, disabled }, children),
    Input,
    InputNumber: () => null,
    Select: () => null,
    Space: ({ children }: { children: React.ReactNode }) => React.createElement("div", null, children),
    Tag: ({ children }: { children: React.ReactNode }) => React.createElement("span", null, children)
  };
});

function appeal(id: string): Appeal {
  return {
    id, exam_name: `考试 ${id}`, question_no: id, reason: `申诉 ${id}`,
    status: "under_review", assigned_to: "teacher", revision: 1,
    student_id: "student", exam_id: "exam", subject: "math", anonymous_code: id
  } as Appeal;
}

describe("appeal detail selection", () => {
  let root: Root;
  let host: HTMLDivElement;
  let finishA: (value: { appeal: Appeal }) => void;

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    mocks.listAppeals.mockResolvedValue({ appeals: [appeal("A"), appeal("B")] });
    mocks.getAppeal.mockImplementation((id: string) => id === "A"
      ? new Promise((resolve) => { finishA = resolve; })
      : Promise.resolve({ appeal: appeal("B") }));
    mocks.submitAppealRecommendation.mockResolvedValue({});
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    host.remove();
  });

  it("keeps B selected and submits B when A's detail arrives late", async () => {
    await act(async () => {
      root.render(<AppealCenterPage mode="teacher" canRead canManage={false} canWork
        canReadAudit={false} canReadIdentities={false} canReadExams={false}
        currentUser={{ id: "teacher" } as SessionUser} />);
    });
    expect(mocks.getAppeal).toHaveBeenCalledWith("A");
    await act(async () => { host.querySelector<HTMLButtonElement>('[data-row-id="B"]')?.click(); });
    expect(host.textContent).toContain("考试 B");
    await act(async () => { finishA({ appeal: appeal("A") }); });
    expect(host.textContent).toContain("考试 B");
    expect(host.textContent).not.toContain("正在读取答卷与复核信息");

    const reason = host.querySelector<HTMLTextAreaElement>("textarea");
    expect(reason).not.toBeNull();
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(reason, "依据评分标准重新核对答卷后建议维持结果");
      reason?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const submit = [...host.querySelectorAll("button")].find((button) => button.textContent === "提交复核意见");
    await act(async () => { submit?.click(); });
    expect(mocks.submitAppealRecommendation).toHaveBeenCalledWith("B", expect.objectContaining({ expected_revision: 1 }));
  });
});
