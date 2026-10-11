// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ProcessingExceptionEvidence } from "./ProcessingExceptionEvidence";

const api = vi.hoisted(() => ({ context: vi.fn(), image: vi.fn() }));
vi.mock("../api/processing", () => ({ getProcessingExceptionContext: api.context, getProcessingExceptionImage: api.image }));
vi.mock("antd", () => ({
  Alert: ({ message, action }: { message: ReactNode; action?: ReactNode }) => <aside role="alert">{message}{action}</aside>,
  Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button onClick={onClick}>{children}</button>,
  Spin: () => <p>读取中</p>
}));

afterEach(cleanup);

it("身份未匹配且原图失败时保留页面材料，并编码定位到匹配工作区", async () => {
  const examId = "exam/A?mode=1";
  api.context.mockResolvedValue({ exception_context: {
    exception: { id: "exception", exam_id: examId, page_id: "source-page", code: "BLOCKED_MISSING_IDENTITY" },
    submission_id: "sub/A?x=1", candidate_no: "", page_no: 4, capture_batch_id: "batch A&1"
  } });
  api.image.mockRejectedValue(new Error("原图暂不可读"));
  const navigate = vi.fn();

  // 上下文先成功、原图再失败；材料和缺页定位入口仍需可用，不能被图像错误遮蔽。
  render(<ProcessingExceptionEvidence id="exception" examId={examId} onNavigate={navigate} />);
  await screen.findByText("原图暂不可读");
  expect(screen.getByText(/考号：尚未匹配.*第 4 页/)).toBeTruthy();
  expect(screen.getByText(/答卷：sub\/A\?x=1.*页面：source-page/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "打开本页处理工作区" }));
  expect(navigate).toHaveBeenCalledWith("/exams/exam%2FA%3Fmode%3D1/capture?batch=batch%20A%261&submission=sub%2FA%3Fx%3D1&page=&tab=matching");
});
