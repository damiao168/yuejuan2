import type { ComponentProps } from "react";
import { Alert } from "antd";
import type { AiGrade } from "../../../../api/review";
import { getSafeUserText } from "../../../../api/client";
import { EmptyState, ErrorState, LoadingState } from "../../../../components/PageState";
import { sourceDescriptions, sourceLabels } from "../gradingWorkbench.model";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { requiresExplicitSecondOpinion } from "../reviewContext";
import { AnswerEvidencePane } from "./AnswerEvidencePane";
import { EvidenceInspector } from "./EvidenceInspector";
import { ScoreEditor } from "./ScoreEditor";
import { FrozenQuestionReference } from "./FrozenQuestionReference";

function emptyQueueDescription(hasTasks: boolean, canManageTasks: boolean): string {
  if (hasTasks) {
    return canManageTasks ? "从左侧队列选择需要检查的答卷。" : "从左侧任务队列选择一份答卷开始阅卷。";
  }
  return canManageTasks ? "当前没有待处理任务。" : "当前没有已分配任务，请等待管理员分配。";
}

interface ReasonBannerProps {
  context: WorkbenchContext;
  selectedGrade: AiGrade | undefined;
  subjectCode: string;
  canAdoptAiScore: boolean;
}

function WorkbenchReasonBanner({ context, selectedGrade, subjectCode, canAdoptAiScore }: Readonly<ReasonBannerProps>) {
  const description = context.warnings[0]
    ? getSafeUserText(context.warnings[0], "智能辅助暂时不可用，请人工确认。")
    : sourceDescriptions[context.task.source] ?? "请结合学生原始答案和评分细则完成确认。";
  return <section className="grading-reason-banner"><div><span>为什么需要我处理？</span><strong>{sourceLabels[context.task.source] ?? "本题需要人工确认"}</strong><p>{description}</p></div>{selectedGrade && !requiresExplicitSecondOpinion(context.reviewContext) ? <div><span>系统建议</span><strong>{subjectCode === "mathematics" && !canAdoptAiScore ? "待核对 / 已失效" : `${selectedGrade.suggested_score} / ${selectedGrade.max_score}`}</strong></div> : null}</section>;
}

interface WorkbenchTaskContentProps {
  context: WorkbenchContext | null;
  loading: boolean;
  error: string | null;
  hasTasks: boolean;
  canManageTasks: boolean;
  onRetry: () => void;
  selectedGrade: AiGrade | undefined;
  subjectCode: string;
  canAdoptAiScore: boolean;
  answer: Omit<ComponentProps<typeof AnswerEvidencePane>, "context">;
  evidence: Omit<ComponentProps<typeof EvidenceInspector>, "context">;
  editor: Omit<ComponentProps<typeof ScoreEditor>, "context">;
}

export function WorkbenchTaskContent({ context, loading, error, hasTasks, canManageTasks, onRetry, selectedGrade, subjectCode, canAdoptAiScore, answer, evidence, editor }: Readonly<WorkbenchTaskContentProps>) {
  if (loading) return <main className="grading-main-empty"><LoadingState label="正在加载答卷" /></main>;
  if (error) return <main className="grading-main-empty"><ErrorState message={error} onRetry={onRetry} /></main>;
  if (!context) return <main className="grading-main-empty"><EmptyState title="请选择一份答卷" description={emptyQueueDescription(hasTasks, canManageTasks)} /></main>;
  return (
    <main className="grading-main">
      {context.warnings.length > 0 ? <Alert type="warning" showIcon message="AI 辅助不可用" description={context.warnings.map((warning) => getSafeUserText(warning, "智能辅助暂时不可用")).join("；")} /> : null}
      <WorkbenchReasonBanner context={context} selectedGrade={selectedGrade} subjectCode={subjectCode} canAdoptAiScore={canAdoptAiScore} />
      <FrozenQuestionReference key={context.task.id} context={context} />
      <section className="grading-panels">
        <AnswerEvidencePane {...answer} context={context} />
        <aside className="grading-inspector">
          <EvidenceInspector {...evidence} context={context} />
          <ScoreEditor {...editor} context={context} />
        </aside>
      </section>
    </main>
  );
}
