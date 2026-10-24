import { Button, Empty, List, Space, Tabs, Tooltip } from "antd";
import { BadgeCheck } from "lucide-react";
import { getSafeUserText } from "../../../../api/client";
import type { AiGrade } from "../../../../api/review";
import { StatusTag } from "../../../../components/StatusTag";
import { MathEvidenceInspector } from "../MathEvidenceInspector";
import { MathRubricMatrix } from "./MathRubricMatrix";
import type { useMathWorkbenchEvidence } from "../hooks/useMathWorkbenchEvidence";
import { mathSuggestionState } from "../mathWorkbenchEvidence";
import { ReviewContextInspector } from "../ReviewContextInspector";
import { imageSuggestionState, isImageWorkbenchGrade } from "../workbenchReviewState";
import { choiceReviewReason, isChoiceReview, requiresExplicitSecondOpinion } from "../reviewContext";
import {
  confidenceTone,
  evidenceTypeLabels,
  formatAnswer,
  recognitionDecisionLabels,
  recognitionSourceLabels,
  riskFlagLabels,
  sourceLabels
} from "../gradingWorkbench.model";
import type { ScoreDraft, WorkbenchContext } from "../gradingWorkbench.types";
import { ImageGradingRecord } from "./ImageGradingRecord";
import { GradingMathMarkdown } from "./GradingMathMarkdown";

export interface EvidenceInspectorProps {
  context: WorkbenchContext;
  draft: ScoreDraft;
  selectedGrade?: AiGrade;
  maxScore: number;
  canEditDraft: boolean;
  canVerifyEvidence: boolean;
  actioning: string | null;
  onVerifyEvidence: () => Promise<void>;
  math: ReturnType<typeof useMathWorkbenchEvidence>;
  onRefreshMath: () => void;
  onSelectMathStep: (stepId: string) => void;
  canRequestMath: boolean;
  mathDisabledReason?: string;
  requestingMath: boolean;
  onRequestMath: () => void;
  mathRequestNotice: string;
}

