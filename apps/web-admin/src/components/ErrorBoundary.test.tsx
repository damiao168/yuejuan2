import type { ReactElement } from "react";
import type { ButtonProps } from "antd";
import { afterEach, expect, it, vi } from "vitest";
import { ErrorBoundary } from "./ErrorBoundary";

afterEach(() => vi.unstubAllGlobals());

it("故障恢复使用干净工作台路径并完整重载失效的模块", () => {
  const replaceState = vi.fn(), reload = vi.fn();
  const state = { key: "current-entry" };
  vi.stubGlobal("window", { history: { state, replaceState }, location: { reload } });
  const boundary = new ErrorBoundary({ children: null });
  boundary.state = { hasError: true };
  const result = boundary.render() as ReactElement<{ extra: ReactElement<ButtonProps>[] }>;
  const dashboard = result.props.extra.find((button) => button.key === "dashboard")!;
  dashboard.props.onClick!({} as Parameters<NonNullable<ButtonProps["onClick"]>>[0]);
  expect(replaceState).toHaveBeenCalledWith(state, "", "/dashboard");
  expect(reload).toHaveBeenCalledOnce();
});
