// @vitest-environment jsdom
import type { ComponentProps } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ScoreActionsPanel } from "./ScoreActionsPanel";

const base: ComponentProps<typeof ScoreActionsPanel> = {
  publishedOrLocked: false, canWrite: true, selectedExamId: "exam-1", gradeTotal: 2,
  quality: { stage: "publish", can_publish: true, quality: { passed: true, issues: [] } },
  actioning: null, confirmReason: "confirmed", publishReason: "approved",
  setConfirmReason: vi.fn(), setPublishReason: vi.fn(), finalize: async () => undefined,
  confirmGrades: async () => undefined, publish: vi.fn(), exportGrades: vi.fn(),
  lastWatermark: "watermark-1", canReadAudit: false, scoreAuditLogs: []
};

function render(props: Partial<ComponentProps<typeof ScoreActionsPanel>> = {}) {
  const container = document.createElement("div");
  container.innerHTML = renderToStaticMarkup(<ScoreActionsPanel {...base} {...props} />);
  const disabled = (label: string) => Array.from(container.querySelectorAll<HTMLButtonElement>("button"))
    .find((button) => button.textContent?.trim() === label)?.disabled;
  return { container, disabled };
}

describe("basic score workflow action panel", () => {
  it("retains all four commands for a writable unlocked examination", () => {
    const { container, disabled } = render();
    for (const label of ["汇总最终成绩", "确认成绩", "公开并锁定基础成绩", "导出成绩"]) expect(disabled(label)).toBe(false);
    expect(container.textContent).toContain("watermark-1");
    expect(container.textContent).toContain("无权查看操作记录");
    expect(container.querySelector('label[for="score-confirm-reason"]')).not.toBeNull();
    expect(container.querySelector('label[for="score-publish-reason"]')).not.toBeNull();
  });

  it("keeps confirmation and publication locked while allowing an authorized export", () => {
    const { container, disabled } = render({ publishedOrLocked: true });
    expect(disabled("汇总最终成绩")).toBe(true);
    expect(disabled("确认成绩")).toBe(true);
    expect(disabled("公开并锁定基础成绩")).toBe(true);
    expect(disabled("导出成绩")).toBe(false);
    expect(container.textContent).toContain("基础成绩不能直接修改");
  });

  it("blocks every write-dependent command while a reload or another action disallows writes", () => {
    const { disabled } = render({ canWrite: false });
    for (const label of ["汇总最终成绩", "确认成绩", "公开并锁定基础成绩", "导出成绩"]) expect(disabled(label)).toBe(true);
  });

  it("preserves the individual examination, grade count and quality checks", () => {
    expect(render({ selectedExamId: "" }).disabled("汇总最终成绩")).toBe(true);
    const noGrades = render({ gradeTotal: 0 });
    expect(noGrades.disabled("确认成绩")).toBe(true);
    expect(noGrades.disabled("导出成绩")).toBe(true);
    expect(render({ quality: null }).disabled("公开并锁定基础成绩")).toBe(true);
    expect(render({ quality: { ...base.quality!, can_publish: false } }).disabled("公开并锁定基础成绩")).toBe(true);
  });
});
