export const DEFAULT_USER_ERROR_MESSAGE = "操作失败，请稍后重试。";
export const NETWORK_USER_ERROR_MESSAGE = "网络连接异常，请检查网络后重试。";

const apiErrorMessages: Record<string, string> = {
  backmark_preview_stale: "回标范围或原评分已变化，请重新预览影响范围后再创建。",
  question_bank_invalid_input: "请检查题干、分值、题型和元数据，分值最多保留两位小数。",
  question_bank_revision_conflict: "草稿已变更或编号已被使用，请刷新核对后重试。",
  question_bank_content_locked: "题库已归档或内容已锁定，当前不能编辑。",
  question_bank_resource_not_found: "题库内容不存在或当前账号没有内容访问权限。",
  question_bank_access_denied: "当前账号没有该题库的访问权限。",
  question_bank_unavailable: "题库暂时不可用，请稍后重试。",
  invalid_request: "请检查必填项和填写格式。",
  exam_not_collecting: "当前考试尚未进入答卷采集阶段，请先完成考试准备并开始采集。",
  exam_not_found: "考试不存在或已被删除。",
  access_scope_missing: "当前账号没有访问这些数据的权限。",
  capture_not_found: "采集记录不存在或已被删除。",
  capture_invalid_input: "采集信息有误，请检查后重试。",
  capture_invalid_transition: "当前采集状态不能执行此操作，请刷新页面查看最新状态。",
  capture_revision_conflict: "采集记录已被其他操作更新，请刷新页面后重试。",
  capture_duplicate_file: "该文件已加入当前批次，无需重复导入。",
  capture_operation_failed: "采集操作失败，请稍后重试。",
  resource_version_conflict: "任务已被其他人更新，请刷新页面后重试。",
  operation_in_progress: "操作正在处理中，请稍后查看结果。",
  operation_outcome_unknown: "原操作结果暂时无法确认，请保留记录并稍后重试。",
  business_receipt_missing: "操作已收到成功响应，但业务结果记录异常，请联系管理员核对，暂勿重复提交。",
  business_command_payload_missing: "原操作缺少可恢复输入，请联系管理员核对，暂勿重复提交。",
  command_not_accepted: "原操作未被服务器接收，请返回业务页面重新提交。",
  ambiguous_command_identity: "同一操作标识关联了多个请求，请联系管理员核对，暂勿重复提交。",
  command_rejected: "原操作已被明确拒绝，请修改后重新提交。",
  csrf_validation_failed: "页面安全状态已失效，请刷新页面后重试。",
  idempotency_key_required: "本次操作缺少安全重试标识，请刷新页面后重试。",
  idempotency_key_reused_with_different_request: "这次操作内容已变化，请重新发起。",
  capability_unavailable: "自动处理暂时不可用，任务已保留，可稍后继续或转人工处理。",
  invalid_credentials: "学校或登录信息不正确。",
	login_rate_limited: "尝试次数过多，请稍后重试。",
  recent_auth_required: "为继续执行关键操作，请先验证当前账号密码。",
  reauthentication_failed: "验证失败，请检查当前账号密码后重试。",
  auth_level_required: "此操作需要更高级别的身份验证。",
  mfa_unavailable: "身份验证器管理暂未开放或暂时不可用。",
  mfa_forbidden: "请在个人设备上登录后管理身份验证器。",
  mfa_verification_failed: "验证信息不正确、已使用或已过期，请重新验证。",
  mfa_already_enabled: "已设置身份验证器，请先通过验证关闭原验证器。",
  invalid_mfa_operation: "暂不支持此验证操作。",
  unauthenticated: "登录状态已失效，请重新登录。",
  forbidden: "当前账号没有执行此操作的权限。",
  organization_scope_forbidden: "所选学校或班级不在你的管理范围内，请重新选择。",
  invalid_parent_scope: "所选学校、年级或班级已不存在，请刷新后重新选择。",
  school_list_failed: "学校信息暂时无法加载，请刷新后重试。",
  academic_year_list_failed: "学年信息暂时无法加载，请刷新后重试。",
  grade_cohort_list_failed: "年级信息暂时无法加载，请刷新后重试。",
  grade_list_failed: "年级列表暂时无法加载，请刷新后重试。",
  class_list_failed: "班级列表暂时无法加载，请刷新后重试。",
  student_list_failed: "学生名单暂时无法加载，请刷新后重试。",
  student_enrollment_list_failed: "学生的班级记录暂时无法加载，请刷新后重试。",
  user_list_failed: "人员名单暂时无法加载，请刷新后重试。",
  role_list_failed: "可选人员角色暂时无法加载，请刷新后重试。",
  school_code_conflict: "该机构代码已被使用，请更换代码。",
  grade_school_invalid: "所选学校已不存在，请刷新后重新选择。",
  invalid_education_stage: "请选择初中或高中学段。",
  class_code_conflict: "该年级中已存在相同的班级代码，请更换代码。",
  class_grade_invalid: "所选年级与学校不匹配，请重新选择。",
  student_no_conflict: "该学号已存在，请更换学号。",
  student_class_invalid: "所选班级与学校不匹配，请重新选择。",
  student_not_found: "该学生已不存在，请刷新学生名单。",
  student_transfer_target_invalid: "所选学生或目标班级已不存在，请刷新后重新选择。",
  username_exists: "该登录账号已被使用，请更换账号。",
  identity_exists: "该手机号或教职工号已被使用。",
  invalid_phone: "请输入有效的手机号。",
  activation_invalid: "激活链接无效、已过期或已使用，请联系管理员重新生成。",
  activation_failed: "账号暂时无法激活，请稍后重试。",
  activation_reissue_forbidden: "当前账号不能重新生成该人员的邀请。",
  activation_reissue_failed: "暂时无法重新生成邀请，请稍后重试。",
  recovery_invalid: "恢复链接无效、已过期或已使用，请联系管理员重新生成。",
  credential_recovery_forbidden: "当前账号不能为该人员生成恢复链接。",
  credential_recovery_failed: "暂时无法恢复账号，请稍后重试。",
  weak_password: "请使用至少 15 个字符的长密码或密码短语。",
  role_not_assignable: "所选人员角色当前不可用，请重新选择。",
  role_assignment_forbidden: "当前账号不能创建该角色的人员。",
  invalid_role_binding: "所选角色与学校或班级不匹配，请重新选择。",
  invalid_teacher_binding: "所选人员不是在用的学科教师，请重新选择。",
  user_create_failed: "人员账号暂时无法创建，请稍后重试。",
  managed_user_not_found: "该账号已不存在，请刷新人员名单。",
  invalid_user_status: "请选择启用或停用状态。",
  user_status_forbidden: "当前账号不能启停该人员账号。",
  last_school_admin: "每所学校必须保留至少一位启用的学校管理员，请先添加或恢复另一位管理员。",
  user_status_update_failed: "账号状态暂时无法更新，请稍后重试。",
  teacher_bind_failed: "教师与班级暂时无法关联，请稍后重试。",
  school_create_failed: "机构暂时无法保存，请稍后重试。",
  grade_create_failed: "年级暂时无法保存，请稍后重试。",
  class_create_failed: "班级暂时无法保存，请稍后重试。",
  student_create_failed: "学生暂时无法添加，请稍后重试。",
  student_transfer_failed: "学生暂时无法调整班级，请稍后重试。",
  request_body_too_large: "填写内容过长，请精简后重试。",
  file_too_large: "上传内容过大，请调整文件后重试。",
  unsupported_media_type: "文件格式不受支持，请更换文件后重试。",
  unsupported_file_type: "文件内容与格式不匹配，或包含无法识别的隐藏字符。请使用 UTF-8 文本、PDF、Word 或图片后重试。",
  provider_unknown: "暂时无法识别模型供应商，请选择服务来源。",
  invalid_managed_model_api: "模型 API 配置不完整或接口地址不安全。",
  managed_model_api_conflict: "该学校已经配置了同一供应商。",
  managed_model_current_active_required: "当前使用的模型必须保持启用，请先切换模型或切回本地模型。",
  credential_invalid: "API Key 无效或已被禁用。",
  model_permission_denied: "API Key 没有访问该模型的权限。",
  model_not_found: "没有找到该模型，请检查模型名称。",
  provider_rate_limited: "供应商额度不足或请求频率受限。",
  provider_timeout: "模型服务连接超时，请稍后重试。",
  provider_unavailable: "当前无法连接模型供应商，请稍后重试。",
  provider_invalid_response: "模型供应商返回了无法识别的响应。",
  provider_error: "模型供应商暂时无法完成验证，请稍后重试。",
  invalid_endpoint: "模型接口地址无效。",
  capability_unsupported: "模型可以访问，但结构化输出兼容检测未通过。",
  managed_model_capability_required: "请先通过完整能力检测，再设为当前使用。",
  no_choices: "模型响应中没有可用结果。",
  empty_content: "模型返回内容为空。",
  output_truncated: "检测输出被截断，请重新检测或更换模型。",
  quick_probe_unsupported: "当前模型适配器不支持零 Token 快速检测。",
  content_filtered: "检测请求被模型内容策略拦截。",
  invalid_json: "模型返回内容不是合法 JSON。",
  schema_mismatch: "模型返回的 JSON 缺少必要字段或字段类型不正确。",
  semantic_mismatch: "模型返回的 JSON 内容与测试文本不一致。",
  response_format_unsupported: "当前模型接口不支持 JSON 输出模式。",
  generation_failed: "模型生成请求失败。",
  managed_model_validation_failed: "模型配置验证失败，请检查后重试。",
  ai_chat_model_unavailable: "当前没有可用的学校对话模型，请联系平台管理员。",
  invalid_ai_chat_request: "对话内容为空、过长或格式不正确。",
  ai_chat_rate_limited: "模型服务繁忙，请稍后重试。",
  ai_chat_provider_failed: "模型暂时无法回答，请稍后重试。"
};

