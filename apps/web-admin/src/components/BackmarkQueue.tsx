import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Drawer, Empty, Input, List, Space, Spin, Tag } from "antd";
import { ClipboardCheck, RefreshCw } from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import {
  claimBackmarkItem,
  downloadBackmarkSegmentImage,
  getBackmarkItemContext,
  listMyBackmarkItems,
  submitBackmarkItem,
  type BackmarkGraderContext,
  type BackmarkGraderItem
} from "../api/backmark";
import { SharedScoreControl, type SharedScoreValue } from "../features/grading/SharedScoreControl";
import { useIndependentReviewGuard } from "../features/grading/workbench/hooks/useIndependentReviewGuard";
import { TaskScoringReference } from "./TaskScoringReference";
import { MathMarkdown } from "./MathMarkdown";

function errorMessage(error: unknown, label: string) {
  return getUserErrorMessage(error, `${label}任务暂时无法处理，请稍后重试`);
}

function rubricPoints(context?: BackmarkGraderContext) {
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

function BackmarkAnswerEvidence({ context, imageUrl }: Readonly<{ context: BackmarkGraderContext; imageUrl: string }>) {
  return (
    <section>
      <h3>{context.question.question_no} · {context.question.question_type}</h3>
      <TaskScoringReference question={context.question} rubric={context.frozen_rubric} />
      {imageUrl ? <img className="backmark-answer-image" src={imageUrl} alt="当前答题区域" /> : <Alert type="warning" showIcon message="答题图片暂不可预览" description={context.answer.raw_answer || context.answer.ocr_text ? "可依据下方文字内容独立评分。" : "请联系管理员检查答题图处理状态。"} />}
      {(context.answer.raw_answer || context.answer.ocr_text) ? <div className="backmark-answer-text"><strong>答题内容</strong><MathMarkdown>{context.answer.raw_answer || context.answer.ocr_text || ""}</MathMarkdown></div> : null}
    </section>
  );
}

export function BackmarkQueue({ canWork, label = "回标" }: Readonly<{ canWork: boolean; label?: string }>) {
  const { message } = App.useApp();
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<BackmarkGraderItem[]>([]);
  const [nextCursor, setNextCursor] = useState("");
  const [loading, setLoading] = useState(false);
  const [activeItem, setActiveItem] = useState<BackmarkGraderItem>();
  const [context, setContext] = useState<BackmarkGraderContext>();
  const [imageUrl, setImageUrl] = useState("");
  const [score, setScore] = useState<SharedScoreValue>({ score: null, rubricSelections: {} });
  const [comments, setComments] = useState("");
  const [actioning, setActioning] = useState(false);
  const { busyRef, isCurrent, leave } = useIndependentReviewGuard(Boolean(activeItem && (score.score !== null || comments || Object.keys(score.rubricSelections).length)), actioning, label, () => {
    if (imageUrl) URL.revokeObjectURL(imageUrl);
    setOpen(false); setItems([]); setActiveItem(undefined); setContext(undefined); setImageUrl(""); setComments(""); setScore({ score: null, rubricSelections: {} }); setActioning(false); setNextCursor(""); setLoading(false);
  });
  const closeActive = () => {
    if (imageUrl) URL.revokeObjectURL(imageUrl);
    setImageUrl(""); setActiveItem(undefined); setContext(undefined);
    setScore({ score: null, rubricSelections: {} }); setComments("");
  };

  const load = useCallback(async (cursor?: string) => {
    if (!canWork || !isCurrent()) return;
    setLoading(true);
    try {
      const response = await listMyBackmarkItems({ limit: 50, cursor });
      if (isCurrent()) { setItems((current) => cursor ? [...current, ...response.backmark_items] : response.backmark_items); setNextCursor(response.has_more ? response.next_cursor : ""); }
    } catch (error) {
      if (isCurrent()) message.error(errorMessage(error, label));
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [canWork, message, isCurrent, label]);

  useEffect(() => {
    if (open) void load();
  }, [load, open]);

  useEffect(() => () => {
    if (imageUrl) URL.revokeObjectURL(imageUrl);
  }, [imageUrl]);

  const points = useMemo(() => rubricPoints(context), [context]);

  const openItem = async (item: BackmarkGraderItem) => {
    if (!isCurrent() || busyRef.current) return;
    busyRef.current = true;
    setActioning(true);
    try {
      const claimed = await claimBackmarkItem(item.id);
      if (!isCurrent()) return;
      const response = await getBackmarkItemContext(item.id);
      if (!isCurrent()) return;
      let nextImageUrl = "";
      try {
        const image = await downloadBackmarkSegmentImage(item.id);
        nextImageUrl = URL.createObjectURL(image.blob);
      } catch {
        // 即使裁切图无法在浏览器预览，文本和 OCR 证据仍然可用。
      }
      // 下载结束时会话可能已失效或组件已卸载；释放临时图片，不能继续更新旧任务状态。
      if (!isCurrent()) {
        if (nextImageUrl) {
          URL.revokeObjectURL(nextImageUrl);
        }
        return;
      }
      if (imageUrl) URL.revokeObjectURL(imageUrl);
      setActiveItem(claimed.backmark_item);
      setContext(response.backmark_context);
      setImageUrl(nextImageUrl);
      setScore({ score: null, rubricSelections: {} });
      setComments("");
    } catch (error) {
      if (isCurrent()) message.error(errorMessage(error, label));
    } finally {
      if (isCurrent()) { busyRef.current = false; setActioning(false); }
    }
  };

  const submit = async () => {
    if (!isCurrent() || !activeItem || !context || score.score === null || busyRef.current) return;
    busyRef.current = true;
    setActioning(true);
    try {
      // 提交的是带上下文修订号的回标意见；此入口不直接改写原成绩。
      await submitBackmarkItem(activeItem.id, {
        score: score.score,
        rubric_selections: Object.entries(score.rubricSelections).map(([point_id, value]) => ({ point_id, score: value })),
        comments,
        expected_revision: context.expected_revision
      });
      if (!isCurrent()) return;
      message.success(`${label}意见已提交，原成绩不会在此处直接修改`);
      closeActive();
      await load();
    } catch (error) {
      if (isCurrent()) message.error(errorMessage(error, label));
    } finally {
      if (isCurrent()) { busyRef.current = false; setActioning(false); }
    }
  };

  if (!canWork) return null;

  return (
    <>
      <Button icon={<ClipboardCheck size={16} />} onClick={() => setOpen(true)}>{label}任务</Button>
      <Drawer
        title={activeItem ? `独立${label}` : `我的${label}任务`}
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
            <Alert showIcon type="info" message={`独立${label}`} description={`本页不显示原得分、原阅卷人或历史意见。提交只形成${label}候选，后续是否仲裁或重评由质量流程决定。`} />
            <div className="backmark-workspace-grid">
              <BackmarkAnswerEvidence context={context} imageUrl={imageUrl} />
              <section>
                <h3>独立评分</h3>
                <SharedScoreControl value={score} maxScore={context.question.score} rubricPoints={points} onChange={setScore} disabled={actioning} />
                <Input.TextArea disabled={actioning} value={comments} onChange={(event) => setComments(event.target.value)} rows={4} maxLength={2000} showCount placeholder="可选：记录评分依据，供后续质量流程查看" />
                <Space className="backmark-submit-actions">
                  <Button disabled={actioning} onClick={() => leave(closeActive)}>返回队列</Button>
                  <Button type="primary" disabled={score.score === null} loading={actioning} onClick={() => void submit()}>提交独立评分</Button>
                </Space>
              </section>
            </div>
          </div>
        ) : (
          <Spin spinning={loading}>
            {items.length ? <List loadMore={nextCursor ? <div style={{ textAlign: "center", marginTop: 16 }}><Button loading={loading} onClick={() => void load(nextCursor)}>加载更多</Button></div> : undefined} dataSource={items} renderItem={(item) => <List.Item actions={[<Button key="open" type="primary" loading={actioning} onClick={() => void openItem(item)}>开始{label}</Button>]}><List.Item.Meta title={`独立${label} · 满分 ${item.max_score}`} description={`任务创建于 ${new Date(item.created_at).toLocaleString("zh-CN", { hour12: false })}`} /><Tag>{item.status === "in_progress" ? "处理中" : "待处理"}</Tag></List.Item>} /> : <Empty description={`暂无分配给你的${label}任务`} />}
          </Spin>
        )}
      </Drawer>
    </>
  );
}
