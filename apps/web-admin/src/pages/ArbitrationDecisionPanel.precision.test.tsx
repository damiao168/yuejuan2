// @vitest-environment jsdom
import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ArbitrationDecisionPanel } from "./ArbitrationDecisionPanel";
import { formatScore as arbitrationScore } from "./arbitrationPresentation.model";
import { formatScore as finalScore } from "../features/score-management/scorePresentation";
import { formatQuestionAppealScore } from "../components/questionAppealPresentation";
import type { ArbitrationDecisionDraft } from "./arbitrationQueue.model";

vi.mock("./ArbitrationPanels", () => ({ ArbitrationAuditList: () => null }));
afterEach(cleanup);

describe("仲裁及成绩两位小数", () => {
  it("仲裁满分2.25可以准确填写并提交2.25", () => {
    const submit = vi.fn();
    function Form() {
      const [draft, setDraft] = useState<ArbitrationDecisionDraft>({ finalScore: null, reason: "核对完成", studentFeedback: "" });
      return <ArbitrationDecisionPanel detail={null} draft={draft} draftTouched={false} finalGrade={null} maxScore={2.25}
        actioning={null} editingDisabled={false} canAssign={false} canSubmit auditLoading={false} auditError={null} auditLogs={[]}
        setDraft={setDraft} setDraftTouched={vi.fn()} onAssign={vi.fn()} onSubmit={() => submit(draft.finalScore)} onAuditRetry={vi.fn()} />;
    }
    render(<Form />);
    const input = screen.getByRole("spinbutton") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "2.25" } });
    fireEvent.blur(input);
    expect(input.value).toBe("2.25");
    fireEvent.click(screen.getByRole("button", { name: "提交仲裁" }));
    expect(submit).toHaveBeenCalledWith(2.25);
  });
  it("成绩、仲裁、题目申诉展示均保留2.25和1.01", () => {
    for (const format of [arbitrationScore, finalScore, formatQuestionAppealScore]) {
      expect(format(2.25)).toBe("2.25");
      expect(format(1.01)).toBe("1.01");
      expect(format(2)).toBe("2");
    }
  });
});
