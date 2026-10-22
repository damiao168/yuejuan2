// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NativeStoragePanel } from "./NativeStoragePanel";
import type { ArchivedCleanupCandidate } from "../lib/durableStore";
const mocks = vi.hoisted(() => ({ usage: vi.fn(), candidates: vi.fn(), cleanup: vi.fn(), confirm: vi.fn(), changeDays: undefined as undefined | ((value: number) => void) }));
vi.mock("../lib/durableStore", () => ({ hasDurableDesktopStore: () => true, getDurableStorageUsage: mocks.usage, listArchivedCleanupCandidates: mocks.candidates, cleanupArchivedOriginals: mocks.cleanup }));
vi.mock("../lib/scanFiles", () => ({ formatBytes: (value: number) => `${value} B`, formatDate: (value: string) => value }));
vi.mock("antd", () => {
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return { Alert: ({ message }: { message: string }) => <p>{message}</p>, Space: Box,
    Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick: () => void }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
    InputNumber: ({ onChange }: { onChange: (value: number) => void }) => { mocks.changeDays = onChange; return <input />; },
    List: Object.assign(({ dataSource, renderItem }: { dataSource: ArchivedCleanupCandidate[]; renderItem: (item: ArchivedCleanupCandidate, index: number) => ReactNode }) => <>{dataSource.map(renderItem)}</>, { Item: Box }), Modal: { confirm: mocks.confirm } };
});
describe("controlled archived-original cleanup", () => {
  let root: Root; let container: HTMLDivElement; let decision: { onOk: () => void; onCancel: () => void };
  beforeEach(() => {
    vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.usage.mockResolvedValue({ spoolBytes: 100, databaseBytes: 20, freeBytes: 5000, assetCount: 3 });
    mocks.candidates.mockResolvedValue([{ id: "old-confirmed", filename: "答卷.pdf", confirmedAt: "2026-09-01", sizeBytes: 100 }]);
    mocks.cleanup.mockResolvedValue({ removed: 1, failures: [] }); mocks.confirm.mockImplementation((value) => { decision = value; });
    container = document.createElement("div"); root = createRoot(container);
  });
  afterEach(async () => { await act(async () => root.unmount()); });
  async function click(prefix: string) { const button = [...container.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.startsWith(prefix))!; expect(button.disabled).toBe(false); await act(async () => button.click()); }
  async function prepare() {
    await act(async () => root.render(<NativeStoragePanel scope="account-A" />));
    expect(container.textContent).toContain("100 B"); expect(container.textContent).toContain("5000 B");
    await act(async () => mocks.changeDays!(7)); await click("检查可清理"); await click("核对后清理");
  }
  it("requires explicit school retention and confirmation before scoped cleanup", async () => {
    await prepare(); expect(mocks.cleanup).not.toHaveBeenCalled();
    await act(async () => decision.onOk());
    expect(mocks.candidates).toHaveBeenCalledWith(7, "account-A");
    expect(mocks.cleanup).toHaveBeenCalledWith(7, ["old-confirmed"], "account-A");
    expect(container.textContent).toContain("已删除 1 份本机原件");
  });
  it("preserves originals on cancellation or an account switch while the decision is pending", async () => {
    await prepare(); await act(async () => decision.onCancel()); expect(mocks.cleanup).not.toHaveBeenCalled();
    await click("核对后清理");
    await act(async () => root.render(<NativeStoragePanel scope="account-B" />));
    await act(async () => decision.onOk()); expect(mocks.cleanup).not.toHaveBeenCalled();
  });
});
