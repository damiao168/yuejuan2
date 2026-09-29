import { getUserErrorMessage } from "../../api/userError";

export function isStructuredAnswer(value: unknown): value is unknown[] | Record<string, unknown> {
  return value !== null && typeof value === "object";
}

export function resolveImportAnswerValue(value: unknown, original: unknown): unknown {
  // 文本编辑结构化答案时必须解析回原来的数组或对象形态，不能把 JSON 文本原样当标准答案保存。
  if (!isStructuredAnswer(original) || isStructuredAnswer(value)) return value;
  if (typeof value !== "string") throw new Error("结构化标准答案必须保持原有数组或对象格式");
  try {
    const parsed: unknown = JSON.parse(value);
    if (!isStructuredAnswer(parsed) || Array.isArray(parsed) !== Array.isArray(original)) throw new Error();
    return parsed;
  } catch {
    throw new Error("结构化标准答案必须填写有效 JSON，并保留原有数组或对象格式");
  }
}

export function importAnswerShapeError(value: unknown, original: unknown): string | undefined {
  try {
    resolveImportAnswerValue(value, original);
    return undefined;
  } catch (error) {
    return getUserErrorMessage(error, "标准答案格式不正确");
  }
}
