import { Collapse, Descriptions, Empty, List } from "antd";
import type { AuditLog } from "../api/audit";
import type { Question, RubricPoint } from "../api/papers";
import type { ArbitrationTask } from "../api/review";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { formatScore, formatTime, pointLabel } from "./arbitrationPresentation.model";

type AiSuggestion = NonNullable<ArbitrationTask["context"]>["ai_suggestion"];

const auditActionLabels: Record<string, string> = {
  "arbitration.task_created": "仲裁任务创建",
  "arbitration.task_assigned": "仲裁任务分配",
  "arbitration.submitted": "仲裁结果提交",
  "final_grade.created": "最终分写入",
  "review.double_mark_auto_finalized": "双评自动定分"
};

const auditTargetLabels: Record<string, string> = {
  arbitration_task: "仲裁任务",
  final_grade: "最终分"
};

export function ArbitrationContextTab({ task }: { task: ArbitrationTask }) {
  return (
    <div className="arbitration-text-grid">
      <div>
        <strong>原始答案</strong>
        <pre>{task.context?.raw_answer || "暂无原始作答内容"}</pre>
      </div>
      <div>
        <strong>识别文本</strong>
        <pre>{task.context?.ocr_text || "暂无识别文本"}</pre>
      </div>
    </div>
  );
}

export function ArbitrationAiTab({ suggestion }: { suggestion: AiSuggestion }) {
  if (!suggestion || Object.keys(suggestion).length === 0) {
    return <EmptyState title="暂无 AI 建议" description="该任务没有 AI 评分建议。" />;
  }
  const suggestedScore = typeof suggestion.suggested_score === "number" && Number.isFinite(suggestion.suggested_score) ? suggestion.suggested_score : undefined;
  const confidence = typeof suggestion.confidence === "number" && Number.isFinite(suggestion.confidence) ? suggestion.confidence : undefined;
  const comments = typeof suggestion.comments === "string" && suggestion.comments.trim() ? suggestion.comments : undefined;
  const restEntries = Object.entries(suggestion).filter(([key]) => !["suggested_score", "confidence", "comments"].includes(key));
  return (
    <div className="arbitration-ai-summary">
      <Descriptions size="small" column={1}>
        <Descriptions.Item label="建议分">{suggestedScore === undefined ? "暂无" : formatScore(suggestedScore)}</Descriptions.Item>
        <Descriptions.Item label="置信度">{confidence === undefined ? "暂无" : `${Math.round(confidence * 100)}%`}</Descriptions.Item>
        <Descriptions.Item label="评语">{comments ?? "暂无"}</Descriptions.Item>
      </Descriptions>
      {restEntries.length > 0 ? (
        <Collapse
          size="small"
          ghost
          items={[{
            key: "raw",
            label: "查看原始数据",
            children: <pre className="arbitration-json-view">{JSON.stringify(Object.fromEntries(restEntries), null, 2)}</pre>
          }]}
        />
      ) : null}
    </div>
  );
}

export function ArbitrationRubricTab({ question, rubricPoints }: { question?: Question; rubricPoints: RubricPoint[] }) {
  if (!question) {
    return <EmptyState title="未获取到评分标准" description="请刷新重试，或联系管理员核对该题设置。" />;
  }
  if (rubricPoints.length === 0) {
    return <EmptyState title="该题未设置评分点" description="可按题目满分直接给出仲裁最终分。" />;
  }
  return (
    <List
      size="small"
      dataSource={rubricPoints}
      locale={{ emptyText: <Empty description="暂无评分点" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
      renderItem={(point) => (
        <List.Item>
          <div className="arbitration-rubric-item">
            <span>{pointLabel(point)}</span>
            <StatusTag tone={point.required ? "processing" : "neutral"}>{point.required ? "必选" : "可选"}</StatusTag>
          </div>
        </List.Item>
      )}
    />
  );
}

export function ArbitrationScoreComparison({ task }: { task: ArbitrationTask }) {
  return (
    <div className="arbitration-score-compare">
      <div>
        <span>阅卷员 A</span>
        <strong>{formatScore(task.first_score)}</strong>
      </div>
      <div>
        <span>阅卷员 B</span>
        <strong>{formatScore(task.second_score)}</strong>
      </div>
      <div>
        <span>分差</span>
        <strong>{formatScore(task.score_difference)}</strong>
        <small>{task.difference_reason || "暂无分差说明"}</small>
        <em>{task.allow_same_arbitrator ? "原阅卷员可参与仲裁" : "须由第三位教师仲裁"}</em>
      </div>
    </div>
  );
}

export function ArbitrationAuditList({ loading, error, logs, onRetry }: { loading: boolean; error: string | null; logs: AuditLog[]; onRetry: () => void }) {
  if (loading) {
    return <LoadingState label="正在读取审计记录" />;
  }
  if (error) {
    return <ErrorState message={error} onRetry={onRetry} />;
  }
  if (logs.length === 0) {
    return <EmptyState title="暂无操作记录" description="该任务暂无操作记录。" />;
  }
  return (
    <List
      size="small"
      dataSource={logs}
      renderItem={(item) => (
        <List.Item>
          <div className="arbitration-audit-item">
            <strong title={item.action}>{auditActionLabels[item.action] ?? "其他操作"}</strong>
            <span>{item.reason || (auditTargetLabels[item.target_type] ?? "操作留痕")}</span>
            <small>{formatTime(item.created_at)}</small>
          </div>
        </List.Item>
      )}
    />
  );
}
