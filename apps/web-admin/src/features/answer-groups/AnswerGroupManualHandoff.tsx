import { Alert, Button } from "antd";
import type { AnswerGroupMemberContext } from "../../api/answerGroups";

const taskStatusLabels: Record<string, string> = {
  pending: "待分派", assigned: "已指派", in_progress: "复核中", returned: "已退回待处理",
  submitted: "已提交", completed: "已完成", cancelled: "已取消", withdrawn: "已撤回"
};

export function AnswerGroupManualHandoff({ material, canReadReview, onNavigate }: Readonly<{
  material?: AnswerGroupMemberContext; canReadReview: boolean; onNavigate?: (path: string) => void;
}>) {
  if (!material) return <Alert type="warning" message="本样本已拒绝，仍需人工核对" description="请先点击“查看实际答题样本”，读取该份答卷的人工复核状态。" />;
  const task = material.manual_review;
  if (!task) return <Alert type="warning" message="本样本尚未关联人工复核任务" description="拒绝已记录，但本次操作不会自动新建复核任务。请学校阅卷管理员为本份答卷安排单份复核，再核对处理结果。" />;
  const finished = ["submitted", "completed", "cancelled", "withdrawn"].includes(task.status);
  let handoffDescription: string;
  if (finished) handoffDescription = "以上是读取材料时的任务状态；如仍需重新检查，请联系学校阅卷管理员。";
  else if (task.assigned) handoffDescription = "已指派的复核教师负责继续处理。学校阅卷管理员可核对分派与处理进度。";
  else handoffDescription = "学校阅卷管理员负责分派本任务，独立复核教师完成后再核对结果。";
  return <Alert type="info" message={`本份人工复核：${taskStatusLabels[task.status] ?? "状态待核对"}`} description={<>
    <p>{handoffDescription}</p>
    {/* 深链只定位服务端返回的真实任务；目标页面继续执行既有任务读取与个人范围检查。 */}
    {canReadReview && onNavigate ? <Button onClick={() => onNavigate(`/grading?task=${encodeURIComponent(task.task_id)}`)}>查看本份人工复核</Button> : <p>请联系学校阅卷管理员查看本份复核任务与处理进度。</p>}
  </>} />;
}
