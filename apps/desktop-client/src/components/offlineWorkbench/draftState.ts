// 只转换内存中的草稿与冻结命令，不生成新命令 ID，也不执行加密或网络写入。
import type { OfflineGradeCommand, OfflineGradeDraft, OfflineTaskPackage } from '../../types';

export function draftFromCommand(command: OfflineGradeCommand): OfflineGradeDraft {
  const payload = command.payload;
  return { taskId: command.taskId, score: payload.score,
    rubricSelections: Object.fromEntries(payload.rubric_selections.map((selection) => [selection.point_id, selection.score])),
    comments: payload.comments, privateNote: payload.private_note, studentFeedback: payload.student_feedback, reason: payload.reason };
}

export function draftForDownloadedPackage(command: OfflineGradeCommand | undefined, previousDraft: OfflineGradeDraft, nextPackage: OfflineTaskPackage, sameTask: boolean, changedAssessment: boolean) {
  // 待确认命令优先恢复原输入；同一任务材料变化时只重置评分点，保留教师总分和评语。
  if (command) return draftFromCommand(command);
  if (!sameTask) return createInitialDraft(nextPackage);
  const nextDraft = { ...previousDraft };
  if (changedAssessment) {
    nextDraft.rubricSelections = Object.fromEntries((nextPackage.question?.rubric?.points ?? []).map((point) => [point.id, 0]));
  }
  return nextDraft;
}

export function packageDownloadMessage(confirmed: boolean, changedAssessment: boolean, sameTask: boolean) {
  if (confirmed) return "该任务已同步成功，服务端已接收人工评分。";
  if (changedAssessment) return "已保留人工总分与评语，评分点已按新材料清空；请重新核对评分依据后保存和提交。";
  if (sameTask) return "任务材料已重新下载，人工评分与评语保持原文。";
  return null;
}

export function createInitialDraft(pkg: OfflineTaskPackage): OfflineGradeDraft {
  const rubricSelections: Record<string, number> = {};
  for (const point of pkg.question?.rubric?.points ?? []) {
    rubricSelections[point.id] = 0;
  }
  return {
    taskId: pkg.task.id,
    // AI 只作为证据展示；教师显式采用前，最终分和学生反馈保持未填写状态。
    score: null,
    rubricSelections,
    comments: "",
    studentFeedback: "",
    privateNote: "",
    reason: ""
  };
}

export function emptyDraft(taskId: string): OfflineGradeDraft {
  return {
    taskId,
    score: null,
    rubricSelections: {},
    comments: "",
    studentFeedback: "",
    privateNote: "",
    reason: ""
  };
}
