// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SchoolMembershipPage } from "./SchoolMembershipPage";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("学校会员中心", () => {
  it("显示三档参考报价和配额，下载不包含登录或学校信息", () => {
    render(<SchoolMembershipPage school="采购测试学校" />);
    expect(screen.getByRole("heading", { level: 1, name: "会员中心" })).toBeTruthy();
    const table = within(screen.getByRole("table", { name: "套餐对比" }));
    expect(table.getAllByRole("columnheader").map(heading => heading.textContent)).toEqual([
      "套餐", "试用版单班试点", "教研组版同学科多班协作", "学校版全校多学科统筹",
    ]);
    const download = screen.getByRole("link", { name: "下载报价" });
    expect(download.getAttribute("download")).toBe("阅卷平台-套餐参考报价.txt");
    const contents = decodeURIComponent(download.getAttribute("href")?.split(",")[1] ?? "");
    for (const item of ["试行参考报价", "¥0", "¥1,980", "¥9,800 起", "500 份", "6,000 份", "50,000 份", "学校可使用自有模型 API 密钥"]) {
      expect(contents).toContain(item);
    }
    expect(contents).not.toContain("采购测试学校");
  });

  it("剪贴板不可用时明确提示下载，并保留下载链接", async () => {
    const original = Object.getOwnPropertyDescriptor(navigator, "clipboard");
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: vi.fn().mockRejectedValue(new Error("permission denied")) } });
    try {
      render(<SchoolMembershipPage school="" />);
      fireEvent.click(screen.getByRole("button", { name: "复制报价" }));
      expect((await screen.findByRole("status")).textContent).toBe("当前浏览器无法复制，请下载报价。");
      expect(screen.getByRole("link", { name: "下载报价" })).toBeTruthy();
    } finally {
      if (original) Object.defineProperty(navigator, "clipboard", original);
      else Reflect.deleteProperty(navigator, "clipboard");
    }
  });
});
