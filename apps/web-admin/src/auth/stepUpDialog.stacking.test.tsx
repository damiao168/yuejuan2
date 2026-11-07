// @vitest-environment jsdom
import { App, Button, Modal } from "antd";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../api/client";
import { isStepUpCancelledError, StepUpDialog, StepUpProvider, useStepUp } from "./stepUpContext";

beforeEach(() => {
  const computed = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation(element => computed(element));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("业务弹窗中的身份验证", () => {
  it.each(["verify", "cancel"])("发布遇到428时，验证窗口在业务弹窗上方且%s可结束等待", async mode => {
    const action = vi.fn().mockRejectedValueOnce(new ApiClientError(428, "recent_auth_required", "需要近期认证")).mockResolvedValue("published");
    const reauthenticate = vi.fn().mockResolvedValue(undefined);
    const settled = vi.fn();
    function Publication() {
      const { runWithStepUp } = useStepUp();
      return <Modal open title="发布成绩" footer={null}><Button onClick={() => {
        void runWithStepUp({ reason: "正式发布成绩", action }).then(settled, settled);
      }}>发布</Button></Modal>;
    }
    render(<App><StepUpProvider onReauthenticate={reauthenticate}><Publication /><StepUpDialog accountLabel="当前管理员" /></StepUpProvider></App>);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /^发\s*布$/ })));
    // rc-util 在测试环境复用标题ID；按实际标题定位对应弹窗，不把测试ID冲突误认为产品行为。
    const authentication = screen.getByText("确认你的身份").closest<HTMLElement>('[role="dialog"]')!;
    const publication = screen.getByText("发布成绩").closest<HTMLElement>('[role="dialog"]')!;
    const authenticationLayer = Number(getComputedStyle(authentication.closest(".ant-modal-wrap")!).zIndex);
    const publicationLayer = Number(getComputedStyle(publication.closest(".ant-modal-wrap")!).zIndex);
    expect(authenticationLayer).toBeGreaterThan(publicationLayer);
    if (mode === "cancel") {
      await act(async () => fireEvent.click(within(authentication).getByRole("button", { name: /^取\s*消$/ })));
      expect(isStepUpCancelledError(settled.mock.calls[0][0])).toBe(true);
      expect(action).toHaveBeenCalledTimes(1);
    } else {
      fireEvent.change(within(authentication).getByPlaceholderText("当前账号密码"), { target: { value: "synthetic-password" } });
      await act(async () => fireEvent.click(within(authentication).getByRole("button", { name: "验证并继续正式发布成绩" })));
      expect(reauthenticate).toHaveBeenCalledWith("synthetic-password");
      expect(settled).toHaveBeenCalledWith("published");
      expect(action).toHaveBeenCalledTimes(2);
    }
    expect(settled).toHaveBeenCalledTimes(1);
  }, 20_000);
});
