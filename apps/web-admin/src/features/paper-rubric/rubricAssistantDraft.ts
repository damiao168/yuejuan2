import type { Question } from "../../api/papers";
import type { RubricSolutionLine } from "./rubricSolutionLines";

export interface SuggestedPointDraft {
  id: string;
  description: string;
  score: number | null;
  steps: RubricSolutionLine[];
  note: string;
}

// 草稿存于页面工作流，配置回读短暂卸载展示区时仍可恢复；题目内容变化后旧来源不能复用。
export interface RubricAssistantDraft {
  questionKey: string;
  scope: string;
  method: "steps" | "ai";
  manual: { current: unknown };
  ai: { points: SuggestedPointDraft[]; issues: string[]; edited?: boolean; jobId?: string };
  segments?: RubricSolutionLine[];
  segmentHistory?: RubricSolutionLine[][];
  boundarySuggestion?: RubricSolutionLine[];
  boundaryJobId?: string;
}

// 保存细则会改变题目快照并重挂助手；同题只保留操作方式，旧建议与分段不能跟随新来源继续生效。
export function newRubricAssistantDraft(question: Question, previous: RubricAssistantDraft | null = null): RubricAssistantDraft {
  const questionKey = JSON.stringify([question.tenant_id, question.exam_id, question.id]);
  return {
    questionKey, scope: JSON.stringify(question), method: previous?.questionKey === questionKey ? previous.method : "steps",
    manual: { current: null }, ai: { points: [], issues: [] }
  };
}
