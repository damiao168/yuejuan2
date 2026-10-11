// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import { SchoolAssistantProvider, useSchoolAssistantContext } from "./SchoolAssistantProvider";

const mocks = vi.hoisted(() => ({ loads: 0, cancel: vi.fn(), finish: undefined as undefined | (() => void) }));
vi.mock("./SchoolConversation", async () => {
  mocks.loads += 1;
  await new Promise<void>((resolve) => { mocks.finish = resolve; });
  return { SchoolConversation: ({ user }: { user: SessionUser }) => <div data-conversation>{user.id}</div> };
});
vi.mock("antd", () => ({ Drawer: ({ open, children, onClose }: { open: boolean; children: ReactNode; onClose: () => void }) => open ? <aside><button onClick={onClose}>关闭助手</button>{children}</aside> : null,
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button onClick={onClick}>{children}</button> }));
vi.mock("./useSchoolAssistant", async () => {
  const { useMemo, useState } = await import("react");
  return { useSchoolAssistant: () => {
    const [context, setContext] = useState({});
    return useMemo(() => ({ context, setContext, cancel: mocks.cancel }), [context]);
  } };
});

const user = { id: "owner", tenant: "tenant", roles: ["school_admin"], permissions: [], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] } } as unknown as SessionUser;
function OpenAssistant() {
  const assistant = useSchoolAssistantContext();
  return <button onClick={() => assistant?.open({ exam_id: "exam" })}>打开助手</button>;
}

describe("assistant drawer module lifecycle", () => {
  let root: Root | undefined;
  let host: HTMLDivElement;
  afterEach(async () => { if (root) await act(async () => root!.unmount()); host?.remove(); });

  it("loads only on authorized open, and never reveals a resolved old module after closing or changing account", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    const render = async (account = user, locationKey = "page") => act(async () => root!.render(
      <SchoolAssistantProvider user={account} locationKey={locationKey} onNavigate={vi.fn()}><OpenAssistant /></SchoolAssistantProvider>
    ));
    await render({ ...user, roles: ["teacher"] });
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
    expect(mocks.loads).toBe(0);
    await render();
    expect(mocks.loads).toBe(0);
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
    expect(mocks.loads).toBe(1);
    const loading = within(host).getByRole("status");
    expect(loading.tagName).toBe("OUTPUT");
    expect(loading.className).toBe("school-assistant-loading");
    expect(loading.style.display).toBe("block");
    expect(loading.getAttribute("aria-live")).toBe("polite");
    expect(loading.getAttribute("aria-atomic")).toBe("true");
    expect(loading.textContent).toContain("正在加载");
    await render({ ...user, id: "next-owner" });
    await act(async () => mocks.finish!());
    expect(host.querySelector("[data-conversation]")).toBeNull();
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
    expect(host.querySelector("[data-conversation]")?.textContent).toBe("next-owner");
    await render({ ...user, id: "next-owner" }, "new-page");
    expect(host.querySelector("[data-conversation]")).toBeNull();
    expect(mocks.cancel).toHaveBeenCalled();
  });

  it("keeps unchanged context stable and updates navigation and user when their props change", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    let current: ReturnType<typeof useSchoolAssistantContext>;
    function Probe() { current = useSchoolAssistantContext(); return null; }
    const firstNavigate = vi.fn();
    const nextNavigate = vi.fn();
    const render = async (account = user, onNavigate = firstNavigate) => act(async () => root!.render(
      <SchoolAssistantProvider user={account} locationKey="page" onNavigate={onNavigate}><Probe /></SchoolAssistantProvider>
    ));
    await render();
    const initial = current!;
    await render();
    expect(current!).toBe(initial);
    const nextUser = { ...user, displayName: "新的显示名称" };
    await render(nextUser, nextNavigate);
    expect(current!).not.toBe(initial);
    expect(current!.user).toBe(nextUser);
    mocks.cancel.mockClear();
    await act(async () => current!.navigate("/exams/exam"));
    expect(mocks.cancel).toHaveBeenCalledOnce();
    expect(nextNavigate).toHaveBeenCalledWith("/exams/exam");
    expect(firstNavigate).not.toHaveBeenCalled();
  });
});
