import type { ReviewTask } from "../../../api/review";
import { examSubjectLabel } from "../../../constants/examStatus";

export interface AssignmentBatch { graderId: string; tasks: ReviewTask[] }
export interface AssignmentQuota { graderId: string; count: number }
// 弹窗只展示本地业务校验的文案；其他异常不能直接把原生 message 暴露给用户。
export class AssignmentPlanValidationError extends Error {
  constructor(readonly userMessage: string) { super(userMessage); this.name = "AssignmentPlanValidationError"; }
}
const naturalOrder = new Intl.Collator("zh-CN", { numeric: true });
function fixedOrder(left: string, right: string) { return left < right ? -1 : left > right ? 1 : 0; }
export function assignmentGroupKey(task: ReviewTask) { return JSON.stringify([task.exam_id, task.question_id]); }
export function assignmentGroups(tasks: ReviewTask[]) {
  const groups = new Map<string, ReviewTask[]>();
  for (const task of tasks) { const key = assignmentGroupKey(task); const group = groups.get(key) ?? []; group.push(task); groups.set(key, group); }
  return [...groups.entries()].map(([key, group]) => {
    const firstTask = group[0];
    const examLabel = firstTask.exam_name || `考试 ${firstTask.exam_id.slice(0, 8)}`;
    const subjectLabel = firstTask.subject ? examSubjectLabel(firstTask.subject) : "学科未标注";
    const shortLabel = `第 ${firstTask.question_no} 题`;
    const contextLabel = `${examLabel} · ${subjectLabel}`;
    return { key, tasks: group, label: `${contextLabel} · ${shortLabel}`, shortLabel, contextLabel };
  }).sort((left, right) => {
    const a = left.tasks[0], b = right.tasks[0];
    // 考试和学科按代码固定分组，题号按数值排列；同题内保留传入答卷顺序。
    return fixedOrder(a.exam_id, b.exam_id) || fixedOrder(a.subject ?? "", b.subject ?? "")
      || naturalOrder.compare(a.question_no, b.question_no) || fixedOrder(a.question_id, b.question_id);
  });
}

/** 分配预览保留任务 ID 和修订号；只使用明确选择的教师，资格仍由服务端校验。 */
export function buildAssignmentPlan(tasks: ReviewTask[], targets: Record<string, string[]>, defaultGraders: string[] = []): AssignmentBatch[] {
  const batches = new Map<string, ReviewTask[]>();
  const inheritedGraders = [...new Set(defaultGraders)];
  let inheritedOffset = 0;
  for (const group of assignmentGroups(tasks)) {
    // 缺少本题配置才继承统一教师；显式空数组表示暂不分配，不能被默认值覆盖。
    const overridden = Object.prototype.hasOwnProperty.call(targets, group.key);
    const graders = overridden ? [...new Set(targets[group.key])] : inheritedGraders;
    if (!graders.length) continue;
    // 继承题目共用轮转位置，避免每题一份时全给首位教师；单题覆盖独立均分，不消耗统一位置。
    const offset = overridden ? 0 : inheritedOffset;
    group.tasks.forEach((task, index) => {
      const grader = graders[(offset + index) % graders.length];
      const assigned = batches.get(grader) ?? [];
      assigned.push(task);
      batches.set(grader, assigned);
    });
    if (!overridden) inheritedOffset += group.tasks.length;
  }
  return [...batches].map(([graderId, tasks]) => ({ graderId, tasks }));
}

/** 自定义数量覆盖当前选中的全部任务；按自然题号和教师填写顺序分配，保留任务对象及修订号。 */
export function buildQuotaAssignmentPlan(tasks: ReviewTask[], quotas: AssignmentQuota[]): AssignmentBatch[] {
  const graders = new Set<string>();
  let total = 0;
  for (const quota of quotas) {
    if (!quota.graderId.trim()) throw new AssignmentPlanValidationError("请选择自定义分配的教师");
    if (graders.has(quota.graderId)) throw new AssignmentPlanValidationError("同一教师不能重复设置分配数量");
    if (!Number.isSafeInteger(quota.count) || quota.count < 0) throw new AssignmentPlanValidationError("每位教师的分配数量必须为 0 或正整数");
    graders.add(quota.graderId);
    total += quota.count;
  }
  // 先完整校验再生成计划；不能把少填、多填或重复任务悄悄截断成可提交的部分分配。
  if (!Number.isSafeInteger(total) || total !== tasks.length) throw new AssignmentPlanValidationError(`自定义分配数量合计必须等于待分配任务数（${tasks.length} 份）`);
  if (new Set(tasks.map(task => task.id)).size !== tasks.length) throw new AssignmentPlanValidationError("待分配任务重复，请刷新任务列表后重试");
  const ordered = assignmentGroups(tasks).flatMap(group => group.tasks);
  const batches: AssignmentBatch[] = [];
  let offset = 0;
  for (const quota of quotas) {
    if (quota.count === 0) continue;
    batches.push({ graderId: quota.graderId, tasks: ordered.slice(offset, offset + quota.count) });
    offset += quota.count;
  }
  return batches;
}
