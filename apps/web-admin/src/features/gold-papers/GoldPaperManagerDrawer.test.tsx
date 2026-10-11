// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Fragment, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GoldPaper, GoldPaperVersion } from "@edugrade/sdk";
import { GoldPaperManagerDrawer } from "./GoldPaperManagerDrawer";

const mocks = vi.hoisted(() => ({ list: vi.fn(), coverage: vi.fn(), approve: vi.fn(), create: vi.fn(), retire: vi.fn(), success: vi.fn(), error: vi.fn() }));
const context = vi.hoisted(() => ({ message: { success: mocks.success, error: mocks.error } }));
vi.mock("../../api/goldPapers", () => ({ listGoldPapers: mocks.list, getGoldCoverage: mocks.coverage, approveGoldPaperVersion: mocks.approve, createGoldPaperVersion: mocks.create, retireGoldPaper: mocks.retire }));
vi.mock("../../api/client", () => ({ apiClient: { url: (path: string) => `https://api.example${path}` }, getUserErrorMessage: (_error: unknown, fallback: string) => fallback }));
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
vi.mock("antd", () => {
  const passthrough = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  const item = ({ children, extra, onClick }: { children?: ReactNode; extra?: ReactNode; onClick?: () => void }) => <article onClick={onClick}>{children}{extra}</article>;
  const list = ({ dataSource = [], renderItem, header, locale }: { dataSource?: unknown[]; renderItem?: (value: unknown) => ReactNode; header?: ReactNode; locale?: { emptyText?: ReactNode } }) => (
    <div>{header}{dataSource.length ? dataSource.map((value, index) => <Fragment key={index}>{renderItem?.(value)}</Fragment>) : locale?.emptyText}</div>
  );
  return {
    App: { useApp: () => context },
    Alert: ({ message, description }: { message: ReactNode; description?: ReactNode }) => <p><span>{message}</span><span>{description}</span></p>,
    Button: ({ children, onClick, loading }: { children: ReactNode; onClick?: () => void; loading?: boolean }) => <button type="button" disabled={loading} onClick={onClick}>{children}</button>,
    Descriptions: Object.assign(passthrough, { Item: passthrough }),
    Drawer: ({ children, open, extra }: { children: ReactNode; open: boolean; extra?: ReactNode }) => open ? <div>{extra}{children}</div> : null,
    Empty: ({ description }: { description: ReactNode }) => <p>{description}</p>,
    Input: { TextArea: () => null }, InputNumber: () => null, Select: () => null,
    List: Object.assign(list, { Item: Object.assign(item, { Meta: ({ title, description }: { title: ReactNode; description: ReactNode }) => <div>{title}{description}</div> }) }),
    Modal: ({ children, open }: { children: ReactNode; open: boolean }) => open ? <aside>{children}</aside> : null,
    Popconfirm: ({ children, onConfirm }: { children: ReactNode; onConfirm: () => void }) => <div onClick={onConfirm}>{children}</div>,
    Space: passthrough, Tag: passthrough
  };
});

function version(number: number, approved = false): GoldPaperVersion {
  return {
    id: `version-${number}`, version: number, exam_question_snapshot_id: "snapshot",
    reference_score: number, max_score: 10, rubric_snapshot: { points: [{ id: "point", description: "冻结采分点", score: 2 }] },
    explanation: `版本 ${number} 的证据`, trait_scores: { point: 1 }, error_tags: [], source_grade_ids: ["source-grade"],
    nominated_by: "teacher", created_at: "2026-10-10", approved_at: approved ? "2026-10-10" : undefined
  };
}

function paper(overrides: Partial<GoldPaper> = {}): GoldPaper {
  return {
    id: "paper-1", exam_id: "exam-1", question_id: "question-1", submission_id: "submission",
    status: "active", subject_code: "math", archetype_code: "calculation", risk_tier: "high",
    nominated_by: "teacher", created_at: "2026-10-10", updated_at: "2026-10-10",
    active_version: 1, versions: [version(1, true), version(2)], ...overrides
  };
}

afterEach(cleanup);
beforeEach(() => {
  vi.resetAllMocks();
  mocks.coverage.mockResolvedValue({ coverage: { gaps: ["missing_zero_score"] } });
  mocks.approve.mockResolvedValue({});
});

function openDrawer() {
  return render(<GoldPaperManagerDrawer open examId="exam-1" questionId="question-1" onClose={vi.fn()} />);
}

describe("标准卷管理详情入口", () => {
  it.each([{ items: [] }, { items: [paper({ versions: [] })] }])("没有可查看版本时保留空状态", async ({ items }) => {
    mocks.list.mockResolvedValue({ gold_papers: items });
    openDrawer();
    await waitFor(() => expect(mocks.list).toHaveBeenCalledWith({ exam_id: "exam-1", question_id: "question-1" }));
    expect(await screen.findByText("选择一份标准卷查看审批证据")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "批准版本" })).toBeNull();
    expect(screen.queryByRole("button", { name: "退役" })).toBeNull();
  });

  it("存在活动旧版本时仍审批最新提名版本，并保留冻结证据与历史顺序", async () => {
    mocks.list.mockResolvedValueOnce({ gold_papers: [paper()] })
      .mockResolvedValueOnce({ gold_papers: [paper({ versions: [version(1, true), version(2, true)] })] });
    openDrawer();
    expect(await screen.findByText("标准卷覆盖仍有缺口")).toBeTruthy();
    expect(screen.getByText("缺少 0 分标准卷")).toBeTruthy();
    expect(screen.getByText("当前记录未提供答题图像地址")).toBeTruthy();
    expect(screen.getByText("冻结采分点")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "创建新版本" })).toBeNull();
    const history = screen.getAllByText(/^版本 \d 的证据$/).map((item) => item.textContent);
    expect(history).toEqual(["版本 2 的证据", "版本 2 的证据", "版本 1 的证据"]);

    // 测试中的 Popconfirm 直达确认回调，保护拆分后传入接口的标准卷及最新版本。
    fireEvent.click(screen.getByRole("button", { name: "批准版本" }));
    await waitFor(() => expect(mocks.approve).toHaveBeenCalledWith("paper-1", 2));
    expect(await screen.findByRole("button", { name: "创建新版本" })).toBeTruthy();
    expect(mocks.list).toHaveBeenCalledTimes(2);
    expect(mocks.success).toHaveBeenCalledWith("标准卷版本已批准");
  });

  it("退役记录仍展示版本证据，但隐藏审批、新建和退役入口", async () => {
    mocks.list.mockResolvedValue({ gold_papers: [paper({ status: "retired", versions: [version(1, true)], answer_image_url: "/answer.png" })] });
    mocks.coverage.mockResolvedValue({ coverage: { gaps: [] } });
    openDrawer();
    const image = await screen.findByRole("img", { name: "标准卷原始答题区域" });
    expect(image.getAttribute("src")).toBe("https://api.example/answer.png");
    expect(screen.getByText("冻结采分点")).toBeTruthy();
    expect(screen.getByText("当前题目标准卷覆盖规则已满足")).toBeTruthy();
    for (const name of ["批准版本", "创建新版本", "退役"]) {
      expect(screen.queryByRole("button", { name })).toBeNull();
    }
    expect(mocks.approve).not.toHaveBeenCalled();
  });
});
