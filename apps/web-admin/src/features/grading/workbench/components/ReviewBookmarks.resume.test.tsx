// @vitest-environment jsdom
import { act, type ReactNode, type MouseEvent } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import { createReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import { ReviewBookmarksDrawer } from "./ReviewBookmarks";
import type { PersonalReviewBookmarkController } from "../hooks/usePersonalReviewBookmarks";
const navigation = vi.hoisted(() => vi.fn());

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  useQuery: () => ({ data: [] }),
  useInfiniteQuery: () => ({ data: { pages: [{ total_count: 4, items: [...["processable", "submitted", "archived"].map(state => ({ id: state, task_id: `task-${state}`, state, exam_id: "exam", exam_name: "合成考试", question_no: "1", anonymous_code: state })), { id: "other", task_id: "task/other", state: "processable", exam_id: "other-exam", exam_name: "另一场考试", question_no: "2" }] }] } })
}));
vi.mock("../../../../components/InternalRouteButton", () => ({ InternalRouteButton: ({ children, href, onClick }: { children: ReactNode; href: string; onClick: (event: MouseEvent<HTMLAnchorElement>) => void }) => <a href={href} onClick={event => { onClick(event); if (!event.defaultPrevented) navigation(href); event.preventDefault(); }}>{children}</a> }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ modal: { confirm: vi.fn() } }) },
  Drawer: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick?: () => void }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Input: () => null, Select: () => null, Spin: () => null
}));

it("只为仍可处理的标记提供继续阅卷，切换会话后旧按钮不能导航", async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  setBusinessCommandScope("tenant", "teacher");
  const host = document.createElement("div"); const root = createRoot(host); const resume = vi.fn();
  const scope = createReviewTaskContextScope();
  try {
    await act(async () => root.render(<ReviewBookmarksDrawer scope={scope} controller={{ busy: false } as PersonalReviewBookmarkController} open initialExamId="exam" onClose={() => undefined} onResumeTask={resume} />));
    const buttons = [...host.querySelectorAll("button")].filter(button => button.textContent === "继续阅卷");
    expect(buttons).toHaveLength(1);
    const crossExam = host.querySelector("a")!;
    expect(crossExam.getAttribute("href")).toBe("/grading?task=task%2Fother");
    const currentClick = new MouseEvent("click", { bubbles: true, cancelable: true });
    await act(async () => crossExam.dispatchEvent(currentClick));
    expect(navigation).toHaveBeenCalledWith("/grading?task=task%2Fother");
    await act(async () => buttons[0].click()); expect(resume).toHaveBeenCalledWith("task-processable");
    setBusinessCommandScope("tenant", "another-teacher");
    await act(async () => buttons[0].click()); expect(resume).toHaveBeenCalledOnce();
    const staleClick = new MouseEvent("click", { bubbles: true, cancelable: true });
    await act(async () => crossExam.dispatchEvent(staleClick));
    expect(staleClick.defaultPrevented).toBe(true);
    expect(navigation).toHaveBeenCalledOnce();
  } finally { await act(async () => root.unmount()); }
});
