import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach } from "vitest";
import { clearStudentAppealDrafts } from "../appealDrafts";

// 各用例模拟独立的浏览器会话；单个用例里的卸载重登仍可验证真实草稿恢复。
beforeEach(() => {
  window.history.replaceState(null, "", "/");
  clearStudentAppealDrafts();
});
afterEach(() => cleanup());
