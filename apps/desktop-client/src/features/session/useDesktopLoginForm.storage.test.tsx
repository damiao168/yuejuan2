// @vitest-environment jsdom
import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useDesktopLoginForm } from "./useDesktopLoginForm";
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it.each(["getter", "read"])("可选地址存储%s失败时保持默认服务器并允许手工登录", failure => {
  if (failure === "getter") vi.spyOn(window, "sessionStorage", "get").mockImplementation(() => { throw new DOMException("blocked", "SecurityError"); });
  else vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("read failed"); });
  const { result } = renderHook(() => useDesktopLoginForm("https://synthetic.example"));
  expect(result.current.serverUrl).toBe("https://synthetic.example"); expect(result.current.confirmedServerUrl).toBe("https://synthetic.example");
});
