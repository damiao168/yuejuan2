// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import { ApiClientError } from "../../../../api/client";
import { createReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import { notifyReviewBookmarkRemoved } from "../../../../query/reviewBookmarks";
import type { PersonalReviewBookmarkContext } from "../../../../api/reviewBookmarks";
import { ReviewBookmarkReadOnly } from "./ReviewBookmarks";

const api = vi.hoisted(() => ({ context: vi.fn(), image: vi.fn() }));
vi.mock("../../../../api/reviewBookmarks", async (original) => ({ ...await original<typeof import("../../../../api/reviewBookmarks")>(), getPersonalReviewBookmarkContext: api.context, getPersonalReviewBookmarkImage: api.image }));
vi.mock("antd", () => ({ Modal: ({ children }: { children: ReactNode }) => <div>{children}</div>, Spin: () => <span>loading</span>, Alert: ({ message, action }: { message: string; action?: ReactNode }) => <div role="alert">{message}{action}</div>, Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button type="button" onClick={onClick}>{children}</button> }));
function context(id = "A"): PersonalReviewBookmarkContext { return { bookmark: { id, task_id: id, marked: true, reason: "核对末步", revision: 2, can_edit_reason: true }, task: { id, exam_id: "exam", exam_name: "合成考试", question_id: "question", question_no: "第2题", subject: "history", anonymous_code: `匿名${id}`, state: "submitted", max_score: 10 }, question: { stem: "合成题目", options: [], rubric: { max_score: 10, points: [] } }, own_submission: { score: 8, max_score: 10, submitted_at: "2026-10-18T01:00:00Z", rubric_selections: [] }, answer: { text: "合成文字", text_binding: "unverified", image_url: "ignored", evidence_version: `version-${id}`, image_available: true, material_change: "unknown" }, readonly: true }; }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done; }); return { promise, resolve }; }
describe("复查只读材料容器", () => {
  let root: Root; let node: HTMLDivElement;
  beforeEach(() => { vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); setBusinessCommandScope("tenant", "teacher"); node = document.createElement("div"); document.body.append(node); root = createRoot(node); api.context.mockResolvedValue(context()); api.image.mockResolvedValue({ blob: new Blob(["image"], { type: "image/png" }), contentType: "image/png" }); Object.defineProperty(URL, "createObjectURL", { configurable: true, value: vi.fn(() => "blob:synthetic") }); Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: vi.fn() }); });
  afterEach(async () => { await act(async () => root.unmount()); node.remove(); });
  it("只调context和版本图，换会话立即清文字并释放Blob", async () => {
    const scope = createReviewTaskContextScope();
    await act(async () => root.render(<ReviewBookmarkReadOnly scope={scope} bookmarkId="A" onClose={() => undefined} onUnavailable={() => undefined} />));
    expect(node.textContent).toContain("本人本次提交"); expect(node.textContent).toContain("8 / 10"); expect(node.textContent).not.toContain("提交并下一份");
    expect(api.image).toHaveBeenCalledWith("A", "version-A", expect.any(AbortSignal)); expect(node.querySelector("img")?.src).toBe("blob:synthetic");
    await act(async () => setBusinessCommandScope("tenant", "teacher"));
    expect(node.textContent).not.toContain("合成文字"); expect(node.querySelector("img")).toBeNull(); expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:synthetic");
  });
  it("图版本冲突时不展示已读取的文字，明确要求整份重载", async () => {
    api.image.mockRejectedValue(new ApiClientError(409, "evidence_version_changed", "changed"));
    await act(async () => root.render(<ReviewBookmarkReadOnly scope={createReviewTaskContextScope()} bookmarkId="A" onClose={() => undefined} onUnavailable={() => undefined} />));
    expect(node.textContent).toContain("材料已变化"); expect(node.textContent).not.toContain("合成文字"); expect(node.querySelector("img")).toBeNull();
  });
  it("A请求迟到不能覆盖已切换的B材料", async () => {
    const pending = deferred<PersonalReviewBookmarkContext>(); api.context.mockReturnValueOnce(pending.promise).mockResolvedValue(context("B")); const scope = createReviewTaskContextScope(); const onUnavailable = () => undefined;
    await act(async () => root.render(<ReviewBookmarkReadOnly scope={scope} bookmarkId="A" onClose={() => undefined} onUnavailable={onUnavailable} />));
    await act(async () => root.render(<ReviewBookmarkReadOnly scope={scope} bookmarkId="B" onClose={() => undefined} onUnavailable={onUnavailable} />));
    await act(async () => pending.resolve(context("A")));
    expect(node.textContent).toContain("匿名B"); expect(node.textContent).not.toContain("匿名A"); expect(api.image).toHaveBeenCalledTimes(1); expect(api.image.mock.calls[0][0]).toBe("B");
  });
  it("取消时尚在途的context不能新展示材料或继续请求图片", async () => {
    const pending = deferred<PersonalReviewBookmarkContext>(); api.context.mockReturnValue(pending.promise);
    const scope = createReviewTaskContextScope();
    await act(async () => root.render(<ReviewBookmarkReadOnly scope={scope} bookmarkId="A" onClose={() => undefined} onUnavailable={() => undefined} />));
    await act(async () => notifyReviewBookmarkRemoved(scope, "A"));
    await act(async () => pending.resolve(context("A")));
    expect(node.textContent).toContain("已移出清单"); expect(node.textContent).not.toContain("合成文字");
    expect(node.textContent).not.toContain("重新加载"); expect(api.image).not.toHaveBeenCalled();
  });
  it("取消时在途图片被丢弃，不能创建或展示Blob", async () => {
    const pending = deferred<{ blob: Blob; contentType: string }>(); api.image.mockReturnValue(pending.promise);
    const scope = createReviewTaskContextScope();
    await act(async () => root.render(<ReviewBookmarkReadOnly scope={scope} bookmarkId="A" onClose={() => undefined} onUnavailable={() => undefined} />));
    await act(async () => notifyReviewBookmarkRemoved(scope, "A"));
    await act(async () => pending.resolve({ blob: new Blob(["image"], { type: "image/png" }), contentType: "image/png" }));
    expect(node.textContent).not.toContain("合成文字"); expect(node.querySelector("img")).toBeNull(); expect(URL.createObjectURL).not.toHaveBeenCalled();
  });
});
