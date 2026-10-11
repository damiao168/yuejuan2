// 后端扩展字段未经具体结构校验，不能把对象当作标签或数值展示成 [object Object]。
export function primitiveDisplayValue(value: unknown, fallback = "-"): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean" || typeof value === "bigint") return String(value);
  return fallback;
}
