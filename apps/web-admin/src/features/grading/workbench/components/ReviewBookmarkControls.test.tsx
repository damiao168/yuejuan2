// @vitest-environment jsdom
import { useMemo, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../../api/client";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import type { PersonalReviewBookmark, PersonalReviewBookmarkAck } from "../../../../api/reviewBookmarks";
import { createReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import { usePersonalReviewBookmarks } from "../hooks/usePersonalReviewBookmarks";
import { ReviewBookmarkControls } from "./ReviewBookmarks";

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), remove: vi.fn(), success: vi.fn(), error: vi.fn() }));
vi.mock("../../../../api/reviewBookmarks", async original => ({ ...await original<typeof import("../../../../api/reviewBookmarks")>(), getPersonalReviewBookmark: mocks.get, putPersonalReviewBookmark: mocks.put, deletePersonalReviewBookmark: mocks.remove }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error } }) },
  Button: ({ children, onClick, disabled, loading, type: _type, icon: _icon, ...props }: { children: ReactNode; onClick: () => void; disabled?: boolean; loading?: boolean; type?: string; icon?: ReactNode }) => <button {...props} disabled={disabled} aria-busy={Boolean(loading)} onClick={onClick}>{children}</button>
}));

let server: PersonalReviewBookmark;
let client: QueryClient;
function Harness() {
  const scope = useMemo(() => createReviewTaskContextScope(), []);
  const controller = usePersonalReviewBookmarks(scope, "task-A");
  return <><ReviewBookmarkControls scope={scope} controller={controller} /><output>{controller.error}</output></>;
}
const ack = (): PersonalReviewBookmarkAck => ({ bookmark_id: "mark-A", revision: server.revision, marked: server.marked });
async function mount() {
  render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>);
  await waitFor(() => expect(mocks.get).toHaveBeenCalled());
  await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: server.marked ? "已标记" : "标记试卷" }).disabled).toBe(!server.marked && !server.can_edit_reason));
}
describe("一键标记答卷", () => {
  beforeEach(() => {
    vi.clearAllMocks(); setBusinessCommandScope("tenant", "grader");
    server = { id: null, task_id: "task-A", marked: false, reason: "", revision: 0, can_edit_reason: true };
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    mocks.get.mockImplementation(async () => ({ bookmark: { ...server } }));
    mocks.put.mockImplementation(async () => { server = { ...server, id: "mark-A", marked: true, revision: server.revision + 1 }; return ack(); });
    mocks.remove.mockImplementation(async () => { server = { ...server, id: null, marked: false, revision: server.revision + 1 }; return ack(); });
  });
  afterEach(() => { cleanup(); client.clear(); });
  it("点击后由按钮显示标记状态，可立即取消且没有遮挡操作的成功提示", async () => {
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "标记试卷" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "已标记" }).getAttribute("aria-pressed")).toBe("true"));
    expect(mocks.put).toHaveBeenCalledWith("task-A", "", 0, expect.any(String), expect.any(AbortSignal));
    expect(mocks.success).not.toHaveBeenCalled();
    expect(screen.queryByRole("textbox")).toBeNull(); expect(screen.queryByRole("button", { name: "编辑复查说明" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "已标记" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "标记试卷" }).getAttribute("aria-pressed")).toBe("false"));
    expect(mocks.remove).toHaveBeenCalledWith("mark-A", 1, expect.any(String), expect.any(AbortSignal));
    expect(mocks.success).not.toHaveBeenCalled();
  });
  it("请求期间显示加载且阻止重复写入，失败时不显示成功", async () => {
    let reject!: (cause: unknown) => void;
    mocks.put.mockImplementation(() => new Promise((_resolve, fail) => { reject = fail; }));
    await mount();
    const button = screen.getByRole<HTMLButtonElement>("button", { name: "标记试卷" });
    fireEvent.click(button); fireEvent.click(button);
    await waitFor(() => expect(button.disabled).toBe(true));
    expect(button.getAttribute("aria-busy")).toBe("true"); expect(mocks.put).toHaveBeenCalledTimes(1);
    reject(new TypeError("offline"));
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("尚未确定"));
    expect(mocks.success).not.toHaveBeenCalled(); expect(screen.queryByRole("button", { name: "已标记" })).toBeNull();
  });
  it("旧新增命令回执之后读取到已取消的新版本，按钮以真实取消状态为准", async () => {
    mocks.put.mockImplementation(async () => { server = { ...server, revision: 2 }; return { bookmark_id: "mark-A", revision: 1, marked: true }; });
    await mount(); fireEvent.click(screen.getByRole("button", { name: "标记试卷" }));
    await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(2));
    expect(mocks.success).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "已标记" })).toBeNull();
  });
  it("没有标记资格时不能新增，历史说明也不会重新出现编辑入口", async () => {
    server = { ...server, can_edit_reason: false };
    await mount(); fireEvent.click(screen.getByRole("button", { name: "标记试卷" }));
    expect(mocks.put).not.toHaveBeenCalled();
    cleanup(); server = { ...server, marked: true, id: "mark-A", revision: 2, reason: "历史说明" };
    await mount(); expect(screen.queryByRole("textbox")).toBeNull(); expect(screen.queryByText("历史说明")).toBeNull();
  });
  it("状态读取失败可明确重试，读取成功前不发标记请求", async () => {
    mocks.get.mockRejectedValueOnce(new ApiClientError(503, "unavailable", "unavailable"));
    render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>);
    await screen.findByRole("button", { name: "重试标记状态" });
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "标记试卷" }).disabled).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "重试标记状态" }));
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "标记试卷" }).disabled).toBe(false));
    expect(mocks.put).not.toHaveBeenCalled();
  });
});