export function EvidenceInspector({
  context,
  draft,
  selectedGrade,
  maxScore,
  canEditDraft,
  canVerifyEvidence,
  actioning,
  onVerifyEvidence,
  math, onRefreshMath, onSelectMathStep, canRequestMath, mathDisabledReason, requestingMath, onRequestMath, mathRequestNotice
}: Readonly<EvidenceInspectorProps>) {
  const choice = isChoiceReview(context.reviewContext);
  // 图像评分直接展示当前建议；旧规则结果卡和手动证据校验不适用于这条评分链路。
  const imageGrade = isImageWorkbenchGrade(selectedGrade);
  const formulaEvidence = !choice && !imageGrade && ["mathematics", "physics", "chemistry"].includes(context.reviewContext.subject_tool_hints.subject_code);
  const automationSummary = () => {
    const result = context.automationResult;
    if (!result) {
      if (choice) {
        const current = context.reviewContext.ai_candidates?.find((candidate) => candidate.is_current);
        return <div className="task-automation-result"><div className="task-automation-grid">
          <div><span>识别选项</span><GradingMathMarkdown className="task-automation-answer">{current?.display_text || draft.answerText || "暂无识别结果"}</GradingMathMarkdown></div>
          <div><span>标准答案</span><GradingMathMarkdown className="task-automation-answer">{formatAnswer(context.question?.answer_key?.standard_answer)}</GradingMathMarkdown></div>
          <div><span>本题分值</span><strong>{maxScore} 分</strong></div>
        </div><p className="task-automation-note">{choiceReviewReason(context.reviewContext)}</p></div>;
      }
      return <div className="ocr-snippet"><span>识别文本</span><GradingMathMarkdown>{draft.answerText || "当前没有可用的识别结果"}</GradingMathMarkdown></div>;
    }
    const automaticallyConfirmed = result.grade_source === "rule_confirmed" && typeof result.score === "number";
    return (
      <div className="task-automation-result">
        <div className="task-automation-grid">
          <div><span>{choice ? "识别选项" : "识别答案"}</span><GradingMathMarkdown className="task-automation-answer">{result.recognized_answer || recognitionDecisionLabels[result.decision ?? ""] || "-"}</GradingMathMarkdown></div>
          <div><span>标准答案</span><GradingMathMarkdown className="task-automation-answer">{formatAnswer(result.standard_answer)}</GradingMathMarkdown></div>
          <div><span>置信度</span><strong>{typeof result.confidence === "number" ? `${Math.round(result.confidence * 100)}%` : "-"}</strong></div>
          <div><span>{choice ? "本题分值" : "自动判定"}</span><strong>{choice ? `${result.max_score ?? maxScore} 分` : automaticallyConfirmed ? `${result.score} / ${result.max_score ?? maxScore}` : "未生效 · 转人工"}</strong></div>
        </div>
        <div className="task-automation-note">
          {choice ? <span>{choiceReviewReason(context.reviewContext)}</span> : <><StatusTag tone="warning">{sourceLabels[context.task.source] ?? "人工复核"}</StatusTag>
          <span title={[result.source, result.decision].filter(Boolean).join(" / ") || undefined}>{recognitionSourceLabels[result.source ?? ""] ?? "识别服务"} · {recognitionDecisionLabels[result.decision ?? ""] ?? "规则未自动确认"}</span></>}
        </div>
      </div>
    );
  };

  const evidence = () => {
    if (!selectedGrade) return <div className="ai-empty-state"><strong>暂无 AI 建议</strong><span>请按评分细则人工判定。</span></div>;
    if (imageGrade) return <ImageGradingRecord grade={selectedGrade} />;
    const confidenceCalibrated = selectedGrade.confidence > 0;
    const mathState = imageGrade ? imageSuggestionState(selectedGrade, context) : formulaEvidence && context.reviewContext.subject_tool_hints.subject_code === "mathematics" ? mathSuggestionState(selectedGrade, math.state, math.dirty) : null;
    const decisionTone = selectedGrade.needs_human_review || selectedGrade.mock ? "warning" : confidenceTone(selectedGrade.confidence);
    let decisionLabel = "未标记风险";
    // 历史数学建议先说明不可采纳的原因；风险颜色仍按原人工复核与模拟标记判断。
    if (mathState && !mathState.current) decisionLabel = mathState.label;
    else if (selectedGrade.needs_human_review) decisionLabel = "需人工复核";
    return (
      <div className="evidence-stack">
        <div className="ai-score-strip">
          <div><span>{mathState && !mathState.current ? "历史建议 · 不可采纳" : "建议分"}</span><strong>{selectedGrade.suggested_score} / {selectedGrade.max_score}</strong></div>
          <div>
            <span>置信度</span>
            {confidenceCalibrated
              ? <strong>{Math.round(selectedGrade.confidence * 100)}%</strong>
              : <Tooltip title="该 AI 建议暂无可靠置信度，请以人工判断为准"><span><StatusTag tone="neutral">暂无置信度</StatusTag></span></Tooltip>}
          </div>
          <div>
            <span>判定</span>
            <StatusTag tone={decisionTone}>
              {decisionLabel}
            </StatusTag>
          </div>
        </div>
        <details className="ai-evidence-details">
          <summary><span>查看 AI 依据</span><small>{selectedGrade.matched_points.length} 个采分点 · {selectedGrade.evidence.length} 条证据 · {selectedGrade.risk_flags.length} 个风险</small></summary>
          <Tabs size="small" items={[
            {
              key: "points",
              label: "采分点",
              children: (
                <div className="point-result-list">
                  <List size="small" dataSource={selectedGrade.matched_points} locale={{ emptyText: <Empty description="暂无命中采分点" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }} renderItem={(point) => <List.Item><StatusTag tone="success">{`${point.score} 分`}</StatusTag><GradingMathMarkdown>{point.label || "未命名采分点"}</GradingMathMarkdown></List.Item>} />
                  <List size="small" dataSource={selectedGrade.missing_points} locale={{ emptyText: <Empty description="暂无缺失采分点" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }} renderItem={(point) => <List.Item><StatusTag tone="danger">{`${point.score} 分`}</StatusTag><GradingMathMarkdown>{point.label || "未命名采分点"}</GradingMathMarkdown></List.Item>} />
                </div>
              )
            },
            {
              key: "evidence",
              label: "证据",
              children: <List size="small" dataSource={selectedGrade.evidence} locale={{ emptyText: <Empty description="暂无证据" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }} renderItem={(item) => <List.Item><div className="evidence-item"><strong title={item.type || undefined}>{evidenceTypeLabels[item.type] ?? "证据"}</strong><GradingMathMarkdown>{item.answer_text || item.rule || item.standard_answer || "无文本证据"}</GradingMathMarkdown></div></List.Item>} />
            },
            {
              key: "risk",
              label: "风险",
              children: <Space wrap>{selectedGrade.risk_flags.length > 0 ? selectedGrade.risk_flags.map((flag) => <span key={flag} title={flag}><StatusTag tone="warning">{riskFlagLabels[flag] ?? "其他风险"}</StatusTag></span>) : <StatusTag tone="success">无风险标记</StatusTag>}</Space>
            }
          ]} />
        </details>
      </div>
    );
  };

  const evidenceJob = () => {
    const job = context.evidenceJob;
    if (!job) return null;
    return (
      <div className="evidence-job">
        <Space><StatusTag tone={job.result.passed ? "success" : "danger"}>{job.result.passed ? "通过" : "未通过"}</StatusTag>{job.needs_human_review ? <StatusTag tone="warning">需人工复核</StatusTag> : null}</Space>
        <List size="small" dataSource={[...job.result.failed, ...job.result.warnings]} locale={{ emptyText: <Empty description="无失败项或警告" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }} renderItem={(item) => <List.Item><span title={item.code}>{getSafeUserText(item.message, "校验未通过")}</span></List.Item>} />
      </div>
    );
  };

  return (
    <>
      {!choice ? <ReviewContextInspector key={context.task.id} context={context.reviewContext} /> : null}
      {formulaEvidence && context.reviewContext.subject_tool_hints.subject_code === "mathematics" && !requiresExplicitSecondOpinion(context.reviewContext) ? <MathRubricMatrix evidence={math.state} grades={context.aiGrades.filter((grade) => grade.delivery_mode !== "shadow_only")} points={context.question?.rubric?.points ?? []} dirty={math.dirty} requesting={requestingMath} canRequest={canRequestMath} disabledReason={mathDisabledReason} onRequest={onRequestMath} onSelectStep={onSelectMathStep} /> : null}
      {formulaEvidence && mathRequestNotice ? <output className="math-suggestion-note">{mathRequestNotice}</output> : null}
      {formulaEvidence ? <details className="grading-reference-details"><summary>公式与解题步骤证据</summary><MathEvidenceInspector key={`math-${context.task.id}`} segmentId={context.task.answer_segment_id} subjectCode={context.reviewContext.subject_tool_hints.subject_code} disabled={!canEditDraft || math.state.phase === "conflict" || math.state.phase === "unavailable"} data={math.state.understanding} loading={math.state.phase === "loading"} onRefresh={onRefreshMath} onCorrectionSaved={math.correctionSaved} onDirtyChange={math.setDirty} onSelectStep={onSelectMathStep} /></details> : null}
      {!imageGrade && !requiresExplicitSecondOpinion(context.reviewContext) ? automationSummary() : null}
      {choice ? <ReviewContextInspector key={context.task.id} context={context.reviewContext} /> : null}
      {selectedGrade && !choice ? evidence() : null}
      {evidenceJob()}
      {canVerifyEvidence && selectedGrade && !imageGrade ? (
        <Button icon={<BadgeCheck size={14} />} loading={actioning === "evidence"} onClick={() => void onVerifyEvidence()}>
          校验 AI 证据
        </Button>
      ) : null}
    </>
  );
}
