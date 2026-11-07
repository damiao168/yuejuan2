import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";
import { clearStudentAppealDrafts } from "../appealDrafts";

// jsdom 不执行跨文档导航；只替换浏览器边界，URL生成和门户卸载仍由真实代码负责。
vi.mock("../loginEntry", async (importOriginal) => ({
  ...await importOriginal<typeof import("../loginEntry")>(),
  leaveForUnifiedLogin: vi.fn()
}));

// 各用例模拟独立的浏览器会话；单个用例里的卸载重登仍可验证真实草稿恢复。
beforeEach(() => {
  window.history.replaceState(null, "", "/");
  clearStudentAppealDrafts();
});
afterEach(() => cleanup());
