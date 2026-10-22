// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ForbiddenState, LoadingState } from "./PageState";

vi.mock("antd", () => ({
  Alert: () => null,
  Empty: Object.assign(() => null, { PRESENTED_IMAGE_SIMPLE: "simple" }),
  Spin: () => null,
  Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button type="button" onClick={onClick}>{children}</button>,
  Result: ({ status, title, subTitle, extra }: { status: string; title: ReactNode; subTitle: ReactNode; extra?: ReactNode }) => <section data-status={status}><h1>{title}</h1><p>{subTitle}</p>{extra}</section>,
}));

describe("无权限页面的职责联系人", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    host.remove();
  });

  it("加载时显示内容轮廓，仅向读屏播报实际加载状态", async () => {
    await act(async () => { root.render(<LoadingState label="正在加载学生名单" />); });
    expect(host.querySelector('[aria-busy="true"]')).not.toBeNull();
    expect(host.querySelector('[role="status"]')?.textContent).toBe("正在加载学生名单");
    expect(host.querySelector('.page-loading-skeleton[aria-hidden="true"]')?.children.length).toBeGreaterThan(0);
  });

  it.each([
    { scope: "platform" as const, contact: "平台负责人" },
    { scope: "school" as const, contact: "学校管理员" },
    { scope: "exam" as const, contact: "本场考试负责人" },
    { scope: "general" as const, contact: "所属学校或平台的账号管理员" },
  ])("$scope 保持原联系人和返回提示", async ({ scope, contact }) => {
    await act(async () => { root.render(<ForbiddenState scope={scope} />); });
    expect(host.querySelector("section")?.getAttribute("data-status")).toBe("403");
    expect(host.querySelector("h1")?.textContent).toBe("当前账号没有访问权限");
    expect(host.querySelector("p")?.textContent).toBe(`请联系${contact}核对账号职责与权限。可以先返回工作台，继续使用已开放的功能。`);
    expect(host.querySelector("button")).toBeNull();
  });

  it("未指定范围时仍联系账号管理员，不增加返回按钮", async () => {
    await act(async () => { root.render(<ForbiddenState />); });
    expect(host.querySelector("p")?.textContent).toContain("请联系所属学校或平台的账号管理员");
    expect(host.querySelector("button")).toBeNull();
  });

  it("功能名称只替换标题，返回工作台仍调用原回调", async () => {
    const onBack = vi.fn();
    await act(async () => { root.render(<ForbiddenState scope="exam" feature="本场考试评分" onBack={onBack} />); });
    expect(host.querySelector("h1")?.textContent).toBe("暂时不能使用本场考试评分");
    const button = host.querySelector<HTMLButtonElement>("button")!;
    expect(button.textContent).toBe("返回工作台");
    await act(async () => { button.click(); });
    expect(onBack).toHaveBeenCalledTimes(1);
  });
});
