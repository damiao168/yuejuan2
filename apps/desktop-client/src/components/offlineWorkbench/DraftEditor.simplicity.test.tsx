// @vitest-environment jsdom
import { useState, type ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DraftEditor } from "./OfflineWorkbenchView";
import type { OfflineGradeDraft, OfflineTaskPackage } from "../../types";

vi.mock("antd", () => {
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  const Input = Object.assign(({ value, onChange }: { value: string; onChange: React.ChangeEventHandler<HTMLInputElement> }) => <input value={value} onChange={onChange} />, { TextArea: () => <textarea /> });
  return { Form: Object.assign(Box, { Item: ({ label, children }: { label: ReactNode; children: ReactNode }) => <label>{label}{children}</label> }),
    Input, InputNumber: ({ value, disabled, onChange }: { value: number | null; disabled?: boolean; onChange: (v: number) => void }) => <input type="number" value={value ?? ""} disabled={disabled} onChange={event => onChange(Number(event.target.value))} />,
    Checkbox: ({ children, checked, onChange }: { children: ReactNode; checked: boolean; onChange: React.ChangeEventHandler<HTMLInputElement> }) => <label><input type="checkbox" checked={checked} onChange={onChange} />{children}</label>,
    Space: Box, Button: Box, Empty: Box, Modal: { confirm: vi.fn() }
  };
});
const pkg = { task: { id: "task-a" }, question: { rubric: { points: [{ id: "a", description: "步骤甲", score: 3 }, { id: "b", description: "步骤乙", score: 4 }] } } } as OfflineTaskPackage;
function Harness() {
  const [draft, setDraft] = useState<OfflineGradeDraft>({ taskId: "task-a", score: 5, rubricSelections: { a: 1, b: 2 }, comments: "", studentFeedback: "", privateNote: "", reason: "历史说明" });
  return <DraftEditor pkg={pkg} draft={draft} setDraft={setDraft} maxScore={7} disabled={false} />;
}
describe("离线按点评分与人工总分", () => {
  it("恢复时保留原总分，修改评分点后自动汇总；主动调整后保留人工覆盖", () => {
    render(<Harness />);
    const total = screen.getByLabelText(/最终分/) as HTMLInputElement;
    expect(total.value).toBe("5");
    expect(total.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("步骤甲"), { target: { value: "3" } });
    expect(total.value).toBe("5");
    fireEvent.change(screen.getByLabelText("步骤乙"), { target: { value: "4" } });
    expect(total.value).toBe("7");
    fireEvent.click(screen.getByLabelText("调整总分"));
    fireEvent.change(total, { target: { value: "6" } });
    fireEvent.change(screen.getByLabelText("步骤甲"), { target: { value: "2" } });
    expect(total.value).toBe("6");
    expect(screen.getByLabelText(/人工调整\/提交原因/)).toBeTruthy();
  });
});
