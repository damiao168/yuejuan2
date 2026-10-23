import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { managedModelDiscoveryErrorMessage, type ManagedAPIProbeResult, type ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { capabilityVerified, isTransientProbeError, textVerified } from "./modelConfig";

type ModelFailureNotice = { kind: "warning" | "error"; text: string };

export function isTransientModelAPIError(error: unknown) {
  if (error instanceof TypeError && /failed to fetch|fetch failed|network(?:error| request failed| disconnected)|load failed/i.test(error.message)) return true;
  if (!(error instanceof ApiClientError)) return false;
  // 保存失败的外层状态是网关校验结果；临时性必须依据内层供应商请求，不能把 400 参数错误当成可重试故障。
  const validation = error.payload.validation;
  return isTransientProbeError(validation?.error_code ?? error.code, validation ? validation.status_code : error.status);
}

export function modelDiscoveryFailure(error: unknown, hasSavedCredential: boolean): ModelFailureNotice {
  const temporarilyUnavailable = isTransientModelAPIError(error);
  return {
    kind: temporarilyUnavailable ? "warning" : "error",
    text: `${managedModelDiscoveryErrorMessage(error, hasSavedCredential)}${temporarilyUnavailable ? "（本次暂时无法验证，不代表 API Key 无效）" : ""}`
  };
}

export function modelSaveFailure(error: unknown, editing: ManagedModelAPIConfig | null): ModelFailureNotice {
  const temporarilyUnavailable = isTransientModelAPIError(error);
  return {
    kind: temporarilyUnavailable ? "warning" : "error",
    text: `${getUserErrorMessage(error, editing ? "配置更新失败" : "配置保存失败")}${temporarilyUnavailable ? "（本次暂时无法验证，已保留当前填写内容）" : ""}`
  };
}

export function modelProbeFailure(config: ManagedModelAPIConfig, result: ManagedAPIProbeResult): ModelFailureNotice {
  // 供应商暂不可用只表示本次未能验证，不能把模型或密钥显示为已失效。
  const temporarilyUnavailable = isTransientProbeError(result.error_code, result.status_code);
  return {
    kind: temporarilyUnavailable ? "warning" : "error",
    text: `${config.display_name}：${result.message}${temporarilyUnavailable ? "（本次暂时无法验证，不代表模型已失效）" : ""}`
  };
}

export function savedModelAccessDescription(config?: ManagedModelAPIConfig) {
  // 保存只做访问检查；聊天和评分分别使用独立结果，结构化未通过不能否定普通回复能力。
  if (config && capabilityVerified(config)) return "该配置已通过当前结构化输出检测。正式评分仍需要角色绑定、真实评测、人工批准和使用策略。";
  if (config && textVerified(config)) return "普通回复已验证，可开放给学校对话；评分和试卷解析仍需通过结构化输出检测。";
  return "此次保存未生成回复，尚未验证普通回复与结构化能力。普通回复检测会产生少量用量，通过后可开放给学校对话；评分和试卷解析需另做结构化输出检测。";
}
