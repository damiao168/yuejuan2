import { useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { GradingToolbarContext } from "../../../../components/GradingToolbarContext";
import { Alert, Button, Progress, Space } from "antd";
import { ArrowRight, BadgeCheck, CheckCircle2, RefreshCw } from "lucide-react";
import type { DraftSaveStatus, ReviewerProgress } from "../gradingWorkbench.types";

const draftStatusLabel: Record<DraftSaveStatus, string> = {
  idle: "尚未修改",
  pending: "有未保存修改",
  saving: "正在保存到服务端",
  saved: "已保存到服务端",
  offline: "仅本页暂存，待同步",
  conflict: "草稿冲突",
  error: "保存失败，仅本页暂存",
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
  onClaim: () => Promise<void>;
  onRelease: () => Promise<void>;
  onReloadConflict: () => Promise<void>;
  onResolveConflict: () => Promise<void>;
  personalActions?: ReactNode;
  readonlyLabel?: string;
  canSubmit?: boolean;
  onSubmit?: () => void;
  taskLabel?: string;
  scoreLabel?: string;
}

// 告警只呈现当前草稿状态；冲突时仍保留合并和放弃本页修改的恢复入口。
function DraftStatusNotice({ draftSaveStatus, onResolveConflict, onReloadConflict, readonlyLabel }: Readonly<Pick<WorkbenchHeaderProps, "draftSaveStatus" | "onResolveConflict" | "onReloadConflict" | "readonlyLabel">>) {
  return (
    <>
      {draftSaveStatus === "conflict" && <Alert type="error" showIcon message="草稿已被其他会话更新" description="为防止覆盖其他会话的修改，自动保存已暂停。可逐项选择本页或服务端内容，再按服务端新版本重新保存。" action={<Space wrap><Button type="primary" onClick={() => void onResolveConflict()}>逐项合并</Button><Button onClick={() => void onReloadConflict()}>放弃本页修改</Button></Space>} />}
      {draftSaveStatus === "offline" && <Alert type="warning" showIcon message="当前离线，未同步内容仅在本页暂存" description="关闭或刷新页面可能丢失未同步内容；恢复网络后会尝试保存到服务端。" />}
      {draftSaveStatus === "error" && <Alert type="warning" showIcon message="草稿尚未保存到服务端" description="未同步内容仅在本页短暂保留，关闭或刷新可能丢失；检查网络后再确认保存状态。" />}
      {draftSaveStatus === "readonly" && <Alert type="info" showIcon message={readonlyLabel || "管理员只读检查"} description="当前材料只读；评分草稿与最终提交只能在本人可编辑的任务中完成。" />}
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
  onClaim,
  onReloadConflict,
  onResolveConflict,
  personalActions,
  readonlyLabel,
  canSubmit,
  onSubmit,
  taskLabel,
  scoreLabel
}: Readonly<WorkbenchHeaderProps>) {
  const showReviewerActions = !canManageTasks || queueScope === "mine";
  const toolbarTarget = useContext(GradingToolbarContext);
  const { incompleteProgress, totalLabel, progressSummary } = progressLabels({ canManageTasks, hasContext, myProgress, remainingCount, progressComplete });
  const toolbar = <section className="grading-topbar">
        <div className="grading-work-title">
          {canManageTasks ? <h1>阅卷管理</h1> : null}
          {taskLabel ? <strong className="grading-current-task">{/^(?:Q)?\d+(?:[.．-]\d+)*$/i.test(taskLabel) ? `第 ${taskLabel.replace(/^Q/i, "")} 题` : taskLabel}</strong> : null}
          {scoreLabel ? <span className="grading-current-score">· {scoreLabel}</span> : null}
          <span>{progressSummary}</span>
        </div>
        <Space wrap={!toolbarTarget}>
          {hasContext ? <span className={`draft-save-status ${draftSaveStatus}`}>{draftStatusText ?? (draftSaveStatus === "readonly" ? readonlyLabel || draftStatusLabel[draftSaveStatus] : draftStatusLabel[draftSaveStatus])}</span> : null}
          {personalActions ? <div className="grading-personal-actions">{personalActions}</div> : null}
          {onSubmit ? <Button type="primary" icon={<CheckCircle2 size={16} />} disabled={!canSubmit} loading={actioning === "submit"} onClick={onSubmit}>提交并下一份</Button> : null}
          <Button icon={<RefreshCw size={16} />} onClick={() => void onRefresh()} loading={loading}>刷新</Button>
          {showReviewerActions ? <Button icon={<ArrowRight size={16} />} onClick={() => void onNext()} disabled={!canWork}>下一份</Button> : null}
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
