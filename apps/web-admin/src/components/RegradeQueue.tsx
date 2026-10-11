import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Drawer, Empty, Input, List, Space, Spin, Tag } from "antd";
import { ClipboardPenLine, RefreshCw } from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import {
  claimRegradeItem,
  downloadRegradeSegmentImage,
  getRegradeItemContext,
  listMyRegradeItems,
  recordRegradeCandidate,
  type RegradeGraderContext,
  type RegradeWorkItem
} from "../api/regrade";
import { SharedScoreControl, type SharedScoreValue } from "../features/grading/SharedScoreControl";
import { useIndependentReviewGuard } from "../features/grading/workbench/hooks/useIndependentReviewGuard";
import { TaskScoringReference } from "./TaskScoringReference";
import { MathMarkdown } from "./MathMarkdown";

function errorMessage(error: unknown) {
  return getUserErrorMessage(error, "重评任务暂时无法处理，请稍后重试");
}

function rubricPoints(context?: RegradeGraderContext) {
  const values = context?.frozen_rubric.points ?? [];
  return values.flatMap((value) => {
    if (!value || typeof value !== "object") return [];
    const point = value as Record<string, unknown>;
    const id = typeof point.id === "string" ? point.id : "";
    const score = typeof point.score === "number" ? point.score : Number(point.score);
    if (!id || !Number.isFinite(score)) return [];
    return [{ id, description: typeof point.description === "string" ? point.description : "未命名采分点", score, required: Boolean(point.required) }];
  });
}

function RegradeAnswerEvidence({ context, imageUrl }: Readonly<{ context: RegradeGraderContext; imageUrl: string }>) {
  return (
    <section>
      <h3>{context.question.question_no} · {context.question.question_type}</h3>
      <TaskScoringReference question={context.question} rubric={context.frozen_rubric} />
      {imageUrl ? <img className="backmark-answer-image" src={imageUrl} alt="当前答题区域" /> : <Alert type="warning" showIcon message="答题图片暂不可预览" description={context.answer.raw_answer || context.answer.ocr_text ? "可依据下方文字内容独立评分。" : "请联系管理员检查答题图处理状态。"} />}
      {(context.answer.raw_answer || context.answer.ocr_text) ? <div className="backmark-answer-text"><strong>答题内容</strong><MathMarkdown>{context.answer.raw_answer || context.answer.ocr_text || ""}</MathMarkdown></div> : null}
    </section>
  );
}

