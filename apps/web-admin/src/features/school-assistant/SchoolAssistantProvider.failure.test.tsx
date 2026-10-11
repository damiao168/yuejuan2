// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import { SchoolAssistantProvider, useSchoolAssistantContext } from "./SchoolAssistantProvider";

const mocks = vi.hoisted(() => ({ cancel: vi.fn() }));
vi.mock("./SchoolConversation", async () => { throw new Error("chunk download failed"); });
vi.mock("antd", () => ({
  Drawer: ({ open, children }: { open: boolean; children: ReactNode }) => open ? <aside>{children}</aside> : null,
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button onClick={onClick}>{children}</button>
}));
vi.mock("./useSchoolAssistant", async () => {
  const { useState } = await import("react");
  return { useSchoolAssistant: () => { const [context, setContext] = useState({}); return { context, setContext, cancel: mocks.cancel }; } };
});

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("keeps the current page and its unsaved input when the assistant chunk fails", async () => {
  // 模拟模块下载失败，而不是对话组件内部报错；只有包在 lazy 外侧的抽屉边界能保护主页面。
  vi.spyOn(console, "error").mockImplementation(() => undefined);
  const user = { id: "owner", tenant: "tenant", roles: ["school_admin"], permissions: [], publicComputer: false,
    organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] } } as unknown as SessionUser;
  function CurrentPage() {
    const assistant = useSchoolAssistantContext();
    return <><input aria-label="正在编辑的考试" defaultValue="未保存内容" /><button onClick={() => assistant?.open({ exam_id: "exam" })}>打开助手</button></>;
  }
  render(<SchoolAssistantProvider user={user} locationKey="exam" onNavigate={vi.fn()}><CurrentPage /></SchoolAssistantProvider>);
  await act(async () => fireEvent.click(screen.getByRole("button", { name: "打开助手" })));
  expect(screen.getByRole("alert").textContent).toContain("助手暂时无法加载");
  expect((screen.getByRole("textbox", { name: "正在编辑的考试" }) as HTMLInputElement).value).toBe("未保存内容");
  fireEvent.click(screen.getByRole("button", { name: "关闭助手" }));
  expect(screen.queryByRole("alert")).toBeNull();
  expect((screen.getByRole("textbox", { name: "正在编辑的考试" }) as HTMLInputElement).value).toBe("未保存内容");
});
