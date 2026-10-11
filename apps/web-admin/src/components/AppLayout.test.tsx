// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../auth/session";
import { PUBLIC_COMPUTER_IDLE_LOCK_MS } from "../auth/loginSecurity";
import { routeFromPath } from "../router/routes";
import { AppLayout, type ShellMode } from "./AppLayout";

const state = vi.hoisted(() => ({ desktop: true, cancel: vi.fn() }));
vi.mock("../auth/stepUpContext", async () => {
  const { createElement } = await import("react");
  return {
    useStepUp: () => ({ cancel: state.cancel }),
    StepUpDialog: ({ hidden }: { hidden: boolean }) => createElement("span", { "data-step-up-hidden": String(hidden) })
  };
});
vi.mock("antd", async () => {
  const { createElement } = await import("react");
  const wrapper = ({ children, className, id, tabIndex }: { children?: ReactNode; className?: string; id?: string; tabIndex?: number }) =>
    createElement("div", { className, id, tabIndex }, children);
  return {
    Alert: ({ message }: { message: ReactNode }) => createElement("div", { role: "alert" }, message),
    Avatar: wrapper, ConfigProvider: wrapper, Dropdown: wrapper, Space: wrapper, Tag: wrapper, Tooltip: wrapper,
    Button: ({ children, htmlType, disabled, onClick, className }: {
      children?: ReactNode; htmlType?: "button" | "submit"; disabled?: boolean; onClick?: () => void; className?: string;
    }) => createElement("button", { type: htmlType ?? "button", disabled, onClick, className }, children),
    Drawer: ({ open, children }: { open: boolean; children: ReactNode }) => open ? createElement("aside", null, children) : null,
    Grid: { useBreakpoint: () => ({ lg: state.desktop }) },
    Input: { Password: ({ value, onChange }: { value: string; onChange: React.ChangeEventHandler<HTMLInputElement> }) =>
      createElement("input", { type: "password", value, onChange }) },
    Layout: Object.assign(wrapper, { Header: wrapper, Sider: wrapper, Content: wrapper }),
    Menu: () => null,
    Modal: ({ open, children }: { open: boolean; children: ReactNode }) => open ? createElement("div", { role: "dialog" }, children) : null,
    Segmented: () => null, theme: { defaultAlgorithm: {} }
  };
});

const user: SessionUser = {
  id: "user-1", username: "teacher-1", displayName: "Teacher", name: "Teacher", role: "teacher", roles: ["teacher"],
  tenant: "tenant-1", school: "School", currentExam: "", permissions: [], publicComputer: true,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: [], gradeIds: [], classIds: [] }
};

describe("shell public-computer lock ordering", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.useFakeTimers();
    state.desktop = true;
    state.cancel.mockReset();
    window.localStorage.clear();
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.useRealTimers();
  });

  async function render(onLockSession: () => Promise<void>, currentUser = user, shellMode: ShellMode = "standard") {
    await act(async () => root.render(<AppLayout user={currentUser} currentRoute={routeFromPath("/dashboard")}
      experience="teacher" availableExperiences={["teacher"]} onNavigate={vi.fn()} onExperienceChange={vi.fn()}
      onLogout={vi.fn()} onLockSession={onLockSession} onReauthenticate={async () => undefined} shellMode={shellMode}>
      <span>Workspace</span><input data-workspace-draft defaultValue="initial" />
    </AppLayout>));
  }

  it("covers the workspace and cancels step-up while the remote lock remains pending", async () => {
    // 网络锁定尚未返回时，界面已经遮挡；pagehide 不能再启动第二次锁定请求。
    let finishLock: (() => void) | undefined;
    const pending = new Promise<void>((resolve) => { finishLock = resolve; });
    const lock = vi.fn(() => pending);
    await render(lock);
    expect(host.querySelector("output.public-computer-banner")).not.toBeNull();
    await act(async () => vi.advanceTimersByTime(PUBLIC_COMPUTER_IDLE_LOCK_MS));
    expect(lock).toHaveBeenCalledTimes(1);
    expect(state.cancel).toHaveBeenCalledTimes(1);
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
    expect(host.querySelector('[data-step-up-hidden="true"]')).not.toBeNull();
    await act(async () => window.dispatchEvent(new Event("pagehide")));
    expect(lock).toHaveBeenCalledTimes(1);
    await act(async () => finishLock?.());
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
  });

  it("keeps the local lock after a remote rejection", async () => {
    await render(vi.fn(async () => { throw new Error("synthetic unavailable"); }));
    await act(async () => vi.advanceTimersByTime(PUBLIC_COMPUTER_IDLE_LOCK_MS));
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("暂时无法确认服务端锁定状态");
  });

  it("does not start public-computer locking for a private session", async () => {
    const lock = vi.fn(async () => undefined);
    await render(lock, { ...user, publicComputer: false });
    await act(async () => {
      vi.advanceTimersByTime(PUBLIC_COMPUTER_IDLE_LOCK_MS);
      window.dispatchEvent(new Event("pagehide"));
    });
    expect(lock).not.toHaveBeenCalled();
    expect(host.querySelector("output.public-computer-banner")).toBeNull();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it("removes activity and pagehide listeners when the shell unmounts", async () => {
    const lock = vi.fn(async () => undefined);
    await render(lock);
    await act(async () => root.render(null));
    await act(async () => {
      vi.advanceTimersByTime(PUBLIC_COMPUTER_IDLE_LOCK_MS);
      window.dispatchEvent(new Event("pagehide"));
    });
    expect(lock).not.toHaveBeenCalled();
  });

  it("keeps the mounted workspace draft when shell mode and breakpoint change", async () => {
    const lock = vi.fn(async () => undefined);
    const privateUser = { ...user, publicComputer: false };
    await render(lock, privateUser);
    const draft = host.querySelector<HTMLInputElement>("[data-workspace-draft]")!;
    draft.value = "unsaved draft";
    for (const desktop of [false, true]) {
      state.desktop = desktop;
      for (const mode of ["focused", "grading", "standard"] as const) {
        await render(lock, privateUser, mode);
        expect(host.querySelector("[data-workspace-draft]")).toBe(draft);
        expect(draft.value).toBe("unsaved draft");
      }
    }
  });
});
