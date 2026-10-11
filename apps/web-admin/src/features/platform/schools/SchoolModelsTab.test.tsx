// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { SchoolModelsTab } from "./SchoolModelsTab";
import { SchoolCreatedReceipt, schoolLoginURL } from "./SchoolCreatedReceipt";

const mocks = vi.hoisted(() => ({ health: vi.fn(), success: vi.fn(), warning: vi.fn() }));
vi.mock("../../../api/platformSchools", () => ({ getPlatformSchoolModelHealth: mocks.health }));
vi.mock("../../../components/ResponsiveTable", () => ({ ResponsiveTable: ({ dataSource }: { dataSource: { model_name: string }[] }) => <div>{dataSource.map((item) => <span key={item.model_name}>{item.model_name}</span>)}</div> }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: { success: mocks.success, warning: mocks.warning } }) },
  Alert: ({ message }: { message: string }) => <p>{message}</p>, Spin: () => <p>加载中</p>,
  Button: ({ children, href, onClick }: { children: ReactNode; href?: string; onClick?: () => void }) => href ? <a href={href}>{children}</a> : <button onClick={onClick}>{children}</button>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>
}));

let host: HTMLDivElement, root: Root;
beforeEach(() => {
  vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

it("无对话默认模型时仍显示已配置的三个评分角色", async () => {
  mocks.health.mockResolvedValue({ default_model: { status: "unconfigured" }, roles: [{ agent_role: "primary_a", model_name: "A-model", status: "healthy" }, { agent_role: "primary_b", model_name: "B-model", status: "healthy" }, { agent_role: "arbiter", model_name: "C-model", status: "healthy" }] });
  await act(async () => root.render(<SchoolModelsTab tenantId="school-b" />));
  expect(host.textContent).toContain("尚未指定学校默认对话模型");
  for (const model of ["A-model", "B-model", "C-model"]) expect(host.textContent).toContain(model);
  expect(host.querySelector('a[href$="tab=bindings"]')?.getAttribute("href")).toContain("tenant_id=school-b");
});

it("学校切换期间的旧请求不能覆盖新的评分角色", async () => {
  let resolveOld!: (value: unknown) => void;
  mocks.health.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; })).mockResolvedValueOnce({ default_model: { status: "unconfigured" }, roles: [{ agent_role: "primary_a", model_name: "new-model", status: "healthy" }] });
  await act(async () => root.render(<SchoolModelsTab tenantId="old" />));
  await act(async () => root.render(<SchoolModelsTab tenantId="new" />));
  await act(async () => resolveOld({ default_model: { status: "unconfigured" }, roles: [{ model_name: "old-model" }] }));
  expect(host.textContent).toContain("new-model"); expect(host.textContent).not.toContain("old-model");
});

it("交接入口预填学校，复制资料不含初始密码", async () => {
  const copy = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: copy } });
  expect(schoolLoginURL("school-a", { origin: "https://school.test", pathname: "/" })).toBe("https://school.test/?tenant=school-a");
  await act(async () => root.render(<SchoolCreatedReceipt delivery={{ tenant: { id: "school-a", name: "甲学校", code: "school-a", status: "active" }, adminUsername: "admin-a", adminDisplayName: "陈老师" }} onDone={() => undefined} />));
  await act(async () => [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === "复制登录资料")!.click());
  expect(copy).toHaveBeenCalledWith(expect.stringContaining("账号：admin-a"));
  expect(copy.mock.calls[0][0]).toContain("初始密码请通过约定的安全渠道单独交接");
  expect(host.querySelector("input[type=password]")).toBeNull();
});
