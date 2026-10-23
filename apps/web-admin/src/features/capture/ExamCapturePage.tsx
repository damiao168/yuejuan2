import { useEffect, useRef, useState } from "react";
import { App, Button, Space } from "antd";
import { ArrowRight, Play } from "lucide-react";
import type { SessionUser } from "../../auth/session";
import { startExamCollection } from "../../api/configuration";
import { getUserErrorMessage } from "../../api/client";
import { ErrorState, LoadingState } from "../../components/PageState";
import { useExamWorkspace } from "../../query/examWorkspace";
import { CaptureBatchPage } from "../../pages/CaptureBatchPage";

export function ExamCapturePage({ user, examId, canManage, canStart, canReview, onNavigate }: Readonly<{
  user: SessionUser;
  examId: string;
  canManage: boolean;
  canStart: boolean;
  canReview: boolean;
  onNavigate: (path: string) => void;
}>) {
  const { message, modal } = App.useApp();
  const { data, error, refetch } = useExamWorkspace(examId);
  const [starting, setStarting] = useState(false);
  const startPending = useRef(false);
  const startDialog = useRef<{ destroy: () => void } | null>(null);
  const active = useRef(true);
  const startContext = useRef({ canStart, status: data?.exam_status });
  startContext.current = { canStart, status: data?.exam_status };
  const basePath = `/exams/${encodeURIComponent(examId)}`;

  useEffect(() => {
    active.current = true;
    // 切换考试或离开导入页时关闭本场确认框，避免在另一场考试页面提交旧操作。
    return () => { active.current = false; startDialog.current?.destroy(); };
  }, []);

  function confirmStart() {
    if (!canStart || data?.exam_status !== "ready" || startPending.current) return;
    startDialog.current = modal.confirm({
      title: "开始导入答卷",
      content: "开始后，考试将进入答卷导入阶段。请确认试卷、答案和答题卡模板已完成最终核对。",
      okText: "开始导入",
      cancelText: "取消",
      onOk: async () => {
        if (!active.current || !startContext.current.canStart || startContext.current.status !== "ready" || startPending.current) return;
        startPending.current = true;
        setStarting(true);
        try {
          // 准备确认与开始导入是两个状态边界；仍由原接口校验，不能仅凭前端跳转开放批次创建。
          await startExamCollection(examId);
          await refetch();
          if (active.current) message.success("考试已进入答卷导入阶段");
        } catch (startError) {
          // 请求可能已经在服务端生效；先重读当前考试，避免网络失败后继续显示过时的开始入口。
          await refetch().catch(() => undefined);
          if (active.current) message.error(getUserErrorMessage(startError, "暂时无法确认导入状态，请刷新后重试。"));
        } finally {
          startPending.current = false;
          if (active.current) setStarting(false);
        }
      }
    });
  }

  if (error) return <ErrorState message={getUserErrorMessage(error, "答卷导入状态加载失败")} onRetry={() => void refetch()} />;
  if (!data) return <LoadingState label="正在加载答卷导入" />;

  const needsPreparation = ["draft", "configured"].includes(data.exam_status);
  const importing = data.exam_status === "collecting";
  const reviewing = ["grading", "reviewing"].includes(data.exam_status);
  const canOpenBatches = importing || reviewing || ["finalized", "published", "archived"].includes(data.exam_status);
  // 阅卷中的新增材料通过补传批次处理；不能为了补传把考试回退到导入阶段。
  const canEditBatches = canManage && (importing || reviewing);

  if (!needsPreparation && data.exam_status !== "ready" && !canOpenBatches) return <ErrorState message="当前考试状态不支持导入答卷，请返回阅卷管理刷新。" onRetry={() => void refetch()} />;

  return <div className="page-stack">
    <Space wrap>
      <Button onClick={() => onNavigate("/grading")}>返回阅卷管理</Button>
      {(importing || reviewing) && canReview ? <Button onClick={() => onNavigate(`${basePath}/grading`)}>前往阅卷 <ArrowRight size={16} /></Button> : null}
    </Space>
    {canOpenBatches ? <CaptureBatchPage user={user} examId={examId} canManage={canEditBatches} examStatus={data.exam_status} onCollectionChanged={refetch} /> : (
      <section className="page-heading">
        <div><h2>答卷导入</h2><p>{needsPreparation ? "请先完成考试准备并确认，再开始导入学生答卷。" : "考试准备已确认，开始导入后可创建批次并上传学生答卷。"}</p></div>
        {needsPreparation ? <Button onClick={() => onNavigate(`${basePath}/settings`)}>检查考试准备</Button> : <Button type="primary" icon={<Play size={16} />} disabled={!canStart} loading={starting} onClick={confirmStart}>开始导入答卷</Button>}
      </section>
    )}
  </div>;
}