const httpErrorMessages: Record<number, string> = {
  400: "提交内容有误，请检查后重试。",
  401: "登录状态已失效，请重新登录。",
  403: "当前账号没有执行此操作的权限。",
  404: "请求的内容不存在或已被删除。",
  409: "当前数据状态已发生变化，请刷新后重试。",
  413: "上传内容过大，请调整文件后重试。",
  415: "文件格式不受支持，请更换文件后重试。",
  422: "提交内容暂时无法处理，请检查设置后重试。",
  428: "请先完成身份验证，再重新执行该操作。",
  429: "操作过于频繁，请稍后再试。"
};

export function getApiErrorMessage(code: string | undefined, status: number | undefined): string {
  if (code && apiErrorMessages[code]) return apiErrorMessages[code];
  if (status && status >= 500 && status <= 599) return "系统暂时无法完成操作，请稍后重试。";
  if (status && httpErrorMessages[status]) return httpErrorMessages[status];
  return DEFAULT_USER_ERROR_MESSAGE;
}

export function getUserErrorMessage(error: unknown, fallback?: string): string {
  if (isApiClientError(error)) return getApiErrorMessage(error.code, error.status);
  if (isNetworkError(error)) return NETWORK_USER_ERROR_MESSAGE;
  if (isAbortError(error)) return "操作已取消，请重新发起。";
  if (error instanceof Error && /[\u3400-\u9fff]/u.test(error.message)) return error.message;
  return safeFallback(fallback);
}

