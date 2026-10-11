import { Alert, Descriptions, Tabs } from "antd";
import { Gavel } from "lucide-react";
import { getSafeUserText } from "../api/client";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { ArbitrationAiTab, ArbitrationContextTab, ArbitrationRubricTab, ArbitrationScoreComparison } from "./ArbitrationPanels";
import { formatScore, formatTime, statusLabels } from "./arbitrationPresentation.model";
import type { ArbitrationDetailState } from "./arbitrationQueue.model";

interface DetailProps {
  detail: ArbitrationDetailState;
  examNames: Readonly<Record<string, string>>;
  canReadExams: boolean;
  currentUserId: string;
  maxScore: number;
}

function ArbitrationDetailContent({ detail, examNames, canReadExams, currentUserId, maxScore }: Readonly<DetailProps>) {
  return <main className="arbitration-main">
    {detail.warnings.length > 0 ? <Alert type="warning" showIcon message="部分评卷材料缺失" description={detail.warnings.map((warning) => getSafeUserText(warning, "评卷材料暂时不可用")).join("；")} /> : null}
    <section className="arbitration-context-row">
      <Descriptions bordered size="small" column={4}>
        <Descriptions.Item label="考试"><span title={detail.task.exam_id}>{examNames[detail.task.exam_id] ?? (canReadExams ? "未匹配到考试" : "本场考试")}</span></Descriptions.Item>
        <Descriptions.Item label="题号">{detail.task.question_no}</Descriptions.Item>
        <Descriptions.Item label="匿名码">{detail.task.anonymous_code}</Descriptions.Item>
        <Descriptions.Item label="状态"><span title={detail.task.status}>{statusLabels[detail.task.status] ?? "未知状态"}</span></Descriptions.Item>
        <Descriptions.Item label="分配给">{detail.task.assigned_to ? <span title={detail.task.assigned_to}>{detail.task.assigned_to === currentUserId ? "我" : "已分配"}</span> : "未分配"}</Descriptions.Item>
        <Descriptions.Item label="创建时间">{formatTime(detail.task.created_at)}</Descriptions.Item>
        <Descriptions.Item label="更新时间">{formatTime(detail.task.updated_at)}</Descriptions.Item>
      </Descriptions>
    </section>
    <section className="arbitration-evidence-panel">
      <div className="panel-head"><div><h2>证据与评分标准</h2><p>满分 {maxScore || "未知"}，当前分差 {formatScore(detail.task.score_difference)}</p></div></div>
      <Tabs size="small" items={[
        { key: "context", label: "作答与识别文本", children: <ArbitrationContextTab task={detail.task} /> },
        { key: "ai", label: "AI 建议", children: <ArbitrationAiTab suggestion={detail.task.context?.ai_suggestion} /> },
        { key: "rubric", label: "评分标准", children: <ArbitrationRubricTab question={detail.question} rubricPoints={detail.question?.rubric?.points ?? []} /> }
      ]} />
    </section>
    <section className="arbitration-reviewer-panel">
      <div className="panel-head"><div><h2>双评分差</h2><p>{detail.task.difference_reason || "暂无分差说明"}</p></div><Gavel size={18} /></div>
      <ArbitrationScoreComparison task={detail.task} />
    </section>
  </main>;
}

interface WorkspaceMainProps extends Omit<DetailProps, "detail"> {
  detail: ArbitrationDetailState | null;
  loading: boolean;
  error: string | null;
  onRetry: () => void;
}

export function ArbitrationWorkspaceMain(props: Readonly<WorkspaceMainProps>) {
  if (props.loading) return <main className="arbitration-main-empty"><LoadingState label="正在读取仲裁详情" /></main>;
  if (props.error) return <main className="arbitration-main-empty"><ErrorState message={props.error} onRetry={props.onRetry} /></main>;
  if (!props.detail) return <main className="arbitration-main-empty"><EmptyState title="请选择仲裁任务" description="从左侧任务列表选择后，核对两次评分与答卷证据。" /></main>;
  return <ArbitrationDetailContent {...props} detail={props.detail} />;
}
