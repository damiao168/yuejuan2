import type { AiGrade } from "../../../api/review";
import type { RubricPoint } from "../../../api/papers";

/** 只有同版、完整且逐项 ID 可核对的建议才能填入评分点；总分或文字相似不能代替绑定。 */
export function aiPointAdoption(grade: AiGrade, points: RubricPoint[], rubricVersion?: string): Record<string, number> | null {
  if (!points.length || !rubricVersion || grade.rubric_version !== rubricVersion) return null;
  const allowed = new Map(points.map((point) => [point.id, point.score]));
  const selections: Record<string, number> = {};
  for (const [items, matched] of [[grade.matched_points ?? [], true], [grade.missing_points ?? [], false]] as const) {
    for (const item of items) {
      // 项目目标为 ES2020；仅检查本轮写入的自有评分点，不能把原型属性当作已匹配。
      if (!allowed.has(item.code) || Object.getOwnPropertyDescriptor(selections, item.code) !== undefined || !Number.isFinite(item.score)
        || item.score < 0 || item.score > allowed.get(item.code)!) return null;
      selections[item.code] = matched ? item.score : 0;
    }
  }
  if (Object.keys(selections).length !== points.length) return null;
  const total = Object.values(selections).reduce((sum, value) => sum + value, 0);
  return Math.abs(total - grade.suggested_score) <= 0.001 ? selections : null;
}
