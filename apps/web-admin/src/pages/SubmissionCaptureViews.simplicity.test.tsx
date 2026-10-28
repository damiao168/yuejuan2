// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { UploadQueuePanel, type UploadQueueItem } from "./SubmissionCaptureViews";

vi.mock("antd", () => ({
  Alert: () => null, Descriptions: () => null, Progress: () => null,
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button onClick={onClick}>{children}</button>,
  List: Object.assign(({ dataSource, renderItem }: { dataSource: UploadQueueItem[]; renderItem: (item: UploadQueueItem) => ReactNode }) => <ul>{dataSource.map(item => <li key={item.id}>{renderItem(item)}</li>)}</ul>, { Item: ({ children }: { children: ReactNode }) => <div>{children}</div> })
}));
vi.mock("../components/ResponsiveTable", () => ({ ResponsiveTable: () => null }));

describe("上传队列失败可发现性", () => {
  it("超过八项后仍显示较早失败，完成记录只在主动展开后显示", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const container = document.createElement("div"); const root = createRoot(container);
    const queue: UploadQueueItem[] = Array.from({ length: 10 }, (_, index) => ({ id: String(index), fileName: `完成${index}.png`, status: "success", phase: "完成", percent: 100 }));
    queue.push({ id: "old-error", fileName: "较早失败.png", status: "error", phase: "登记", percent: 50, error: "连接中断" });
    try {
      await act(async () => root.render(<UploadQueuePanel queue={queue} percent={90} />));
      expect(container.textContent).toContain("较早失败.png"); expect(container.textContent).toContain("连接中断");
      expect(container.textContent).not.toContain("完成0.png");
      await act(async () => container.querySelector("button")!.click());
      expect(container.textContent).toContain("完成0.png"); expect(container.textContent).toContain("较早失败.png");
    } finally { await act(async () => root.unmount()); }
  });
});
