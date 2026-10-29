import { useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { GradingToolbarContext } from "../../../../components/GradingToolbarContext";
import { Alert, Button, Progress, Space } from "antd";
import { ArrowLeft, ArrowRight, BadgeCheck, RefreshCw } from "lucide-react";
import type { DraftSaveStatus, ReviewerProgress } from "../gradingWorkbench.types";
import type { FrozenReferenceTab } from "./FrozenQuestionReference";

const draftStatusLabel: Record<DraftSaveStatus, string> = {
  idle: "",
  pending: "",
  saving: "",
  saved: "",
  offline: "",
  conflict: "草稿冲突",
  error: "",
  readonly: "只读"
};

export interface WorkbenchHeaderProps {
  canWork: boolean;
  canManageTasks: boolean;
  queueScope: "mine" | "all";
  hasContext: boolean;
  ownsSelectedTask: boolean;
  myProgress?: ReviewerProgress;
  reviewerProgress: ReviewerProgress[];
  remainingCount: number;
  progressComplete: boolean;
  draftSaveStatus: DraftSaveStatus;
  draftStatusText?: string;
  loading: boolean;
  actioning: string | null;
  onRefresh: () => Promise<void>;
  onNext: () => Promise<void>;
  nextLabel?: string;
  onPrevious?: () => void | Promise<void>;
  canPrevious?: boolean;
  onOpenReference?: (tab: FrozenReferenceTab) => void;
  onClaim: () => Promise<void>;
  onRelease: () => Promise<void>;
  onReloadConflict: () => Promise<void>;
  onResolveConflict: () => Promise<void>;
  personalActions?: ReactNode;
  readonlyLabel?: string;
  taskLabel?: string;
  scoreLabel?: string;
}

// 草稿同步在后台进行；只读边界和冲突恢复入口仍须直接可见，正式交分错误由提交动作呈现。
function DraftStatusNotice({ draftSaveStatus, onResolveConflict, onReloadConflict, readonlyLabel }: Readonly<Pick<WorkbenchHeaderProps, "draftSaveStatus" | "onResolveConflict" | "onReloadConflict" | "readonlyLabel">>) {
  return (
    <>
      {draftSaveStatus === "conflict" && <Alert type="error" showIcon message="草稿已被其他会话更新" description="自动保存已暂停。请逐项选择要保留的内容，或放弃本页修改。" action={<Space wrap><Button type="primary" onClick={() => void onResolveConflict()}>逐项合并</Button><Button onClick={() => void onReloadConflict()}>放弃本页修改</Button></Space>} />}
      {draftSaveStatus === "readonly" && readonlyLabel !== "已提交·只读" && <Alert type="info" showIcon message={readonlyLabel || "管理员只读检查"} description="当前材料只读；评分草稿与最终提交只能在本人可编辑的任务中完成。" />}
    </>
  );
}

// 未加载完整进度时只显示已加载数量，不能把它写成全量待处理任务。
function progressLabels({ canManageTasks, hasContext, myProgress, remainingCount, progressComplete }: Readonly<Pick<WorkbenchHeaderProps, "canManageTasks" | "hasContext" | "myProgress" | "remainingCount" | "progressComplete">>) {
  const incompleteProgress = progressComplete ? "" : " · 数据未完整";
  const totalLabel = progressComplete ? "共" : "已加载";
  const remainingLabel = progressComplete ? "剩余" : "已加载待处理";
  let progressSummary = "分配任务、查看进度和处理异常";
  if (!canManageTasks) {
    progressSummary = progressComplete ? `已完成 ${myProgress?.completed ?? 0}/${myProgress?.total ?? 0}` : `已完成 ${myProgress?.completed ?? 0} / 已加载 ${myProgress?.total ?? 0}${incompleteProgress}`;
  } else if (hasContext) {
    progressSummary = `${remainingLabel} ${remainingCount} 份${incompleteProgress}`;
  }
  return { incompleteProgress, totalLabel, progressSummary };
}

export function WorkbenchHeader({
  canWork,
  canManageTasks,
  queueScope,
  hasContext,
  myProgress,
  reviewerProgress,
  remainingCount,
  progressComplete,
  draftSaveStatus,
  draftStatusText,
  loading,
  actioning,
  onRefresh,
  onNext,
  nextLabel = "下一份",
  onPrevious,
  canPrevious = false,
  onOpenReference,
  onClaim,
  onReloadConflict,
  onResolveConflict,
  personalActions,
  readonlyLabel,
  taskLabel,
  scoreLabel
}: Readonly<WorkbenchHeaderProps>) {
  const showReviewerActions = !canManageTasks || queueScope === "mine";
  const toolbarTarget = useContext(GradingToolbarContext);
  const { incompleteProgress, totalLabel, progressSummary } = progressLabels({ canManageTasks, hasContext, myProgress, remainingCount, progressComplete });
  // 日常录分不展示后台草稿同步状态；只读和冲突继续解释当前可用操作。
  const draftLabel = draftStatusText ?? (draftSaveStatus === "readonly" ? readonlyLabel || draftStatusLabel[draftSaveStatus] : draftStatusLabel[draftSaveStatus]);
  const toolbar = <section className="grading-topbar">
        <div className="grading-work-title">
          {canManageTasks ? <h1>阅卷管理</h1> : null}
          {taskLabel ? <strong className="grading-current-task">{/^(?:Q)?\d+(?:[.．-]\d+)*$/i.test(taskLabel) ? `第 ${taskLabel.replace(/^Q/i, "")} 题` : taskLabel}</strong> : null}
          {scoreLabel ? <span className="grading-current-score">· {scoreLabel}</span> : null}
          <span>{progressSummary}</span>
        </div>
        <Space wrap={!toolbarTarget}>
          {hasContext && draftLabel ? <span className={`draft-save-status ${draftSaveStatus}`}>{draftLabel}</span> : null}
          {personalActions ? <div className="grading-personal-actions">{personalActions}</div> : null}
          {onOpenReference ? <><Button disabled={!hasContext} onClick={() => onOpenReference("rules")}>评分细则</Button><Button disabled={!hasContext} onClick={() => onOpenReference("question")}>题目与答案</Button></> : null}
          <Button icon={<RefreshCw size={16} />} onClick={() => void onRefresh()} loading={loading}>刷新</Button>
          {showReviewerActions && onPrevious ? <Button icon={<ArrowLeft size={16} />} onClick={() => void onPrevious()} disabled={!canWork || !canPrevious}>上一份</Button> : null}
          {showReviewerActions ? <Button type={nextLabel === "下一份" ? "default" : "primary"} icon={<ArrowRight size={16} />} onClick={() => void onNext()} disabled={!canWork || loading || Boolean(actioning)} loading={actioning === "submit"}>{nextLabel}</Button> : null}
          {/* 当前答卷打开后已自动续租；只有空选择且仍有任务时才需要显式领取，避免重复领取看起来毫无变化。 */}
          {canWork && showReviewerActions && !hasContext && !loading && remainingCount > 0 ? <Button type="primary" icon={<BadgeCheck size={16} />} disabled={Boolean(actioning)} loading={actioning === "claim-task"} onClick={() => void onClaim()}>领取任务</Button> : null}
        </Space>
      </section>;
  return (
    <>
      {toolbarTarget && !canManageTasks ? createPortal(toolbar, toolbarTarget) : toolbar}
      <DraftStatusNotice draftSaveStatus={draftSaveStatus} onResolveConflict={onResolveConflict} onReloadConflict={onReloadConflict} readonlyLabel={readonlyLabel} />

      {canManageTasks && queueScope === "all" ? (
        <section className="reviewer-progress-panel" aria-label="阅卷员进度">
          <div className="reviewer-progress-head"><div><h2>阅卷员进度</h2></div><span className="muted">{`${totalLabel} ${reviewerProgress.length} 名阅卷员${incompleteProgress}`}</span></div>
          <div className="reviewer-progress-list">
            {reviewerProgress.length ? reviewerProgress.map((reviewer) => (
              <div className="reviewer-progress-row" key={reviewer.id}>
                <div className="reviewer-progress-label"><strong>{reviewer.name}</strong><span>{reviewer.completed} / {reviewer.total} 题</span></div>
                <Progress percent={reviewer.percent} showInfo={false} status={reviewer.percent === 100 ? "success" : "active"} />
                <div className="reviewer-progress-meta"><span>进行中 {reviewer.active}</span><span>待完成 {reviewer.total - reviewer.completed}</span><strong>{reviewer.percent}%</strong></div>
              </div>
            )) : <span className="muted">暂无已分配的阅卷任务</span>}
          </div>
        </section>
      ) : null}
    </>
  );
}
