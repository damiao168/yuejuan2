// @vitest-environment jsdom
import { within } from "@testing-library/react";
import { expect, it } from "vitest";
import html from "../../index.html?raw";

it("React 启动前的加载提示使用原生状态输出并保留块布局和礼貌播报", () => {
  const page = new DOMParser().parseFromString(html, "text/html");
  // 可见性查询需要真实文档的窗口；挂载实际入口节点，断言结束后再清理。
  const root = document.importNode(page.getElementById("root")!, true);
  document.body.append(root);
  try {
    const status = within(root).getByRole("status");
    expect(status.tagName).toBe("OUTPUT");
    expect(status.textContent).toBe("正在加载 EduGrade，请稍候…");
    expect(status.getAttribute("aria-live")).toBe("polite");
    expect(status.getAttribute("aria-atomic")).toBe("true");
    expect(status.style.display).toBe("block");
  } finally {
    root.remove();
  }
});
