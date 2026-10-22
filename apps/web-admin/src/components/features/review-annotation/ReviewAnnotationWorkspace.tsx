import { useEffect, useMemo, useRef, useState, type Dispatch, type PointerEvent as ReactPointerEvent, type SetStateAction } from "react";
import { Alert, Button, Empty, Input, InputNumber, Popconfirm, Segmented, Select, Space, Spin, Tag, Tooltip } from "antd";
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
import { getUserErrorMessage } from "../../../api/client";
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

function canvasLabel(tool: DrawTool) {
  if (tool === "rectangle") return "在答题卡上拖动框选批注区域";
  if (tool === "note") return "在答题卡上点击添加批注";
  return "答题卡批注画布";
}

function AnnotationEditor({ draft, setDraft, annotationCount, readOnly, disabled, saving, rubricCriteria, trailingShortcut, templates, templateSuggestions, updateGeometry, insertTemplate, saveDraft, removeAnnotation }: Readonly<{
  draft?: AnnotationDraft;
  setDraft: Dispatch<SetStateAction<AnnotationDraft | undefined>>;
  annotationCount: number;
  readOnly: boolean;
  disabled: boolean;
  saving: boolean;
  rubricCriteria: CriterionOption[];
  trailingShortcut: string | undefined;
  templates: ReturnType<typeof useReviewAnnotations>["templates"];
  templateSuggestions: ReturnType<typeof useReviewAnnotations>["templates"];
  updateGeometry: (field: "x" | "y" | "width" | "height", percentage: number | null) => void;
  insertTemplate: (shortcut: string) => Promise<void>;
  saveDraft: () => Promise<void>;
  removeAnnotation: ReturnType<typeof useReviewAnnotations>["removeAnnotation"];
}>) {
  // 查看权限保留批注内容与原图定位，不用禁用表单暗示用户还能编辑。
  if (readOnly) return draft ? <div className="review-annotation-editor"><strong>批注内容</strong><p>{draft.content}</p><Tag>{draft.visibility === "private" ? "仅教师可见" : "成绩发布后学生可见"}</Tag></div>
    : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={annotationCount ? "选择图上批注查看内容" : "本题暂无批注"} />;
  // 编辑器只展示父层草稿；保存和删除继续使用父层回调及原批注对象，版本处理仍由共享 hook 负责。
  if (!draft) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={annotationCount ? "选择已有批注，或在原图上新增" : "选择点批注或框选工具开始"} />;
  }
  return (
    <div className="review-annotation-editor">
      <div className="review-annotation-editor-title">
        <strong>{draft.current ? "编辑批注" : "新增批注"}</strong>
        <Tag>{draft.type === "note" ? "点批注" : "矩形批注"}</Tag>
      </div>
      <fieldset className="review-annotation-coordinate-fields" disabled={disabled || saving}>
        <legend>批注位置（原图百分比）</legend>
        {(draft.type === "note" ? ["x", "y"] as const : ["x", "y", "width", "height"] as const).map((field) => (
          <label key={field}>
            <span>{{ x: "距左侧", y: "距顶部", width: "宽度", height: "高度" }[field]}</span>
            <InputNumber aria-label={`批注${{ x: "距左侧", y: "距顶部", width: "宽度", height: "高度" }[field]}百分比`} min={field === "width" || field === "height" ? 0.3 : 0} max={draft.type === "note" || field === "width" || field === "height" ? 100 : 99.7} step={0.1} precision={1} value={Number((draft.geometry[field] * 100).toFixed(1))} disabled={disabled || saving} onChange={(value) => updateGeometry(field, value)} />
          </label>
        ))}
      </fieldset>
      <Input.TextArea
        autoFocus
        aria-label="批注内容"
        rows={5}
        maxLength={4000}
        showCount
        disabled={disabled || saving}
        value={draft.content}
        placeholder="输入评语；键入 /快捷码 后按 Enter 插入个人常用评语"
        onChange={(event) => setDraft((current) => current ? { ...current, content: event.target.value } : current)}
        onKeyDown={(event) => {
          if (event.key !== "Enter" || !trailingShortcut) return;
          const exactTemplate = templates.find((template) => template.shortcut === trailingShortcut);
          if (!exactTemplate) return;
          event.preventDefault();
          void insertTemplate(exactTemplate.shortcut);
        }}
      />
      {templateSuggestions.length ? (
        <div className="review-template-suggestions" aria-label="匹配的常用评语">
          {templateSuggestions.map((template) => (
            <Button key={template.id} size="small" disabled={disabled || saving} onClick={() => void insertTemplate(template.shortcut)}>
              /{template.shortcut} · {template.title}
            </Button>
          ))}
        </div>
      ) : null}
      <label>
        <span>学生可见性</span>
        <Select
          disabled={disabled || saving}
          aria-label="批注学生可见性"
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
            disabled={disabled || saving}
            aria-label="批注关联评分点"
            allowClear
            value={draft.rubricCriterionId}
            onChange={(rubricCriterionId) => setDraft((current) => current ? { ...current, rubricCriterionId } : current)}
            options={rubricCriteria.map((criterion) => ({ value: criterion.id, label: criterion.label }))}
          />
        </label>
      ) : null}
      <Space wrap>
        <Button type="primary" icon={<Check size={16} />} loading={saving} disabled={disabled || !draft.content.trim()} onClick={() => void saveDraft()}>
          保存批注
        </Button>
        <Button disabled={saving} onClick={() => setDraft(undefined)}>取消</Button>
        {draft.current ? (
          <Popconfirm
            title="删除这条批注？"
            okText="删除"
            cancelText="取消"
            onConfirm={async () => {
              if (disabled || saving || !draft.current) return;
              await removeAnnotation(draft.current);
              setDraft(undefined);
            }}
          >
            <Button danger disabled={disabled || saving} type="text" icon={<Trash2 size={16} />}>删除</Button>
          </Popconfirm>
        ) : null}
      </Space>
    </div>
  );
}

