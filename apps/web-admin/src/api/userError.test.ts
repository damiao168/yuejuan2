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
    ["password_change_required", "首次登录需要先修改学校发放的初始密码。"],
    ["initial_password_confirmation_required", "请填写初始密码和确认密码，并确保两次输入一致。"],
    ["current_password_invalid", "当前密码不正确，请重新输入。"],
    ["password_unchanged", "新密码不能与当前密码相同。"],
    ["password_changed_concurrently", "密码已在其他会话修改，请退出后使用新密码重新登录。"],
    ["invalid_web_search_assessment", "本次搜索结果未能核实，请补充问题或稍后重试。"],
    ["invalid_web_search_selection", "暂时无法选择需要读取的网页，请补充具体问题后重试。"],
    ["invalid_web_search_action", "模型处理联网查询时返回异常，请重试或切换模型。"],
    ["invalid_web_search_citation", "回答中的引用未通过核验，请重试。"],
    ["invalid_web_search_query", "请输入不超过500字的公开问题。"],
    ["web_search_unavailable", "联网搜索暂不可用，请关闭联网搜索后提问。"],
    ["web_search_failed", "公开资料搜索未完成，请稍后重试。"],
    ["web_search_timeout", "联网搜索处理超时，请重试或切换模型。"],
    ["ai_chat_timeout", "模型响应超时，请重试或切换模型。"],
    ["ai_chat_model_timeout", "模型响应超时，请重试或切换模型。"],
    ["ai_chat_budget_exceeded", "本次问题需要的处理步骤过多，请分成更具体的问题。"],
    ["ai_chat_tools_unsupported", "当前模型不支持校内查询，请切换模型或联系管理员。"],
    ["grade_cohort_exists", "该入学届别已存在，请直接编辑已有届别，不要重复创建。"],
    ["grade_level_year_mismatch", "当前年级与入学年份、学校当前学年不一致，请刷新后按提示校正。"],
    ["grade_level_conflict", "该当前年级已被其他届别占用，请先核对并校正已有届别。"],
    ["grade_entry_year_future", "该届别尚未入学，不能创建为当前在读年级。"],
    ["grade_cohort_graduated", "该届别已超出当前学段的三年范围，不能创建或调整为当前在读年级。"],
    ["grade_academic_year_mismatch", "所填学年与学校当前学年不一致，请刷新后重试。"],
    ["grade_historical_record_immutable", "这是历史学年记录，请编辑该届别当前的年级。"],
    ["score_release_gate_blocked", "发布前仍有待处理事项，请查看本场成绩的发布检查并完成处理后再发布。"],
    ["review_assignee_ineligible", "请选择本校任教该考试学科的有效阅卷教师（学校管理员除外）。"],
    ["regrade_assignee_ineligible", "请选择本校任教该考试学科的有效阅卷教师（学校管理员除外）。"],
    ["backmark_assignee_ineligible", "请选择本校任教该考试学科的有效阅卷教师（学校管理员除外）。"],
    ["calibration_assignee_ineligible", "请选择本校任教该考试学科的有效阅卷教师（学校管理员除外）。"],
    ["calibration_retake_cooldown", "本题试评未通过，须在上次试评完成24小时后重新参加。"],
    ["capture_collection_incomplete", "请先结束本场答卷导入，核对缺考名单后再发布。"],
    ["question_appeal_restart_shared_job", "该复评任务涉及其他学生或申请，不能单独重新处理本申请，以免取消他人任务。"],
    ["appeal_assignee_ineligible", "请选择与本场考试学科一致、已启用的本校复核教师。"],
    ["invalid_teaching_subjects", "请至少选择一门有效的任教学科。"],
    ["score_release_stale_source", "当前正式成绩已更新，请以最新正式版本重新建立复评和发布草稿。"],
    ["regrade_source_release_invalid", "复评来源不再是本场可用的正式成绩版本，请核对最新正式版本后重新预览。"],
    ["regrade_no_affected_items", "当前范围没有匹配的已发布答卷，请核对题目、学生范围和来源版本。"],
    ["calibration_gold_set_incomplete", "已批准标准卷不足，请在标准卷管理中补足本题试评样本后重试。"],
    ["calibration_template_forbidden", "请有本校管理权限的阅卷管理员批准或撤回学校试评标准。"],
    ["calibration_template_unavailable", "所选学校试评标准已撤回、更新或不再适合本题，请重新选择，当前输入已保留。"],
    ["calibration_template_context_missing", "尚未确定本题作答类型或满分，请先核对题目配置后再使用学校标准。"],
    ["calibration_conflict", "本题试评标准已更新，请保留当前输入，重新读取后核对再保存。"],
    ["seed_quality_conflict", "盲投标准卷已变化，请重新读取并确认本题盲投策略；普通阅卷可继续。"],
    ["seed_gold_set_missing", "本题缺少可用的盲投标准卷，请补齐并重新确认盲投策略；普通阅卷可继续。"],
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
    ["managed_user_access_conflict", "该成员的职责已被更新，请刷新后重新核对授权，当前填写内容已保留。"],
    ["readiness_configuration_changed", "考试准备内容已变化，请重新核对名单和评分方式后确认。"],
    ["exam_not_collecting", "当前考试尚未进入答卷采集阶段，请先完成考试准备并开始采集。"],
    ["capture_duplicate_file", "该文件已加入当前批次，无需重复导入。"],
    ["unsupported_file_type", "文件内容与格式不匹配，或包含无法识别的隐藏字符。请使用 UTF-8 文本、PDF、Word 或图片后重试。"],
    ["student_no_conflict", "该学号已存在，请更换学号。"],
    ["class_code_conflict", "该年级中的现有或历史班级已使用过此代码，请更换班级代码。"],
    ["username_exists", "该登录账号已被使用，请更换账号。"],
    ["last_school_admin", "每所学校必须保留至少一位启用的学校管理员，请先添加或恢复另一位管理员。"],
    ["invalid_role_binding", "所选角色与所属学校不匹配，请重新选择。"]
  ])("maps %s to Chinese", (code, expected) => {
    expect(getUserErrorMessage(new ApiClientError(409, code, "English backend message"))).toBe(expected);
  });

  it("uses the HTTP fallback for an unknown conflict code", () => {
    expect(getUserErrorMessage(new ApiClientError(409, "new_conflict", "resource changed")))
      .toBe("当前数据状态已发生变化，请刷新后重试。");
  });

  it.each([
    [409, "formal_export_incomplete", "本场仍有学科未正式发布，或冻结名册不完整，请先完成发布并核对考试名册。"],
    [403, "formal_export_scope_required", "正式成绩表需要本校完整的成绩管理权限，请联系学校管理员。"],
    [503, "formal_export_unavailable", "正式成绩导出暂不可用，请稍后重试。"],
    [503, "formal_export_audit_unavailable", "本次导出记录未能保存，文件尚未下载，请稍后重试。"],
    [413, "student_import_too_large", "名单内容超过4MB，请分批导入。"],
    [413, "student_import_too_many_rows", "每次最多导入10000名学生，请分批处理。"],
    [503, "student_import_atomic_unavailable", "当前服务不支持整批学生导入，请联系管理员更新服务后重试。"],
    [500, "student_import_failed", "导入结果暂时无法确认，请刷新学生列表后重试；相同记录会自动跳过。"],
    [400, "invalid_csv", "名单CSV格式有误，请检查列数和引号，本次没有导入任何学生。"]
  ])("shows the actionable import/export error %s %s through the real client", async (status, code, expected) => {
    const originalFetch = globalThis.fetch;
    globalThis.fetch = async () => response({ error: { code, message: "internal-detail=synthetic-private-value" } }, status as number);
    try {
      // 下载也走受控错误码映射；不能把后端缺少正式版本的409退化成泛化的“刷新重试”。
      const client = new ApiClient({ baseUrl: "https://synthetic.example" });
      await expect(client.requestBlob("/api/v1/synthetic-export")).rejects.toMatchObject({ status, code, message: expected });
      expect(getUserErrorMessage(new ApiClientError(status as number, code as string, "internal-detail=synthetic-private-value"))).toBe(expected);
    } finally {
      globalThis.fetch = originalFetch;
    }
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