export function getSafeUserText(value: unknown, fallback: string): string {
  return typeof value === "string" && /[\u3400-\u9fff]/u.test(value) ? value : fallback;
}

const paperImportMessageMap: Record<string, string> = {
  page_processing_failed: "页面处理失败",
  source_ocr_failed: "图片文字识别失败",
  paper_ocr_unavailable: "文字识别服务暂不可用",
  ai_parse_failed: "考试资料解析失败",
  source_text_unavailable: "资料文字读取失败",
  paper_import_no_sources: "已删除全部考试资料，请重新上传正确的资料"
};

/** 将试卷资料解析返回的机器错误码转换为可直接展示给教师的中文。 */
export function getPaperImportUserMessage(value: unknown, fallback = "考试资料识别失败，请检查资料后重试"): string {
  if (typeof value !== "string" || !value.trim()) return fallback;
  let message = value.trim().replace(/\bOCR\b/gi, "文字识别");
  for (const [code, translated] of Object.entries(paperImportMessageMap)) message = message.split(code).join(translated);
  message = message
    .replace(/扫描文档\s+文字识别\s+失败/gi, "扫描文档文字识别失败")
    .replace(/exam has no questions/gi, "试卷尚未配置题目")
    .replace(/question is missing question_no or question_type/gi, "题目缺少题号或题型")
    .replace(/question\s+([^\s]+)\s+is missing answer_area/gi, "第$1题缺少答题区域")
    .replace(/question total\s+[\d.]+\s+does not equal exam total\s+[\d.]+/gi, "题目总分与考试总分不一致");
  return /[\u3400-\u9fff]/u.test(message) ? message : fallback;
}

function isApiClientError(error: unknown): error is { status: number; code: string } {
  if (!error || typeof error !== "object") return false;
  const candidate = error as { name?: unknown; status?: unknown; code?: unknown };
  return candidate.name === "ApiClientError"
    && typeof candidate.status === "number"
    && typeof candidate.code === "string";
}

function isNetworkError(error: unknown): boolean {
  if (!(error instanceof TypeError)) return false;
  return /failed to fetch|fetch failed|network(?:error| request failed| disconnected)|load failed/i.test(error.message);
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function safeFallback(fallback: string | undefined): string {
  return fallback && /[\u3400-\u9fff]/u.test(fallback) ? fallback : DEFAULT_USER_ERROR_MESSAGE;
}
