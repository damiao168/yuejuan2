// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { GoldCoverageGaps } from "./GoldCoverageGaps";

const mocks = vi.hoisted(() => ({ questions: vi.fn(), coverage: vi.fn() }));
vi.mock("../../api/papers", () => ({ listQuestions: mocks.questions }));
vi.mock("../../api/goldPapers", () => ({ getGoldCoverage: mocks.coverage }));
vi.mock("antd", () => ({
  Alert: ({ message, description, action }: { message: ReactNode; description?: ReactNode; action?: ReactNode }) => <aside>{message}{description}{action}</aside>,
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button onClick={onClick}>{children}</button>,
  List: Object.assign(({ dataSource, renderItem }: { dataSource: unknown[]; renderItem: (value: unknown) => ReactNode }) => <div>{dataSource.map(renderItem)}</div>, { Item: ({ children }: { children: ReactNode }) => <div>{children}</div> }),
  Spin: () => <p>读取中</p>, Tag: ({ children }: { children: ReactNode }) => <span>{children}</span>
}));
beforeEach(() => { vi.resetAllMocks(); mocks.questions.mockResolvedValue({ questions: [{ id: "q1", question_no: "1" }] }); });
afterEach(cleanup);

it("覆盖接口失败显示无法判断，重试成功后才显示无缺口", async () => {
  mocks.coverage.mockRejectedValueOnce(new Error("网络失败")).mockResolvedValueOnce({ coverage: { gaps: [] } });
  render(<GoldCoverageGaps examId="exam" />);
  await screen.findByText("1 道题的标准卷覆盖暂时无法读取，尚不能判断是否存在缺口");
  expect(screen.queryByText(/尚无冻结评估快照/)).toBeNull();
  expect(screen.queryByText("标准卷覆盖暂无阻断缺口")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "重试覆盖检查" }));
  await screen.findByText("标准卷覆盖暂无阻断缺口");
  expect(mocks.coverage).toHaveBeenCalledTimes(2);
});

it("部分题失败仍保留已知缺口和区域重试", async () => {
  mocks.questions.mockResolvedValue({ questions: [{ id: "q1", question_no: "1" }, { id: "q2", question_no: "2" }] });
  mocks.coverage.mockImplementation(async (_exam, question) => {
    if (question === "q2") throw new Error("权限读取失败");
    return { coverage: { gaps: ["insufficient_samples"] } };
  });
  render(<GoldCoverageGaps examId="exam" />);
  await screen.findByText("1 道题的标准卷覆盖不完整；另有 1 道题无法判断");
  expect(screen.getByRole("button", { name: "重试覆盖检查" })).toBeTruthy();
  expect(screen.queryByText("标准卷覆盖暂无阻断缺口")).toBeNull();
});
