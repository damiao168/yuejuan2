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
    ["ai_output_incomplete", "AI 建议未完成，请人工评分。"],
    ["template_vision_model_required", "尚未配置可用的答题卡多模态模型，请平台管理员在学校模型库分配答题卡识别用途。"],
    ["answer_sheet_model_required", "请选择已启用且已配置密钥的模型，用于答题卡多模态识别。"],
    ["answer_sheet_model_selection_changed", "答题卡模型分配已被更新，请刷新学校配置后重新操作。"],
    ["managed_model_credential_source_disabled", "密钥来源配置已停用，请先启用该配置，再使用已保存的密钥获取模型列表或添加模型。"],
    ["managed_model_credential_origin_changed", "更换 API 域名或端口时，请重新输入 API Key（可以使用原密钥）。"],
    ["managed_model_text_required", "请先通过普通回复检测，再设为日常对话模型。"],
    ["managed_model_capability_required", "请先通过结构化输出检测，再用于评分或试卷解析。"],
    ["panel_model_binding_unavailable", "阅卷角色配置服务暂不可用，请稍后重试。"],
    ["invalid_panel_model_binding", "阅卷角色配置不完整或已失效，请核对范围、评分模型和评分规则版本，必要时刷新绑定后重试。"],
    ["panel_model_capability_required", "请选择本校已启用且通过结构化输出检测的模型，再保存阅卷角色配置。"],
    ["panel_model_binding_changed", "阅卷角色配置已被其他管理员更新，请刷新绑定、核对新配置后重新操作。"],
    ["backmark_preview_stale", "回标范围或原评分已变化，请重新预览影响范围后再创建。"],
    ["managed_user_access_conflict", "该成员的职责或班级已被更新，请刷新后重新核对授权，当前填写内容已保留。"],
    ["readiness_configuration_changed", "考试准备内容已变化，请重新核对名单和评分方式后确认。"],
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

  it.each([
    [409, "answer_sheet_source_in_use", "此答题卡已锁定、已收录到模板库或已用于正式处理，不能删除。"],
    [503, "answer_sheet_source_delete_unavailable", "答题卡文件删除服务暂不可用，请稍后重试；当前文件和草稿已保留。"],
    [503, "answer_sheet_page_order_unavailable", "答题卡自动页序分析暂不可用，请稍后重试，或上传按正确页序排列的单个 PDF。"],
    [502, "answer_sheet_page_order_failed", "答题卡自动页序分析未完成，请重试，或上传按正确页序排列的单个 PDF。"],
    [409, "exam_frozen", "考试准备已确认并冻结，不能再修改答题卡文件或模板设置。"]
  ])("describes answer-sheet boundary %s %s without exposing upstream details", (status, code, expected) => {
    const error = new ApiClientError(status as number, code as string, "internal detail: token=private-value");
    expect(getUserErrorMessage(error)).toBe(expected);
    expect(getUserErrorMessage(error)).not.toContain("private-value");
  });

  it("identifies role save failures before applying the generic 5xx message", () => {
    expect(getUserErrorMessage(new ApiClientError(500, "panel_model_binding_failed", "pq: internal statement failure")))
      .toBe("阅卷角色配置暂时无法保存，请稍后重试。");
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
