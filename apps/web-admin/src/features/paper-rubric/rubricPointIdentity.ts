import type { RubricPoint } from "../../api/papers";

/** 采分点 ID 会进入评分证据；删除后新增不能复用数组长度生成的旧 ID。 */
export function newRubricPointId() {
  return `p-${crypto.randomUUID()}`;
}

export function rubricPointIdentityError(points: Pick<RubricPoint, "id">[]) {
  const seen = new Set<string>();
  for (const point of points) {
    if (!point.id || point.id !== point.id.trim()) return "采分点标识不能为空或包含首尾空格，请核对后重新创建该采分点";
    if (seen.has(point.id)) return "采分点标识重复，请核对并重新创建重复的采分点后再保存";
    seen.add(point.id);
  }
  return undefined;
}
