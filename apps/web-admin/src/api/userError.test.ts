import { describe, expect, it } from "vitest";
import zhCN from "antd/locale/zh_CN";
import { ApiClient, ApiClientError } from "./client";
import { getPaperImportUserMessage, getUserErrorMessage, NETWORK_USER_ERROR_MESSAGE } from "./userError";

function response(body: unknown, status: number, statusText = "") {
  return new Response(typeof body === "string" ? body : JSON.stringify(body), {
    status,
    statusText,
    headers: { "Content-Type": typeof body === "string" ? "text/plain" : "application/json" }
  });
}

describe("user-facing API errors", () => {
  it.each([
    ["backmark_preview_stale", "回标范围或原评分已变化，请重新预览影响范围后再创建。"],
    ["exam_not_collecting", "当前考试尚未进入答卷采集阶段，请先完成考试准备并开始采集。"],
    ["capture_duplicate_file", "该文件已加入当前批次，无需重复导入。"],
    ["unsupported_file_type", "文件内容与格式不匹配，或包含无法识别的隐藏字符。请使用 UTF-8 文本、PDF、Word 或图片后重试。"],
    ["student_no_conflict", "该学号已存在，请更换学号。"],
    ["class_code_conflict", "该年级中已存在相同的班级代码，请更换代码。"],
    ["username_exists", "该登录账号已被使用，请更换账号。"],
    ["last_school_admin", "每所学校必须保留至少一位启用的学校管理员，请先添加或恢复另一位管理员。"],
    ["invalid_role_binding", "所选角色与学校或班级不匹配，请重新选择。"]
  ])("maps %s to Chinese", (code, expected) => {
    expect(getUserErrorMessage(new ApiClientError(409, code, "English backend message"))).toBe(expected);
  });

  it("uses the HTTP fallback for an unknown conflict code", () => {
    expect(getUserErrorMessage(new ApiClientError(409, "new_conflict", "resource changed")))
      .toBe("当前数据状态已发生变化，请刷新后重试。");
  });

  it("never exposes raw 5xx messages or status text", async () => {
    const client = new ApiClient({ baseUrl: "https://example.test" });
    const fetchMock = (body: unknown, statusText: string) => {
      globalThis.fetch = async () => response(body, 500, statusText);
    };
    fetchMock({ error: { code: "unknown_backend", message: "Internal Server Error" } }, "Internal Server Error");
    await expect(client.request("/json")).rejects.toMatchObject({ message: "系统暂时无法完成操作，请稍后重试。" });
    fetchMock("Internal Server Error", "Internal Server Error");
    await expect(client.request("/text")).rejects.toMatchObject({ message: "系统暂时无法完成操作，请稍后重试。" });
  });

  it("maps fetch failures and unknown native errors without exposing Error.message", () => {
    expect(getUserErrorMessage(new TypeError("Failed to fetch"))).toBe(NETWORK_USER_ERROR_MESSAGE);
    expect(getUserErrorMessage(new Error("Unexpected English error"))).toBe("操作失败，请稍后重试。");
  });

  it("translates paper import and OCR machine messages to Chinese", () => {
    expect(getPaperImportUserMessage("扫描文档 OCR 失败：page_processing_failed"))
      .toBe("扫描文档文字识别失败：页面处理失败");
    expect(getPaperImportUserMessage("exam has no questions")).toBe("试卷尚未配置题目");
  });

  it("provides Chinese Ant Design modal defaults", () => {
    expect(zhCN.Modal?.cancelText).toBe("取消");
    expect(zhCN.Modal?.okText).toBe("确定");
  });
});
