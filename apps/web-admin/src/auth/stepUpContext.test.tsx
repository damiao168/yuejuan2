// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { ApiClientError, RECENT_AUTH_REQUIRED_EVENT } from "../api/client";
import { isStepUpCancelledError, StepUpProvider, useStepUp } from "./stepUpContext";

const recentAuthError = () => new ApiClientError(428, "recent_auth_required", "recent authentication required");

describe("StepUpProvider", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useStepUp>;
  let reauthenticate: Mock<(password: string) => Promise<void>>;
  let renderProvider: (onReauthenticate?: (password: string) => Promise<void>) => Promise<void>;

  beforeEach(async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    reauthenticate = vi.fn<(password: string) => Promise<void>>().mockResolvedValue(undefined);
    function Probe() {
      current = useStepUp();
      return null;
    }
    renderProvider = async (onReauthenticate = reauthenticate) => act(async () => {
      root.render(<StepUpProvider onReauthenticate={onReauthenticate}><Probe /></StepUpProvider>);
    });
    await renderProvider();
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });

  it("returns immediately when the action succeeds", async () => {
    const action = vi.fn().mockResolvedValue("done");
    let result = "";
    await act(async () => { result = await current.runWithStepUp({ reason: "发布成绩", action }); });
    expect(result).toBe("done");
    expect(action).toHaveBeenCalledTimes(1);
    expect(reauthenticate).not.toHaveBeenCalled();
    expect(current.open).toBe(false);
  });

  it("keeps the context value on an unchanged parent render and uses the latest verification callback", async () => {
    const initial = current;
    await renderProvider();
    expect(current).toBe(initial);
    await act(async () => current.setPassword("synthetic-password"));
    expect(current).not.toBe(initial);
    expect(current.password).toBe("synthetic-password");
    const nextReauthenticate = vi.fn().mockResolvedValue(undefined);
    await renderProvider(nextReauthenticate);
    await act(async () => current.complete());
    expect(nextReauthenticate).toHaveBeenCalledWith("synthetic-password");
    expect(reauthenticate).not.toHaveBeenCalled();
    expect(current.password).toBe("");
    expect(current.feedback).toContain("刚才的操作尚未执行");
  });

  it("marks fallback verification as identity-only and asks the user to repeat the original action", async () => {
    await act(async () => { window.dispatchEvent(new Event(RECENT_AUTH_REQUIRED_EVENT)); await new Promise(resolve => setTimeout(resolve, 10)); });
    expect(current.open).toBe(true); expect(current.canResume).toBe(false);
    await act(async () => current.setPassword("synthetic-password"));
    await act(async () => current.complete());
    expect(reauthenticate).toHaveBeenCalledOnce(); expect(current.open).toBe(false);
    expect(current.feedback).toContain("刚才的操作尚未执行");
  });

  it("reauthenticates and retries a 428 action exactly once", async () => {
    const action = vi.fn().mockRejectedValueOnce(recentAuthError()).mockResolvedValueOnce("published");
    let pending!: Promise<string>;
    await act(async () => {
      pending = current.runWithStepUp({ reason: "正式发布成绩", action });
      await Promise.resolve();
    });
    expect(current.open).toBe(true);
    expect(current.reason).toBe("正式发布成绩");
    await act(async () => { current.setPassword("correct horse battery staple"); });
    await act(async () => { await current.complete(); });
    await expect(pending).resolves.toBe("published");
    expect(reauthenticate).toHaveBeenCalledTimes(1);
    expect(action).toHaveBeenCalledTimes(2);
    expect(current.open).toBe(false);
  });

  it("does not retry when password verification fails", async () => {
    reauthenticate.mockRejectedValueOnce(new ApiClientError(401, "invalid_credentials", "invalid"));
    const action = vi.fn().mockRejectedValue(recentAuthError());
    let pending!: Promise<unknown>;
    await act(async () => {
      pending = current.runWithStepUp({ reason: "回滚成绩", action });
      await Promise.resolve();
    });
    await act(async () => { current.setPassword("wrong password"); });
    await act(async () => { await current.complete(); });
    expect(action).toHaveBeenCalledTimes(1);
    expect(current.open).toBe(true);
    expect(current.error).toBeTruthy();
    const settled = pending.catch((error) => error);
    await act(async () => { current.cancel(); });
    expect(isStepUpCancelledError(await settled)).toBe(true);
  });

  it("cancels without retrying the action", async () => {
    const action = vi.fn().mockRejectedValue(recentAuthError());
    let pending!: Promise<unknown>;
    await act(async () => {
      pending = current.runWithStepUp({ reason: "恢复账号", action });
      await Promise.resolve();
    });
    const settled = pending.catch((error) => error);
    await act(async () => { current.cancel(); });
    expect(isStepUpCancelledError(await settled)).toBe(true);
    expect(action).toHaveBeenCalledTimes(1);
    expect(reauthenticate).not.toHaveBeenCalled();
  });

  it("stops when the single retry also returns 428", async () => {
    const action = vi.fn().mockRejectedValue(recentAuthError());
    let pending!: Promise<unknown>;
    await act(async () => {
      pending = current.runWithStepUp({ reason: "发布成绩", action });
      await Promise.resolve();
    });
    const settled = pending.catch((error) => error);
    await act(async () => { current.setPassword("correct horse battery staple"); });
    await act(async () => { await current.complete(); });
    const error = await settled;
    expect(error).toMatchObject({ status: 428, code: "recent_auth_required" });
    expect(action).toHaveBeenCalledTimes(2);
    expect(current.open).toBe(false);
  });

  it.each([403, 409, 500])("does not prompt for HTTP %s", async (status) => {
    const action = vi.fn().mockRejectedValue(new ApiClientError(status, "request_failed", "failed"));
    await expect(current.runWithStepUp({ reason: "发布成绩", action })).rejects.toMatchObject({ status });
    expect(current.open).toBe(false);
    expect(reauthenticate).not.toHaveBeenCalled();
  });
});
