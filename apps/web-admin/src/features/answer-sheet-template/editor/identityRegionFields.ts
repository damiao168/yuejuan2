import type { LayoutRegion } from "../../../api/configuration";

export const IDENTITY_REGION_LABELS = ["姓名", "学号", "准考证号", "考号", "班级", "学校", "条码", "二维码", "身份信息"] as const;

/** 模板只保存字段种类和位置；未知标签不能把学生填写值带入可复用模板。 */
export function normalizeIdentityRegionLabel(label?: string | null): string {
  return IDENTITY_REGION_LABELS.find(value => value === label?.trim()) ?? "身份信息";
}

export function sameIdentityRegion(left: LayoutRegion, right: LayoutRegion): boolean {
  const leftLabel = normalizeIdentityRegionLabel(left.label), rightLabel = normalizeIdentityRegionLabel(right.label);
  // 同页同名字段优先保留已有人工框；泛称身份信息可能有多块，需有几何重合才合并。
  if (leftLabel === rightLabel && leftLabel !== "身份信息") return true;
  if (leftLabel !== rightLabel) return false;
  const width = Math.max(0, Math.min(left.x + left.width, right.x + right.width) - Math.max(left.x, right.x));
  const height = Math.max(0, Math.min(left.y + left.height, right.y + right.height) - Math.max(left.y, right.y));
  const smallerArea = Math.min(left.width * left.height, right.width * right.height);
  return smallerArea > 0 && width * height / smallerArea >= .8;
}
