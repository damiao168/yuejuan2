// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PersonalReviewBookmark } from "../../../../api/reviewBookmarks";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import { createReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import type { PersonalReviewBookmarkController } from "../hooks/usePersonalReviewBookmarks";
import { ReviewBookmarkReasonDialog } from "./ReviewBookmarks";

const mocks = vi.hoisted(() => ({ state: vi.fn(), write: vi.fn(), close: vi.fn(), confirm: vi.fn() }));
vi.mock("@tanstack/react-query", () => ({ useQuery: mocks.state, queryOptions: (options: unknown) => options }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) },
  Modal: ({ children, onOk, okButtonProps }: { children: ReactNode; onOk: () => void; okButtonProps: { disabled: boolean } }) => <div>{children}<button type="button" disabled={okButtonProps.disabled} onClick={onOk}>保存说明</button></div>,
  Input: { TextArea: ({ value, onChange }: { value: string; onChange: (event: { target: { value: string } }) => void }) => <textarea aria-label="复查说明" value={value} onChange={onChange} /> },
  Alert: ({ message, description, action }: { message: string; description?: ReactNode; action?: ReactNode }) => <div role="alert">{message}{description}{action}</div>,
  Button: ({ children, onClick, disabled }: { children: ReactNode; onClick: () => void; disabled?: boolean }) => <button type="button" disabled={disabled} onClick={onClick}>{children}</button>
}));

const original: PersonalReviewBookmark = { id: "bookmark-A", task_id: "task-A", marked: true, reason: "原说明", revision: 2, can_edit_reason: true };
describe("复查说明编辑的版本确认", () => {
  let root: Root; let node: HTMLDivElement;
  let current: PersonalReviewBookmark;
  const controller = { busy: false, error: "", uncertainTask: "", write: mocks.write } as unknown as PersonalReviewBookmarkController;
  beforeEach(() => {
    vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); setBusinessCommandScope("tenant", "teacher");
    current = original; mocks.state.mockImplementation(() => ({ data: current, isPending: false, isError: false })); mocks.write.mockResolvedValue(true);
    node = document.createElement("div"); document.body.append(node); root = createRoot(node);
  });
  afterEach(async () => { await act(async () => root.unmount()); node.remove(); });
  async function render() { await act(async () => root.render(<ReviewBookmarkReasonDialog scope={createReviewTaskContextScope()} bookmark={original} controller={controller} onClose={mocks.close} />)); }
  it("后台新版本不能自动替换保存使用的revision，需要展示最新说明并由用户明确确认", async () => {
    await render();
    current = { ...original, revision: 3, reason: "另一会话的新说明" };
    await render();
    const buttons = Array.from(node.querySelectorAll("button"));
    expect(buttons.find((button) => button.textContent === "保存说明")?.disabled).toBe(true);
    expect(node.textContent).toContain("另一会话的新说明");
    expect(node.querySelector("textarea")?.value).toBe("原说明");
    expect(mocks.write).not.toHaveBeenCalled();
    await act(async () => buttons.find((button) => button.textContent === "按最新版本保存本页说明")?.click());
    expect(mocks.write).toHaveBeenCalledWith(current, "原说明");
    expect(mocks.close).toHaveBeenCalledTimes(1);
  });
  it("已取消的标记不能从仍打开的说明弹窗中被重新加入", async () => {
    current = { ...original, revision: 3, marked: false, reason: "" };
    await render();
    expect(node.textContent).toContain("不能从说明编辑中重新加入");
    expect(node.textContent).not.toContain("按最新版本保存本页说明");
    await act(async () => node.querySelector("button")?.click());
    expect(mocks.write).not.toHaveBeenCalled();
  });
  it("没有版本变化时仍按打开弹窗的精确版本写入", async () => {
    await render();
    await act(async () => node.querySelector("button")?.click());
    expect(mocks.write).toHaveBeenCalledWith(original, "原说明");
  });
});
