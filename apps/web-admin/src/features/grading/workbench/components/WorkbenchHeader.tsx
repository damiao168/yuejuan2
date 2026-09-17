import { Alert, Button, Progress, Space, Tooltip } from "antd";
import { ArrowRight, BadgeCheck, LogOut, RefreshCw } from "lucide-react";
import { BackmarkQueue } from "../../../../components/BackmarkQueue";
import { RegradeQueue } from "../../../../components/RegradeQueue";
import type { DraftSaveStatus, ReviewerProgress } from "../gradingWorkbench.types";

const draftStatusLabel: Record<DraftSaveStatus, string> = {
  idle: "尚未修改",
  saving: "正在保存",
  saved: "草稿已保存",
  offline: "离线草稿待同步",
  conflict: "草稿冲突",
  error: "草稿保存失败",
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
  draftSaveStatus: DraftSaveStatus;
  loading: boolean;
  actioning: string | null;
  onRefresh: () => Promise<void>;
  onNext: () => Promise<void>;
  onClaim: () => Promise<void>;
  onRelease: () => Promise<void>;
  onReloadConflict: () => Promise<void>;
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
  draftSaveStatus,
  loading,
  actioning,
  onRefresh,
  onNext,
  onClaim,
  onRelease,
  onReloadConflict
}: WorkbenchHeaderProps) {
  const showReviewerActions = !canManageTasks || queueScope === "mine";
  return (
    <>
      <section className="grading-topbar">
        <div className="grading-work-title">
          <h1>{canManageTasks ? "阅卷管理" : "阅卷"}</h1>
          <span>
            {!canManageTasks
              ? `已完成 ${myProgress?.completed ?? 0} / 共 ${myProgress?.total ?? 0}`
              : hasContext ? `剩余 ${remainingCount} 份` : "分配任务、查看进度和处理异常"}
          </span>
        </div>
        <Space wrap>
          {hasContext ? <span className={`draft-save-status ${draftSaveStatus}`}>{draftStatusLabel[draftSaveStatus]}</span> : null}
          <Button icon={<RefreshCw size={16} />} onClick={() => void onRefresh()} loading={loading}>刷新</Button>
          {showReviewerActions ? <Button icon={<ArrowRight size={16} />} onClick={() => void onNext()} disabled={!canWork}>下一份</Button> : null}
          {!canManageTasks ? <BackmarkQueue canWork={canWork} /> : null}
          {!canManageTasks ? <RegradeQueue canWork={canWork} /> : null}
          {canWork && showReviewerActions ? <Button type="primary" icon={<BadgeCheck size={16} />} loading={actioning === "claim-task"} onClick={() => void onClaim()}>开始处理</Button> : null}
          {showReviewerActions ? (
            <Tooltip title="把这份答卷放回队列，稍后可继续，草稿会保留">
              <Button icon={<LogOut size={16} />} disabled={!ownsSelectedTask} loading={actioning === "release"} onClick={() => void onRelease()}>暂放</Button>
            </Tooltip>
          ) : null}
        </Space>
      </section>

      {draftSaveStatus === "conflict" ? <Alert type="error" showIcon message="草稿已被其他会话更新" description="为防止覆盖他人修改，自动保存已暂停。重新载入任务后再应用本地修改。" action={<Button onClick={() => void onReloadConflict()}>重新载入</Button>} /> : draftSaveStatus === "offline" ? <Alert type="warning" showIcon message="当前离线，草稿已保存在本机" description="恢复网络后会按版本号同步；提交或退出后会清理本机草稿。" /> : draftSaveStatus === "error" ? <Alert type="warning" showIcon message="草稿暂未保存到服务端" description="本机保留了短期草稿；检查网络后系统会再次尝试保存。" /> : draftSaveStatus === "readonly" ? <Alert type="info" showIcon message="管理员只读检查" description="管理员可查看材料、分配和管理任务；评分草稿与最终提交只能由被分配的阅卷员完成。" /> : null}

      {canManageTasks && queueScope === "all" ? (
        <section className="reviewer-progress-panel" aria-label="阅卷员进度">
          <div className="reviewer-progress-head"><div><h2>阅卷员进度</h2></div><span className="muted">{`${reviewerProgress.length} 名阅卷员`}</span></div>
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
