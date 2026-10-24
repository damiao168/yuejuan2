import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { WorkbenchHeader, type WorkbenchHeaderProps } from "./WorkbenchHeader";

vi.mock("../../../../components/BackmarkQueue", () => ({ BackmarkQueue: () => <span>退回队列</span> }));
vi.mock("../../../../components/RegradeQueue", () => ({ RegradeQueue: () => <span>重评队列</span> }));

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
    [{}, "已完成 3/10"],
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

  it("does not show routine save text or a failure notice for a saved draft", () => {
    const html = renderToStaticMarkup(<WorkbenchHeader {...baseProps} />);
    expect(html).not.toContain("ant-alert-message");
    expect(html).not.toContain("draft-save-status");
  });

  it("keeps daily grading actions without routine save text, special queues or release controls", () => {
    const html = renderToStaticMarkup(<WorkbenchHeader {...baseProps} personalActions={<button>标记试卷</button>} onOpenReference={vi.fn()} onPrevious={noop} canPrevious />);
    for (const label of ["标记试卷", "评分细则", "题目与答案", "刷新", "上一份", "下一份"]) expect(html).toContain(label);
    for (const label of ["退回队列", "重评队列", "暂放", "提交并下一份"]) expect(html).not.toContain(label);
  });
});
