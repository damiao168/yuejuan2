import { Alert, Button, Progress, Space, Tooltip } from "antd";
import { ArrowRight, BadgeCheck, LogOut, RefreshCw } from "lucide-react";
import { BackmarkQueue } from "../../../../components/BackmarkQueue";
import { RegradeQueue } from "../../../../components/RegradeQueue";
import type { DraftSaveStatus, ReviewerProgress } from "../gradingWorkbench.types";

const draftStatusLabel: Record<DraftSaveStatus, string> = {
  idle: "尚未修改",
  saving: "正在保存到服务端",
  saved: "已保存到服务端",
  offline: "仅本页暂存，待同步",
  conflict: "草稿冲突",
  error: "保存失败，仅本页暂存",
  readonly: "管理员只读"
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
  loading: boolean;
  actioning: string | null;
  onRefresh: () => Promise<void>;
  onNext: () => Promise<void>;
  onClaim: () => Promise<void>;
  onRelease: () => Promise<void>;
  onReloadConflict: () => Promise<void>;
  onResolveConflict: () => Promise<void>;
}

// 告警只呈现当前草稿状态；是否显示操作以及能否暂放，仍由工具栏的权限和保存状态判断决定。
function DraftStatusNotice({ draftSaveStatus, onResolveConflict, onReloadConflict }: Readonly<Pick<WorkbenchHeaderProps, "draftSaveStatus" | "onResolveConflict" | "onReloadConflict">>) {
  return (
    <>
      {draftSaveStatus === "conflict" && <Alert type="error" showIcon message="草稿已被其他会话更新" description="为防止覆盖其他会话的修改，自动保存已暂停。可逐项选择本页或服务端内容，再按服务端新版本重新保存。" action={<Space wrap><Button type="primary" onClick={() => void onResolveConflict()}>逐项合并</Button><Button onClick={() => void onReloadConflict()}>放弃本页修改</Button></Space>} />}
      {draftSaveStatus === "offline" && <Alert type="warning" showIcon message="当前离线，未同步内容仅在本页暂存" description="关闭或刷新页面可能丢失未同步内容；恢复网络后会尝试保存到服务端。" />}
      {draftSaveStatus === "error" && <Alert type="warning" showIcon message="草稿尚未保存到服务端" description="未同步内容仅在本页短暂保留，关闭或刷新可能丢失；检查网络后再确认保存状态。" />}
      {draftSaveStatus === "readonly" && <Alert type="info" showIcon message="管理员只读检查" description="管理员可查看材料、分配和管理任务；评分草稿与最终提交只能由被分配的阅卷员完成。" />}
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
    progressSummary = `已完成 ${myProgress?.completed ?? 0} / ${totalLabel} ${myProgress?.total ?? 0}${incompleteProgress}`;
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
  ownsSelectedTask,
  myProgress,
  reviewerProgress,
  remainingCount,
  progressComplete,
  draftSaveStatus,
  loading,
  actioning,
  onRefresh,
  onNext,
  onClaim,
  onRelease,
  onReloadConflict,
  onResolveConflict
}: Readonly<WorkbenchHeaderProps>) {
  const showReviewerActions = !canManageTasks || queueScope === "mine";
  const { incompleteProgress, totalLabel, progressSummary } = progressLabels({ canManageTasks, hasContext, myProgress, remainingCount, progressComplete });
  return (
    <>
      <section className="grading-topbar">
        <div className="grading-work-title">
          <h1>{canManageTasks ? "阅卷管理" : "阅卷"}</h1>
          <span>{progressSummary}</span>
        </div>
        <Space wrap>
          {hasContext ? <span className={`draft-save-status ${draftSaveStatus}`}>{draftStatusLabel[draftSaveStatus]}</span> : null}
          <Button icon={<RefreshCw size={16} />} onClick={() => void onRefresh()} loading={loading}>刷新</Button>
          {showReviewerActions ? <Button icon={<ArrowRight size={16} />} onClick={() => void onNext()} disabled={!canWork}>下一份</Button> : null}
          {!canManageTasks ? <BackmarkQueue canWork={canWork} /> : null}
          {!canManageTasks ? <RegradeQueue canWork={canWork} /> : null}
          {canWork && showReviewerActions ? <Button type="primary" icon={<BadgeCheck size={16} />} loading={actioning === "claim-task"} onClick={() => void onClaim()}>开始处理</Button> : null}
          {showReviewerActions ? (
            <Tooltip title="放回队列前请确认草稿已保存到服务端；未同步的修改无法保证恢复">
              <Button icon={<LogOut size={16} />} disabled={!ownsSelectedTask || !["saved", "idle"].includes(draftSaveStatus)} loading={actioning === "release"} onClick={() => void onRelease()}>暂放</Button>
            </Tooltip>
          ) : null}
        </Space>
      </section>

      <DraftStatusNotice draftSaveStatus={draftSaveStatus} onResolveConflict={onResolveConflict} onReloadConflict={onReloadConflict} />

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
