// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { DraftConflictModal } from "./DraftConflictModal";
import type { DraftFallbackSnapshot } from "../gradingWorkbench.types";

vi.mock("antd", () => ({
  Modal: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Button: () => null,
  Radio: Object.assign(() => null, { Group: () => null })
}));

it("评分点使用名称分数对照，阅读位置默认折叠且不显示 JSON", async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  const host = document.createElement("div"); const root = createRoot(host);
  const local: DraftFallbackSnapshot = { draft: { score: 0, rubricSelections: { point: 0 }, comments: "", studentFeedback: "", privateNote: "", reason: "", disputeReason: "", answerText: "" }, viewer: { mode: "segment", scale: 1.5, rotation: 0, offset: { x: 2, y: 4 }, fit: false } };
  const server = { draft: { ...local.draft, score: 2, rubricSelections: { point: 2 } }, viewer: { ...local.viewer, scale: 1 } };
  try {
    await act(async () => root.render(<DraftConflictModal conflict={{ taskId: "task", serverRevision: 2, local, server }} rubricPoints={[{ id: "point", description: "推导过程", score: 2, required: true }]} onCancel={() => undefined} onApply={() => undefined} />));
    expect(host.textContent).toContain("推导过程：0 分"); expect(host.textContent).toContain("推导过程：2 分");
    expect(host.textContent).not.toContain('"point"'); expect(host.textContent).not.toContain('"offset"');
    expect(host.querySelector("details")?.open).toBe(false);
  } finally { await act(async () => root.unmount()); }
});
