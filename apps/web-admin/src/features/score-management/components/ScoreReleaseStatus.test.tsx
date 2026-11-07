// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ScoreRelease } from "../../../api/scoreReleases";
import { ScoreReleaseHistory } from "./ScoreReleaseHistory";
import { ScoreReleaseStatus } from "./ScoreReleaseStatus";

vi.mock("antd", () => {
  const List = Object.assign(({ dataSource, renderItem }: { dataSource: unknown[]; renderItem: (value: unknown) => ReactNode }) => <div>{dataSource.map(renderItem)}</div>, {
    Item: ({ actions, children }: { actions?: ReactNode[]; children: ReactNode }) => <article>{children}{actions}</article>
  });
  return {
    List,
    Button: ({ children, onClick, disabled }: ComponentProps<"button">) => <button type="button" onClick={onClick} disabled={disabled}>{children}</button>,
    Drawer: ({ open, children, extra }: { open: boolean; children: ReactNode; extra?: ReactNode }) => open ? <aside>{extra}{children}</aside> : null,
    Empty: Object.assign(({ description }: { description: ReactNode }) => <p>{description}</p>, { PRESENTED_IMAGE_SIMPLE: "simple" }),
    Alert: ({ message }: { message: ReactNode }) => <div role="alert">{message}</div>
  };
});
vi.mock("../../../components/ResponsiveTable", () => ({ ResponsiveTable: () => <table /> }));
vi.mock("../../../components/StatusTag", () => ({ StatusTag: ({ children }: { children: ReactNode }) => <span>{children}</span> }));

let root: Root; let host: HTMLDivElement;
beforeEach(() => { Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
const button = (label: string) => [...host.querySelectorAll("button")].find(node => node.textContent === label);

describe("学生端发布状态行", () => {
  type Props = ComponentProps<typeof ScoreReleaseStatus>;
  const props = (overrides: Partial<Props> = {}): Props => ({
    model: { tone: "processing", text: "未发布 · 检查已通过", primary: { kind: "publish", label: "发布成绩" } },
    canWrite: true, actioning: null, onPrimary: vi.fn(), onExport: vi.fn(), exportBlocked: false,
    roster: { entries: [], summary: { expected: 45, received: 43, graded: 43, absent: 2, unresolved: 0, missing_pages: 0, unidentified: 0 } },
    rosterColumns: [], sessionText: "", activeRegrades: 0, historyCount: 0, onOpenHistory: vi.fn(), ...overrides
  });

  it("显示人数摘要；有缺考时提醒先排除漏扫", async () => {
    await act(async () => root.render(<ScoreReleaseStatus {...props()} />));
    expect(host.textContent).toContain("应考 45 · 收到答卷 43 · 按缺考计 2 人");
    expect(host.textContent).toContain("确认没有漏扫或未识别的答卷");
    expect(button("发布成绩")?.disabled).toBe(false);
  });

  // “还有 N 项待处理”只滚动定位，只读或重读期间也可用；发布和导出必须有写权限。
  it("定位待办不受写权限限制，发布和导出需要写权限", async () => {
    const onPrimary = vi.fn();
    await act(async () => root.render(<ScoreReleaseStatus {...props({ canWrite: false, onPrimary, model: { tone: "danger", text: "未发布 · 还有 2 项待处理", primary: { kind: "locate", label: "还有 2 项待处理" } } })} />));
    await act(async () => button("还有 2 项待处理")!.click());
    expect(onPrimary).toHaveBeenCalledTimes(1);
    await act(async () => root.render(<ScoreReleaseStatus {...props({ canWrite: false })} />));
    expect(button("发布成绩")?.disabled).toBe(true);
    await act(async () => root.render(<ScoreReleaseStatus {...props({ exportBlocked: true, sessionText: "同场 3 科已发布 2 科，还差物理", model: { tone: "success", text: "已发布 V1", primary: { kind: "export", label: "导出教师成绩表" } } })} />));
    expect(button("导出教师成绩表")?.disabled).toBe(true);
    expect(host.textContent).toContain("还差物理");
  });

  it("有待发布更正时主按钮是发布更正，导出降为次要按钮", async () => {
    const onExport = vi.fn();
    await act(async () => root.render(<ScoreReleaseStatus {...props({ onExport, model: { tone: "warning", text: "已发布 V1 · 有 1 项更正待发布", primary: { kind: "correction", label: "发布更正" } } })} />));
    expect(button("发布更正")).toBeDefined();
    await act(async () => button("导出教师成绩表")!.click());
    expect(onExport).toHaveBeenCalledTimes(1);
  });
});

describe("发布记录", () => {
  const release = (id: string, version: number, status: string) => ({ id, version, status, source: "initial", reason: "首次发布本场成绩", visibility_policy: { show_high_score_paper: false }, appeal_window: { enabled: false }, created_at: "2026-10-31T10:00:00Z", published_at: status === "published" ? "2026-10-31T11:00:00Z" : null }) as unknown as ScoreRelease;

  it("发布前检查未通过时草稿不能发布，并说明原因；当前版本单独标出", async () => {
    const current = release("r1", 1, "published");
    const render = (canPublish: boolean) => root.render(<ScoreReleaseHistory open onClose={vi.fn()} releases={[current, release("r2", 2, "draft")]} currentRelease={current}
      canWrite canPublish={canPublish} actioning={null} onPreview={vi.fn()} onPublish={vi.fn()} onRevokePaper={vi.fn()} onRepublish={vi.fn()} />);
    await act(async () => render(false));
    expect(host.textContent).toContain("学生端当前版本");
    expect(host.textContent).toContain("草稿 · 学生看不到");
    expect(host.textContent).toContain("发布前检查尚未通过，暂不能发布草稿。");
    expect(button("发布此版本")?.disabled).toBe(true);
    await act(async () => render(true));
    expect(button("发布此版本")?.disabled).toBe(false);
    expect(button("按当前成绩重新发布")?.disabled).toBe(false);
  });
});
