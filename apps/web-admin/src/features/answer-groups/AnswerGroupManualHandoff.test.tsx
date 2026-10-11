// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AnswerGroupMemberContext } from "../../api/answerGroups";
import { AnswerGroupManualHandoff } from "./AnswerGroupManualHandoff";

vi.mock("antd", () => ({
  Alert: ({ message, description }: { message: ReactNode; description: ReactNode }) => <section><div>{message}</div><div>{description}</div></section>,
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button type="button" onClick={onClick}>{children}</button>
}));
afterEach(cleanup);
function material(manual_review?: AnswerGroupMemberContext["manual_review"]) { return { manual_review } as AnswerGroupMemberContext; }

describe("分组样本拒绝后的真实人工交接", () => {
  it("未读材料时要求先读取，不把拒绝声称为已建任务", () => {
    render(<AnswerGroupManualHandoff canReadReview />);
    expect(screen.getByText(/先点击“查看实际答题样本”/)).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });
  it("无任务时明确学校阅卷管理员安排下一步", () => {
    render(<AnswerGroupManualHandoff material={material()} canReadReview onNavigate={vi.fn()} />);
    expect(screen.getByText("本样本尚未关联人工复核任务")).toBeTruthy();
    expect(screen.getByText(/不会自动新建复核任务/)).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });
  it.each([false, true])("真实任务已分派=%s时显示相应负责人，点击只使用返回的任务", (assigned) => {
    const navigate = vi.fn();
    render(<AnswerGroupManualHandoff material={material({ task_id: "real/task", status: "pending", assigned })} canReadReview onNavigate={navigate} />);
    expect(screen.getByText(assigned ? /已指派的复核教师负责/ : /学校阅卷管理员负责分派/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "查看本份人工复核" }));
    expect(navigate).toHaveBeenCalledWith("/grading?task=real%2Ftask");
  });
  it("无阅卷读取权限仅提示联系管理员，已完成任务不再声称等待复核", () => {
    render(<AnswerGroupManualHandoff material={material({ task_id: "private-task", status: "completed", assigned: true })} canReadReview={false} onNavigate={vi.fn()} />);
    expect(screen.getByText("本份人工复核：已完成")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.getByText(/如仍需重新检查/)).toBeTruthy();
    expect(document.body.textContent).not.toContain("private-task");
  });
});
