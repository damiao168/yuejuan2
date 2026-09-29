import { useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { Alert, Button, Empty, Input, Popconfirm, Segmented, Select, Space, Spin, Tag, Tooltip } from "antd";
import { Check, MessageSquareText, MousePointer2, RefreshCw, Settings2, Square, Trash2 } from "lucide-react";
import type {
  CanonicalImageGeometry,
  CreateReviewAnnotationRequest,
  ReviewAnnotation,
  ReviewAnnotationType,
  ReviewAnnotationVisibility
} from "@edugrade/sdk";
import { CommentTemplateManager } from "./CommentTemplateManager";
import { AnnotationRevisionConflict, useReviewAnnotations } from "./useReviewAnnotations";
import { replaceTrailingTemplateShortcut, trailingTemplateShortcut } from "./templateShortcuts";
import {
  canonicalPoint,
  canonicalRectangle,
  clientPointToCanonical,
  geometryStyle,
  hasDrawableArea,
  type ClientPoint
} from "./geometry";
import "./reviewAnnotations.css";

type DrawTool = "select" | "note" | "rectangle";

interface CriterionOption {
  id: string;
  label: string;
}

interface AnnotationDraft {
  current?: ReviewAnnotation;
  type: ReviewAnnotationType;
  geometry: CanonicalImageGeometry;
  content: string;
  visibility: ReviewAnnotationVisibility;
  rubricCriterionId?: string;
}

export interface ReviewAnnotationWorkspaceProps {
  reviewTaskId: string;
  imageUrl: string;
  imageAlt?: string;
  rubricCriteria?: CriterionOption[];
  disabled?: boolean;
}

function draftFromAnnotation(annotation: ReviewAnnotation): AnnotationDraft {
  const criterion = typeof annotation.payload.rubric_criterion_id === "string"
    ? annotation.payload.rubric_criterion_id
    : undefined;
  return {
    current: annotation,
    type: annotation.type,
    geometry: annotation.geometry,
    content: annotation.content,
    visibility: annotation.visibility,
    rubricCriterionId: criterion
  };
}

function statusLabel(state: ReturnType<typeof useReviewAnnotations>["saveState"]) {
  if (state === "saving") return "正在保存";
  if (state === "saved") return "已保存";
  if (state === "conflict") return "版本已刷新";
  if (state === "error") return "保存失败";
  return "";
}

export function ReviewAnnotationWorkspace({
  reviewTaskId,
  imageUrl,
  imageAlt = "答题卡原图",
  rubricCriteria = [],
  disabled = false
}: ReviewAnnotationWorkspaceProps) {
  const workspace = useReviewAnnotations(reviewTaskId);
  const imageRef = useRef<HTMLImageElement>(null);
  const [tool, setTool] = useState<DrawTool>("select");
  const [dragStart, setDragStart] = useState<ClientPoint>();
  const [draft, setDraft] = useState<AnnotationDraft>();
  const [templateManagerOpen, setTemplateManagerOpen] = useState(false);
  const [saving, setSaving] = useState(false);

  const trailingShortcut = useMemo(() => trailingTemplateShortcut(draft?.content ?? ""), [draft?.content]);

  const templateSuggestions = useMemo(() => {
    if (!trailingShortcut) return [];
    return workspace.templates
      .filter((template) => template.shortcut.startsWith(trailingShortcut))
      .slice(0, 5);
  }, [trailingShortcut, workspace.templates]);

  // 以图片当前显示矩形换算归一化坐标；保存的是图片位置而不是屏幕像素，重新缩放后仍指向同一处。
  const canonicalEventPoint = (event: ReactPointerEvent) => {
    const image = imageRef.current;
    if (!image) return undefined;
    const bounds = image.getBoundingClientRect();
    return clientPointToCanonical({ x: event.clientX, y: event.clientY }, bounds);
  };

  const startDrawing = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (disabled || tool === "select") return;
    const point = canonicalEventPoint(event);
    if (!point) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    if (tool === "note") {
      setDraft({ type: "note", geometry: canonicalPoint(point), content: "", visibility: "private" });
      setTool("select");
      return;
    }
    setDragStart(point);
  };

  const finishDrawing = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (disabled || tool !== "rectangle" || !dragStart) return;
    const end = canonicalEventPoint(event);
    setDragStart(undefined);
    setTool("select");
    if (!end) return;
    const geometry = canonicalRectangle(dragStart, end);
    if (!hasDrawableArea(geometry)) return;
    setDraft({ type: "rectangle", geometry, content: "", visibility: "private" });
  };

  const saveDraft = async () => {
    if (!draft || !draft.content.trim()) return;
    const input: CreateReviewAnnotationRequest = {
      type: draft.type,
      geometry: draft.geometry,
      content: draft.content.trim(),
      visibility: draft.visibility,
      payload: draft.rubricCriterionId ? { rubric_criterion_id: draft.rubricCriterionId } : {}
    };
    setSaving(true);
    try {
      await workspace.saveAnnotation(input, draft.current);
      setDraft(undefined);
    } catch (saveError) {
      if (saveError instanceof AnnotationRevisionConflict && saveError.latest) {
        setDraft(draftFromAnnotation(saveError.latest));
      }
    } finally {
      setSaving(false);
    }
  };

  const insertTemplate = async (shortcut: string) => {
    if (!draft) return;
    try {
      const template = await workspace.applyTemplate(shortcut);
      setDraft((current) => current ? {
        ...current,
        content: replaceTrailingTemplateShortcut(current.content, template.content)
      } : current);
    } catch {
      // The shared hook/API error is surfaced by the surrounding workspace.
    }
  };

  if (workspace.loading) {
    return <div className="review-annotation-loading"><Spin /><span>加载批注与个人评语…</span></div>;
  }

  return (
    <section className="review-annotation-workspace" aria-label="答题卡批注">
      <header className="review-annotation-toolbar">
        <Space wrap>
          <Segmented<DrawTool>
            value={tool}
            disabled={disabled}
            onChange={setTool}
            options={[
              { value: "select", label: <span className="review-tool-label"><MousePointer2 size={15} />选择</span> },
              { value: "note", label: <span className="review-tool-label"><MessageSquareText size={15} />点批注</span> },
              { value: "rectangle", label: <span className="review-tool-label"><Square size={15} />框选</span> }
            ]}
          />
          <Tag>{workspace.annotations.length} 条批注</Tag>
          {workspace.saveState !== "idle" ? <span className={`review-save-state review-save-state--${workspace.saveState}`}>{statusLabel(workspace.saveState)}</span> : null}
        </Space>
        <Space>
          <Tooltip title="只刷新批注，不会认领其他任务">
            <Button type="text" icon={<RefreshCw size={16} />} onClick={() => void workspace.reloadAnnotations()} aria-label="刷新批注" />
          </Tooltip>
          <Button icon={<Settings2 size={16} />} onClick={() => setTemplateManagerOpen(true)}>常用评语</Button>
        </Space>
      </header>

      {workspace.error ? <Alert type={workspace.saveState === "conflict" ? "warning" : "error"} showIcon message={workspace.error} /> : null}

      <div className="review-annotation-layout">
        <div
          className={`review-annotation-canvas review-annotation-canvas--${tool}`}
          onPointerDown={startDrawing}
          onPointerUp={finishDrawing}
          role="application"
          aria-label={tool === "rectangle" ? "在答题卡上拖动框选批注区域" : tool === "note" ? "在答题卡上点击添加批注" : "答题卡批注画布"}
        >
          <img ref={imageRef} src={imageUrl} alt={imageAlt} draggable={false} />
          <div className="review-annotation-overlay">
            {workspace.annotations.map((annotation) => {
              const point = annotation.geometry.width === 0 && annotation.geometry.height === 0;
              return (
                <button
                  type="button"
                  key={annotation.id}
                  className={`${point ? "review-annotation-mark review-annotation-mark--point" : "review-annotation-mark"}${draft?.current?.id === annotation.id ? " is-selected" : ""}`}
                  style={geometryStyle(annotation.geometry)}
                  title={annotation.content}
                  onPointerDown={(event) => event.stopPropagation()}
                  onClick={() => setDraft(draftFromAnnotation(annotation))}
                  aria-label={`编辑批注：${annotation.content}`}
                />
              );
            })}
          </div>
        </div>

        <aside className="review-annotation-inspector">
          {!draft ? (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={workspace.annotations.length ? "选择已有批注，或在原图上新增" : "选择点批注或框选工具开始"}
            />
          ) : (
            <div className="review-annotation-editor">
              <div className="review-annotation-editor-title">
                <strong>{draft.current ? "编辑批注" : "新增批注"}</strong>
                <Tag>{draft.type === "note" ? "点批注" : "矩形批注"}</Tag>
              </div>
              <Input.TextArea
                autoFocus
                rows={5}
                maxLength={4000}
                showCount
                value={draft.content}
                placeholder="输入评语；键入 /快捷码 后按 Enter 插入个人常用评语"
                onChange={(event) => setDraft((current) => current ? { ...current, content: event.target.value } : current)}
                onKeyDown={(event) => {
                  if (event.key !== "Enter" || !trailingShortcut) return;
                  const exactTemplate = workspace.templates.find((template) => template.shortcut === trailingShortcut);
                  if (!exactTemplate) return;
                  event.preventDefault();
                  void insertTemplate(exactTemplate.shortcut);
                }}
              />
              {templateSuggestions.length ? (
                <div className="review-template-suggestions" aria-label="匹配的常用评语">
                  {templateSuggestions.map((template) => (
                    <Button key={template.id} size="small" onClick={() => void insertTemplate(template.shortcut)}>
                      /{template.shortcut} · {template.title}
                    </Button>
                  ))}
                </div>
              ) : null}
              <label>
                <span>学生可见性</span>
                <Select
                  value={draft.visibility}
                  onChange={(visibility) => setDraft((current) => current ? { ...current, visibility } : current)}
                  options={[
                    { value: "private", label: "仅教师可见" },
                    { value: "student_after_publish", label: "成绩发布后学生可见" }
                  ]}
                />
              </label>
              {rubricCriteria.length ? (
                <label>
                  <span>关联评分点（可选）</span>
                  <Select
                    allowClear
                    value={draft.rubricCriterionId}
                    onChange={(rubricCriterionId) => setDraft((current) => current ? { ...current, rubricCriterionId } : current)}
                    options={rubricCriteria.map((criterion) => ({ value: criterion.id, label: criterion.label }))}
                  />
                </label>
              ) : null}
              <Space wrap>
                <Button type="primary" icon={<Check size={16} />} loading={saving} disabled={!draft.content.trim()} onClick={() => void saveDraft()}>
                  保存批注
                </Button>
                <Button onClick={() => setDraft(undefined)}>取消</Button>
                {draft.current ? (
                  <Popconfirm
                    title="删除这条批注？"
                    okText="删除"
                    cancelText="取消"
                    onConfirm={async () => {
                      if (!draft.current) return;
                      await workspace.removeAnnotation(draft.current);
                      setDraft(undefined);
                    }}
                  >
                    <Button danger type="text" icon={<Trash2 size={16} />}>删除</Button>
                  </Popconfirm>
                ) : null}
              </Space>
            </div>
          )}
        </aside>
      </div>

      <CommentTemplateManager
        open={templateManagerOpen}
        templates={workspace.templates}
        onClose={() => setTemplateManagerOpen(false)}
        onCreate={workspace.createTemplate}
        onUpdate={workspace.updateTemplate}
        onDelete={workspace.removeTemplate}
      />
    </section>
  );
}
