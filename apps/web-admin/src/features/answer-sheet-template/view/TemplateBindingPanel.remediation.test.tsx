// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { answerQuestion, answerTemplate } from "../testing/answerSheetFixtures";
import { templateCoveredQuestions } from "../editor/templateCoverage";
import { TemplateBindingPanel } from "./TemplateWorkspaceView";
import type { TemplatePageViewState } from "./templateViewTypes";

afterEach(cleanup);

describe("group-only template confirmation", () => {
  it.each(["draft", "locked"] as const)("keeps a valid group-only %s template eligible for confirmation", status => {
    const layout = { pages: [{ ...answerTemplate.layout.pages[0], question_regions: [], grading_groups: [{ group_id: "fill-group", question_ids: [answerQuestion.id], bbox: { x: .1, y: .2, width: .3, height: .4 } }] }] };
    const activate = vi.fn();
    const bind = vi.fn();
    const view = { selectedTemplate: { ...answerTemplate, status, layout }, questions: [answerQuestion], canManage: true, canEditSources: true,
      sourceUpload: { uploading: false }, printing: { printBusy: false },
      editor: { coveredQuestions: templateCoveredQuestions(layout), saving: false, suggestingRegions: false, readonly: status === "locked", confirmActivate: activate },
      binding: { examBinding: null, bindingBusy: false, bindTemplateForExam: bind }
    } as unknown as TemplatePageViewState;
    render(<TemplateBindingPanel view={view} />);
    const button = screen.getByRole<HTMLButtonElement>("button", { name: "确认答题卡设置" });
    expect(button.disabled).toBe(false);
    fireEvent.click(button);
    expect(status === "draft" ? activate : bind).toHaveBeenCalledOnce();
    expect(status === "draft" ? bind : activate).not.toHaveBeenCalled();
  });

  it("still rejects a template missing a question from both independent and group regions", () => {
    const layout = { pages: [{ ...answerTemplate.layout.pages[0], question_regions: [], grading_groups: [] }] };
    const view = { selectedTemplate: { ...answerTemplate, status: "locked", layout }, questions: [answerQuestion], canManage: true, canEditSources: true,
      sourceUpload: { uploading: false }, printing: { printBusy: false }, editor: { coveredQuestions: templateCoveredQuestions(layout) }, binding: {}
    } as unknown as TemplatePageViewState;
    render(<TemplateBindingPanel view={view} />);
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认答题卡设置" }).disabled).toBe(true);
    expect(screen.getByText("还有 1 道题未配置答题区域。")).toBeTruthy();
  });
});