// RegradeQueue 与普通阅卷分开：它接收更正任务冻结的
// 评分细则，而不是先前评分、AI 建议或发布结果。
// 提交只保存候选结果，供管理人员复核并在以后发布成绩；
// 不会直接更新当前可见成绩。
export function RegradeQueue({ canWork }: Readonly<{ canWork: boolean }>) {
  const { message } = App.useApp();
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<RegradeWorkItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [activeItem, setActiveItem] = useState<RegradeWorkItem>();
  const [context, setContext] = useState<RegradeGraderContext>();
  const [imageUrl, setImageUrl] = useState("");
  const [score, setScore] = useState<SharedScoreValue>({ score: null, rubricSelections: {} });
  const [comment, setComment] = useState("");
  const [actioning, setActioning] = useState(false);
  const { busyRef, isCurrent, leave } = useIndependentReviewGuard(Boolean(activeItem && (score.score !== null || comment || Object.keys(score.rubricSelections).length)), actioning, "重评", () => {
    if (imageUrl) URL.revokeObjectURL(imageUrl);
    setOpen(false); setItems([]); setActiveItem(undefined); setContext(undefined); setImageUrl(""); setComment(""); setScore({ score: null, rubricSelections: {} }); setActioning(false); setLoading(false);
  });

  const load = useCallback(async () => {
    if (!canWork || !isCurrent()) return;
    setLoading(true);
    try {
      const response = await listMyRegradeItems();
      if (isCurrent()) setItems(response.regrade_items);
    } catch (error) {
      if (isCurrent()) message.error(errorMessage(error));
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [canWork, message, isCurrent]);

  useEffect(() => {
    if (open) void load();
  }, [load, open]);

  useEffect(() => () => {
    if (imageUrl) URL.revokeObjectURL(imageUrl);
  }, [imageUrl]);

  const points = useMemo(() => rubricPoints(context), [context]);

  const closeActive = () => {
    if (imageUrl) URL.revokeObjectURL(imageUrl);
    setImageUrl("");
    setActiveItem(undefined);
    setContext(undefined);
    setScore({ score: null, rubricSelections: {} });
    setComment("");
  };

  const openItem = async (item: RegradeWorkItem) => {
    if (!isCurrent() || busyRef.current) return;
    busyRef.current = true;
    setActioning(true);
    try {
      const claimed = await claimRegradeItem(item.id);
      if (!isCurrent()) return;
      const response = await getRegradeItemContext(item.id);
      if (!isCurrent()) return;
      let nextImageUrl = "";
      try {
        const image = await downloadRegradeSegmentImage(item.id);
        nextImageUrl = URL.createObjectURL(image.blob);
      } catch {
        // 裁切图不可用时，仍可根据文本或 OCR 答案评分。
      }
      // 下载结束时会话可能已失效或组件已卸载；释放临时图片，不能继续更新旧任务状态。
      if (!isCurrent()) {
        if (nextImageUrl) {
          URL.revokeObjectURL(nextImageUrl);
        }
        return;
      }
      if (imageUrl) URL.revokeObjectURL(imageUrl);
      setActiveItem(claimed.regrade_item);
      setContext(response.regrade_context);
      setImageUrl(nextImageUrl);
      setScore({ score: null, rubricSelections: {} });
      setComment("");
    } catch (error) {
      if (isCurrent()) message.error(errorMessage(error));
    } finally {
      if (isCurrent()) { busyRef.current = false; setActioning(false); }
    }
  };

  const submit = async () => {
    if (!isCurrent() || !activeItem || !context || score.score === null || busyRef.current) return;
    busyRef.current = true;
    setActioning(true);
    try {
      await recordRegradeCandidate(activeItem.id, {
        score: score.score,
        rubric_selections: Object.entries(score.rubricSelections).map(([point_id, value]) => ({ point_id, score: value })),
        comment,
        expected_revision: context.expected_revision,
        require_manual_review: true
      });
      if (!isCurrent()) return;
      message.success("新版细则下的重评意见已提交，原成绩不会在此处直接修改");
      closeActive();
      await load();
    } catch (error) {
      if (isCurrent()) message.error(errorMessage(error));
    } finally {
      if (isCurrent()) { busyRef.current = false; setActioning(false); }
    }
  };

  if (!canWork) return null;

  return (
    <>
      <Button icon={<ClipboardPenLine size={16} />} onClick={() => setOpen(true)}>重评任务</Button>
      <Drawer
        title={activeItem ? "按新版细则重评" : "我的重评任务"}
        width={activeItem ? "min(1180px, 96vw)" : 520}
        open={open}
        onClose={() => leave(() => { closeActive(); setOpen(false); })}
        closable={!actioning}
        maskClosable={!actioning}
        keyboard={!actioning}
        extra={<Button icon={<RefreshCw size={15} />} loading={loading} onClick={() => void load()}>刷新</Button>}
      >
        {activeItem && context ? (
          <div className="backmark-workspace">
            <Alert showIcon type="info" message="独立重评" description="本页不显示旧分、旧评语、原阅卷人或历史成绩。请仅依据当前答题内容和新版冻结评分细则评分。" />
            <div className="backmark-workspace-grid">
              <RegradeAnswerEvidence context={context} imageUrl={imageUrl} />
              <section>
                <h3>新版细则评分</h3>
                <SharedScoreControl value={score} maxScore={context.question.score} rubricPoints={points} onChange={setScore} disabled={actioning} />
                <Input.TextArea disabled={actioning} value={comment} onChange={(event) => setComment(event.target.value)} rows={4} maxLength={2000} showCount placeholder="可选：记录本次重评依据，供后续复核查看" />
                <Space className="backmark-submit-actions">
                  <Button disabled={actioning} onClick={() => leave(closeActive)}>返回队列</Button>
                  <Button type="primary" disabled={score.score === null} loading={actioning} onClick={() => void submit()}>提交重评意见</Button>
                </Space>
              </section>
            </div>
          </div>
        ) : (
          <Spin spinning={loading}>
            {items.length ? <List dataSource={items} renderItem={(item) => <List.Item actions={[<Button key="open" type="primary" loading={actioning} onClick={() => void openItem(item)}>开始重评</Button>]}><List.Item.Meta title={`重评任务 · 满分 ${item.max_score}`} description={`任务创建于 ${new Date(item.created_at).toLocaleString("zh-CN", { hour12: false })}`} /><Tag>{item.status === "claimed" ? "处理中" : "待处理"}</Tag></List.Item>} /> : <Empty description="暂无分配给你的重评任务" />}
          </Spin>
        )}
      </Drawer>
    </>
  );
}
