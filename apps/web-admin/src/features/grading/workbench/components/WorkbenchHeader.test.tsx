import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { WorkbenchHeader, type WorkbenchHeaderProps } from "./WorkbenchHeader";

vi.mock("../../../../components/BackmarkQueue", () => ({ BackmarkQueue: () => null }));
vi.mock("../../../../components/RegradeQueue", () => ({ RegradeQueue: () => null }));

const noop = async () => undefined;
const baseProps: WorkbenchHeaderProps = {
  canWork: true,
  canManageTasks: false,
  queueScope: "mine",
  hasContext: true,
  ownsSelectedTask: true,
  myProgress: { id: "reviewer-1", name: "阅卷员", total: 10, completed: 3, active: 1, percent: 30 },
  reviewerProgress: [],
  remainingCount: 7,
  progressComplete: true,
  draftSaveStatus: "saved",
  loading: false,
  actioning: null,
  onRefresh: noop,
  onNext: noop,
  onClaim: noop,
  onRelease: noop,
  onReloadConflict: noop,
  onResolveConflict: noop
};

describe("WorkbenchHeader status rendering", () => {
  it.each([
    [{}, "已完成 3 / 共 10"],
    [{ progressComplete: false }, "已完成 3 / 已加载 10 · 数据未完整"],
    [{ canManageTasks: true }, "剩余 7 份"],
    [{ canManageTasks: true, progressComplete: false }, "已加载待处理 7 份 · 数据未完整"],
    [{ canManageTasks: true, hasContext: false }, "分配任务、查看进度和处理异常"]
  ] as const)("keeps reviewer and manager progress labels distinct: %s", (overrides, expected) => {
    const html = renderToStaticMarkup(<WorkbenchHeader {...baseProps} {...overrides} />);
    expect(html).toContain(expected);
  });

  it.each([
    ["conflict", "草稿已被其他会话更新"],
    ["offline", "当前离线，未同步内容仅在本页暂存"],
    ["error", "草稿尚未保存到服务端"],
    ["readonly", "管理员只读检查"]
  ] as const)("shows only the notice for the %s draft state", (draftSaveStatus, message) => {
    const html = renderToStaticMarkup(<WorkbenchHeader {...baseProps} draftSaveStatus={draftSaveStatus} />);
    expect(html).toContain(message);
    expect(html.match(/class="ant-alert-message"/g)).toHaveLength(1);
  });

  it("does not show a failure notice for a saved draft", () => {
    const html = renderToStaticMarkup(<WorkbenchHeader {...baseProps} />);
    expect(html).not.toContain("ant-alert-message");
    expect(html).toContain("已保存到服务端");
  });
});
