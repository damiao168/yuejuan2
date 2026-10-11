// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppealDecisionPanel } from "./AppealDecisionPanel";
vi.mock("antd", () => ({ Button: ({ children, onClick, disabled }: { children: ReactNode; onClick?: () => void; disabled?: boolean }) => <button disabled={disabled} onClick={onClick}>{children}</button>, Select: ({ placeholder, disabled }: { placeholder?: string; disabled?: boolean }) => <select aria-label={placeholder} disabled={disabled} />, InputNumber: () => null, Input: { TextArea: () => <textarea /> } }));
afterEach(cleanup);
describe("管理员申诉分派入口", () => {
  it("未分派直接呈现动作，已分派可更换，终态不能分派", () => {
    const reassign = vi.fn();
    const props = { mode: "admin" as const, selected: {} as Parameters<typeof AppealDecisionPanel>[0]["selected"], workerNames: {}, workerOptions: [{ value: "worker", label: "独立教师" }], assignedTo: "worker", decision: "accepted", canManage: true, terminal: false, actioning: "", score: null, reason: "", recommendation: "uphold" as Parameters<typeof AppealDecisionPanel>[0]["recommendation"], canSubmit: false, draftTouched: false, onScoreChange: vi.fn(), onReasonChange: vi.fn(), onAssignedToChange: vi.fn(), onDecisionChange: vi.fn(), onRecommendationChange: vi.fn(), onReassign: reassign, onAdminSubmit: vi.fn(), onSubmit: vi.fn() };
    const view = render(<AppealDecisionPanel {...props} />);
    expect(screen.getByText("分派独立复核").closest("details")).toBeNull(); fireEvent.click(screen.getByText("分派复核")); expect(reassign).toHaveBeenCalledOnce();
    expect(document.body.textContent).not.toContain("自动分派");
    view.rerender(<AppealDecisionPanel {...props} selected={{ ...props.selected, assigned_to: "worker" }} />); expect(screen.getByText("更换复核教师").tagName).toBe("SUMMARY");
    view.rerender(<AppealDecisionPanel {...props} terminal />); expect((screen.getByText("分派复核") as HTMLButtonElement).disabled).toBe(true);
  });
});