export function ReviewAnnotationWorkspace({
  reviewTaskId,
  imageUrl,
  imageAlt = "答题卡原图",
  rubricCriteria = [],
  disabled = false
}: Readonly<ReviewAnnotationWorkspaceProps>) {
  const workspace = useReviewAnnotations(reviewTaskId);
  const imageRef = useRef<HTMLImageElement>(null);
  const [tool, setTool] = useState<DrawTool>("select");
  const [dragStart, setDragStart] = useState<ClientPoint>();
  const [draft, setDraft] = useState<AnnotationDraft>();
  const [templateManagerOpen, setTemplateManagerOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const saveBusy = useRef(false);
  const editDraft: typeof setDraft = value => { if (!saveBusy.current) setDraft(value); };
  const [templateFailure, setTemplateFailure] = useState<{ shortcut: string; error: string }>();
  const [applyingTemplate, setApplyingTemplate] = useState(false);
  const templateTask = useRef(reviewTaskId);
  templateTask.current = reviewTaskId;
  // biome-ignore lint/correctness/useExhaustiveDependencies: 切阅卷任务必须清掉上一份答卷的未提交标注和模板失败状态。
  useEffect(() => { setDraft(undefined); setTemplateFailure(undefined); setApplyingTemplate(false); }, [reviewTaskId]);

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

  const startDrawing = (event: ReactPointerEvent<HTMLElement>) => {
    if (disabled || saveBusy.current || tool === "select") return;
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

  const finishDrawing = (event: ReactPointerEvent<HTMLElement>) => {
    if (disabled || saveBusy.current || tool !== "rectangle" || !dragStart) return;
    const end = canonicalEventPoint(event);
    setDragStart(undefined);
    setTool("select");
    if (!end) return;
    const geometry = canonicalRectangle(dragStart, end);
    if (!hasDrawableArea(geometry)) return;
    setDraft({ type: "rectangle", geometry, content: "", visibility: "private" });
  };

  const createKeyboardDraft = (type: "note" | "rectangle") => {
    if (disabled || saveBusy.current || saving) return;
    setTool("select");
    setDragStart(undefined);
    setDraft({ type, geometry: type === "note" ? canonicalPoint({ x: 0.5, y: 0.5 }) : canonicalRectangle({ x: 0.25, y: 0.25 }, { x: 0.75, y: 0.75 }), content: "", visibility: "private" });
  };

  const updateGeometry = (field: "x" | "y" | "width" | "height", percentage: number | null) => {
    if (percentage === null || !Number.isFinite(percentage) || disabled || saving || saveBusy.current) return;
    setDraft((current) => {
      if (!current) return current;
      const next = { ...current.geometry, [field]: percentage / 100 };
      // 键盘输入沿用画布的原图归一化坐标；矩形保持至少 0.3% 边长并限制在图像内部。
      const geometry = current.type === "note"
        ? canonicalPoint(next)
        : canonicalRectangle(
            { x: Math.min(0.997, Math.max(0, next.x)), y: Math.min(0.997, Math.max(0, next.y)) },
            { x: Math.min(0.997, Math.max(0, next.x)) + Math.max(0.003, next.width), y: Math.min(0.997, Math.max(0, next.y)) + Math.max(0.003, next.height) }
          );
      return { ...current, geometry };
    });
  };

  const saveDraft = async () => {
    if (disabled || saveBusy.current || applyingTemplate || !draft?.content.trim()) return;
    // 保存冻结这份草稿；所有输入/切换入口共用同步锁，旧回执不能静默丢弃后续输入。
    saveBusy.current = true;
    const originalTask = reviewTaskId;
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
      if (templateTask.current === originalTask) setDraft(undefined);
    } catch (saveError) {
      if (templateTask.current === originalTask && saveError instanceof AnnotationRevisionConflict && saveError.latest) {
        setDraft(draftFromAnnotation(saveError.latest));
      }
    } finally {
      saveBusy.current = false;
      if (templateTask.current === originalTask) setSaving(false);
    }
  };

  const insertTemplate = async (shortcut: string) => {
    if (!draft || applyingTemplate || saveBusy.current) return;
    const originalDraft = draft;
    const originalTask = reviewTaskId;
    setApplyingTemplate(true);
    setTemplateFailure(undefined);
    try {
      const template = await workspace.applyTemplate(shortcut);
      if (templateTask.current !== originalTask) return;
      // 返回期间改选批注或编辑内容，就保留当前输入，不能把旧请求结果插进另一份草稿。
      setDraft((current) => current === originalDraft ? {
        ...current,
        content: replaceTrailingTemplateShortcut(current.content, template.content)
      } : current);
    } catch (cause) {
      if (templateTask.current === originalTask) setTemplateFailure({ shortcut, error: getUserErrorMessage(cause, "常用评语插入失败，输入已保留") });
    } finally {
      if (templateTask.current === originalTask) setApplyingTemplate(false);
    }
  };

  if (workspace.loading) {
    return <div className="review-annotation-loading"><Spin /><span>加载批注与个人评语…</span></div>;
  }

  return (
    <section className="review-annotation-workspace" aria-label="答题卡批注">
      <header className="review-annotation-toolbar">
        <Space wrap>
          {!disabled ? <><Segmented<DrawTool>
            value={tool}
            disabled={disabled || saving}
            onChange={setTool}
            options={[
              { value: "select", label: <span className="review-tool-label"><MousePointer2 size={15} />选择</span> },
              { value: "note", label: <span className="review-tool-label"><MessageSquareText size={15} />点批注</span> },
              { value: "rectangle", label: <span className="review-tool-label"><Square size={15} />框选</span> }
            ]}
          />
          <Button disabled={disabled || saving} onClick={() => createKeyboardDraft("note")}>新增点批注</Button>
          <Button disabled={saving} onClick={() => createKeyboardDraft("rectangle")}>新增矩形批注</Button></> : null}
          <Tag>{workspace.annotations.length} 条批注</Tag>
          {workspace.saveState !== "idle" ? <span className={`review-save-state review-save-state--${workspace.saveState}`}>{statusLabel(workspace.saveState)}</span> : null}
        </Space>
        <Space>
          <Tooltip title="只刷新批注，不会认领其他任务">
            <Button type="text" icon={<RefreshCw size={16} />} onClick={() => void workspace.reloadAnnotations()} aria-label="刷新批注" />
          </Tooltip>
          {!disabled ? <Button icon={<Settings2 size={16} />} onClick={() => setTemplateManagerOpen(true)}>常用评语</Button> : null}
        </Space>
      </header>

      {workspace.error ? <Alert type={workspace.saveState === "conflict" ? "warning" : "error"} showIcon message={workspace.error} /> : null}
      {templateFailure ? <Alert type="error" showIcon message={templateFailure.error} action={<Button loading={applyingTemplate} disabled={!draft || disabled || saving} onClick={() => void insertTemplate(templateFailure.shortcut)}>重试插入评语</Button>} /> : null}

      <div className="review-annotation-layout">
        <section
          className={`review-annotation-canvas review-annotation-canvas--${disabled ? "select" : tool}`}
          onPointerDown={startDrawing}
          onPointerUp={finishDrawing}
          aria-label={canvasLabel(disabled ? "select" : tool)}
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
                  disabled={saving}
                  onClick={() => editDraft(draftFromAnnotation(annotation))}
                  aria-label={`${disabled ? "查看" : "编辑"}批注：${annotation.content}`}
                />
              );
            })}
          </div>
        </section>

        <aside className="review-annotation-inspector">
          <AnnotationEditor draft={draft} setDraft={editDraft} annotationCount={workspace.annotations.length} readOnly={disabled} disabled={disabled || saving} saving={saving} rubricCriteria={rubricCriteria} trailingShortcut={trailingShortcut} templates={workspace.templates} templateSuggestions={templateSuggestions} updateGeometry={updateGeometry} insertTemplate={insertTemplate} saveDraft={saveDraft} removeAnnotation={workspace.removeAnnotation} />
        </aside>
      </div>

      {!disabled ? <CommentTemplateManager
        open={templateManagerOpen}
        templates={workspace.templates}
        onClose={() => setTemplateManagerOpen(false)}
        onCreate={workspace.createTemplate}
        onUpdate={workspace.updateTemplate}
        onDelete={workspace.removeTemplate}
      /> : null}
    </section>
  );
}
