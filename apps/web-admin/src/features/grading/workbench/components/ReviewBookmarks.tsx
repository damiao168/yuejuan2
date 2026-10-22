import { useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Button, Drawer, Input, Modal, Select, Space, Spin } from "antd";
import { Bookmark, BookmarkCheck, ListChecks, Pencil, Trash2 } from "lucide-react";
import { ApiClientError } from "../../../../api/client";
import { examSubjectOptions } from "../../../../constants/examStatus";
import { getPersonalReviewBookmarkCleanup, getPersonalReviewBookmarkContext, getPersonalReviewBookmarkImage, reviewBookmarkCodePointLength, type PersonalReviewBookmark, type PersonalReviewBookmarkContext, type PersonalReviewBookmarkFilter } from "../../../../api/reviewBookmarks";
import { fetchReviewBookmarkPage, onReviewBookmarkRemoved, reviewBookmarkKeys, reviewBookmarkStateOptions } from "../../../../query/reviewBookmarks";
import { assertReviewTaskContextScope, isReviewTaskContextScopeCurrent, type ReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import type { PersonalReviewBookmarkController } from "../hooks/usePersonalReviewBookmarks";

export function ReviewBookmarkReasonDialog({ scope, bookmark, controller, onClose, onDirtyChange }: { scope: ReviewTaskContextScope; bookmark: PersonalReviewBookmark; controller: PersonalReviewBookmarkController; onClose: () => void; onDirtyChange?: (dirty: boolean) => void }) {
  const { modal } = App.useApp();
  const state = useQuery(reviewBookmarkStateOptions(scope, bookmark.task_id));
  const [reason, setReason] = useState(bookmark.reason);
  const current = state.data ?? bookmark;
  // 编辑始终绑定打开时的版本；后台刷新不能替用户确认覆盖另一会话的新说明。
  const versionChanged = current.revision !== bookmark.revision || current.marked !== bookmark.marked;
  const dirty = reason !== bookmark.reason;
  useEffect(() => { onDirtyChange?.(dirty); return () => onDirtyChange?.(false); }, [dirty, onDirtyChange]);
  const close = () => {
    if (controller.busy) return;
    if (!dirty) { onClose(); return; }
    modal.confirm({ title: "放弃未保存的复查说明？", okText: "放弃修改", cancelText: "继续编辑", onOk: onClose });
  };
  const length = reviewBookmarkCodePointLength(reason);
  return <Modal open title="复查说明 · 仅自己可见" onCancel={close} okText="保存说明" cancelText="关闭" confirmLoading={controller.busy} okButtonProps={{ disabled: length > 200 || !current.can_edit_reason || state.isPending || state.isError || versionChanged || !current.marked || Boolean(controller.uncertainTask) }} onOk={async () => { if (await controller.write(bookmark, reason)) onClose(); }} destroyOnHidden>
    <Input.TextArea autoFocus aria-label="复查说明" value={reason} rows={4} onChange={(event) => setReason(event.target.value)} status={length > 200 ? "error" : undefined} />
    <div className="bookmark-reason-count" role="status">{length} / 200</div>
    {versionChanged ? <Alert type="warning" showIcon message={current.marked ? "复查说明已在其他会话变化，请先核对。" : "这条标记已移出清单，不能从说明编辑中重新加入。"} description={current.marked ? <p className="bookmark-plain-text">最新说明：{current.reason || "未填写说明"}</p> : undefined} action={current.marked && current.can_edit_reason ? <Button disabled={length > 200 || controller.busy || Boolean(controller.uncertainTask)} onClick={async () => { if (await controller.write(current, reason)) onClose(); }}>按最新版本保存本页说明</Button> : undefined} /> : null}
    {!current.can_edit_reason ? <Alert type="info" message="当前任务不能修改复查说明。" /> : null}
    {controller.error ? <Alert type="warning" showIcon message={controller.error} action={controller.uncertainTask ? <Button loading={controller.busy} onClick={() => void controller.retry()}>重试原操作</Button> : undefined} /> : null}
  </Modal>;
}

export function ReviewBookmarkControls({ scope, controller, onDirtyChange }: { scope: ReviewTaskContextScope; controller: PersonalReviewBookmarkController; onDirtyChange?: (dirty: boolean) => void }) {
  const [editing, setEditing] = useState<PersonalReviewBookmark | null>(null);
  const { state } = controller;
  useEffect(() => { setEditing(null); }, [scope, state.data?.task_id]);
  return <>
    <Button icon={state.data?.marked ? <BookmarkCheck size={16} /> : <Bookmark size={16} />} aria-pressed={Boolean(state.data?.marked)} disabled={!state.data || controller.busy || (!state.data.marked && !state.data.can_edit_reason) || Boolean(controller.uncertainTask)} onClick={() => { if (state.data) void controller.write(state.data, state.data.marked ? null : ""); }}>稍后复查</Button>
    {state.data?.marked ? <Button icon={<Pencil size={15} />} aria-label="编辑复查说明" disabled={controller.busy || !state.data.can_edit_reason} onClick={() => { if (state.data) setEditing(state.data); }} /> : null}
    {state.isError ? <Button size="small" onClick={() => void state.refetch()}>重试复查状态</Button> : null}
    {editing ? <ReviewBookmarkReasonDialog scope={scope} bookmark={editing} controller={controller} onClose={() => setEditing(null)} onDirtyChange={onDirtyChange} /> : null}
  </>;
}

// 此容器只调用书签 context/image，不挂载领取、续租、草稿、数学或批注写入 hooks。
export function ReviewBookmarkReadOnly({ scope, bookmarkId, onClose, onUnavailable }: { scope: ReviewTaskContextScope; bookmarkId: string; onClose: () => void; onUnavailable: () => void }) {
  const [context, setContext] = useState<PersonalReviewBookmarkContext | null>(null);
  const [imageUrl, setImageUrl] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0);
  const [cancelled, setCancelled] = useState(false);
  const generation = useRef(0);
  useEffect(() => {
    const request = ++generation.current;
    const controller = new AbortController();
    let url = "";
    const current = () => !controller.signal.aborted && request === generation.current && isReviewTaskContextScopeCurrent(scope);
    const purge = () => { controller.abort(); if (url) URL.revokeObjectURL(url); url = ""; setImageUrl(""); setContext(null); };
    const unsubscribeRemoval = onReviewBookmarkRemoved(scope, bookmarkId, () => {
      // 即使 context 或图片响应已经在途，也不能在取消后首次展示材料。
      purge(); setCancelled(true); setLoading(false); setError("这条标记已移出清单，请返回清单。");
    });
    const onSession = () => { if (!isReviewTaskContextScopeCurrent(scope)) { purge(); setLoading(false); setError("登录状态已变化，请重新打开记录。"); } };
    window.addEventListener("business-command-scope-changed", onSession);
    setCancelled(false); setLoading(true); setError(""); setContext(null); setImageUrl("");
    const load = async () => {
      try {
        assertReviewTaskContextScope(scope);
        const data = await getPersonalReviewBookmarkContext(bookmarkId, controller.signal);
        if (!current()) return;
        if (data.answer.image_available) {
          const image = await getPersonalReviewBookmarkImage(bookmarkId, data.answer.evidence_version, controller.signal);
          if (!current()) return;
          if (!image.contentType.startsWith("image/")) throw new Error("答题图片格式不可用。");
          url = URL.createObjectURL(image.blob);
        }
        // 图像校验完成后才同时展示文字，避免旧文字与变更后的图像拼在一起。
        if (current()) { setContext(data); setImageUrl(url); }
      } catch (cause) {
        if (!current()) return;
        if (url) URL.revokeObjectURL(url);
        setContext(null); setImageUrl("");
        if (cause instanceof ApiClientError && [401, 403, 404].includes(cause.status)) { setError("这条复查记录当前不可用。"); onUnavailable(); }
        else if (cause instanceof ApiClientError && cause.status === 409) setError("答题材料已变化，请重新加载整份材料。");
        else setError("材料读取失败，请重试。");
      } finally { if (current()) setLoading(false); }
    };
    void load();
    return () => { generation.current += 1; controller.abort(); if (url) URL.revokeObjectURL(url); unsubscribeRemoval(); window.removeEventListener("business-command-scope-changed", onSession); };
  }, [scope, bookmarkId, attempt, onUnavailable]);
  return <Modal open className="bookmark-readonly-modal" title="复查材料 · 只读" width={1160} footer={<Button onClick={onClose}>返回清单</Button>} onCancel={onClose} destroyOnHidden>
    {loading ? <div className="bookmark-loading"><Spin /> 正在读取答题材料</div> : null}
    {error ? <Alert showIcon type="warning" message={error} action={!cancelled ? <Button onClick={() => setAttempt((n) => n + 1)}>重新加载</Button> : undefined} /> : null}
    {context ? <div className="bookmark-readonly">
      <header><strong>{context.task.anonymous_code || "匿名答卷"} · {context.task.question_no}</strong><span>{context.task.exam_name} · {context.task.state === "archived" ? "考试归档·只读" : context.own_submission ? "已提交·只读" : "材料只读"}</span></header>
      {context.answer.material_change !== "unchanged" ? <Alert type="info" showIcon message={context.answer.material_change === "changed" ? "答题图已变化，当前显示现有材料。" : "无法确认答题图与标记时是否一致。"} /> : null}
      <section className="bookmark-readonly-layout">
        <div className="bookmark-readonly-answer">{imageUrl ? <img src={imageUrl} alt="当前授权的答题图" /> : <p className="muted">当前没有可查看的答题图。</p>}</div>
        <aside><h3>本人本次提交</h3>{context.own_submission ? <><strong className="bookmark-submitted-score">{context.own_submission.score} / {context.own_submission.max_score}</strong><p>{new Date(context.own_submission.submitted_at).toLocaleString("zh-CN")}</p>{(context.own_submission.rubric_selections ?? []).map((selection) => <p key={selection.point_id}>{(context.question.rubric.points ?? []).find((point) => point.id === selection.point_id)?.description ?? "评分点"}：{selection.score}</p>)}</> : <p>本任务尚无本人提交记录。</p>}<h3>复查说明</h3><p className="bookmark-plain-text">{context.bookmark.reason || "未填写说明"}</p></aside>
      </section>
      <details><summary>题目与评分细则</summary><p className="bookmark-plain-text">{context.question.stem}</p>{(context.question.options ?? []).map((option, index) => <p key={`${index}-${option}`}>{option}</p>)}{(context.question.rubric.points ?? []).map((point) => <p key={point.id}>{point.description} · {point.score} 分</p>)}</details>
      {context.answer.text ? <details><summary>识别文字 · 与图像的对应关系未核对</summary><p className="bookmark-plain-text">{context.answer.text}</p></details> : null}
    </div> : null}
  </Modal>;
}

export function ReviewBookmarksDrawer({ scope, controller, open, initialExamId, onClose, onDirtyChange }: { scope: ReviewTaskContextScope; controller: PersonalReviewBookmarkController; open: boolean; initialExamId: string; onClose: () => void; onDirtyChange?: (dirty: boolean) => void }) {
  const client = useQueryClient();
  const { modal } = App.useApp();
  const [filter, setFilter] = useState<PersonalReviewBookmarkFilter>({ exam_id: initialExamId });
  const [keyword, setKeyword] = useState("");
  const [selected, setSelected] = useState("");
  const [editing, setEditing] = useState<PersonalReviewBookmark | null>(null);
  const [cleanupOpen, setCleanupOpen] = useState(false);
  useEffect(() => { if (!open) { setSelected(""); setEditing(null); } }, [open]);
  useEffect(() => { const timer = window.setTimeout(() => setFilter((f) => ({ ...f, q: keyword })), 300); return () => window.clearTimeout(timer); }, [keyword]);
  useEffect(() => { setFilter({ exam_id: initialExamId }); setKeyword(""); setSelected(""); setEditing(null); setCleanupOpen(false); }, [scope, initialExamId]);
  const list = useInfiniteQuery({ queryKey: reviewBookmarkKeys.list(scope, filter), initialPageParam: "", queryFn: ({ pageParam, signal }) => fetchReviewBookmarkPage(scope, filter, pageParam, signal), getNextPageParam: (page) => page.next_cursor || undefined, enabled: open && Boolean(scope.session.scope), retry: false, gcTime: 0, staleTime: 0 });
  const cleanup = useQuery({ queryKey: [...reviewBookmarkKeys.scope(scope), "cleanup"], queryFn: async ({ signal }) => { assertReviewTaskContextScope(scope); const result = await getPersonalReviewBookmarkCleanup(signal); assertReviewTaskContextScope(scope); return result.items; }, enabled: open && cleanupOpen && Boolean(scope.session.scope), retry: false, gcTime: 0 });
  const unavailable = useMemo(() => () => { void client.invalidateQueries({ queryKey: reviewBookmarkKeys.scope(scope) }); }, [client, scope]);
  const rows = [...new Map((list.data?.pages.flatMap((page) => page.items) ?? []).map((item) => [item.id, item])).values()];
  const first = list.data?.pages[0];
  const update = (key: keyof PersonalReviewBookmarkFilter, value: string) => setFilter((current) => ({ ...current, [key]: value, ...(key === "subject" ? { exam_id: "", question_id: "" } : key === "exam_id" ? { question_id: "" } : {}) }));
  const remove = (bookmark: PersonalReviewBookmark) => modal.confirm({ title: "从个人复查清单移除？", content: "不改变任务、评分或草稿。", okText: "移除", cancelText: "保留", onOk: async () => { if (await controller.write(bookmark, null)) { await list.refetch(); if (cleanupOpen) await cleanup.refetch(); } } });
  return <>
    <Drawer title={<span><ListChecks size={18} /> 我的复查</span>} open={open} width={520} onClose={onClose} destroyOnHidden={false}>
      <div className="bookmark-filters">
        <Select aria-label="复查学科" allowClear value={filter.subject || undefined} placeholder="全部学科" options={examSubjectOptions} onChange={(value) => update("subject", value || "")} />
        <Select aria-label="复查考试" allowClear value={filter.exam_id || undefined} placeholder="全部考试" options={(first?.exams ?? []).map((item) => ({ value: item.id, label: item.name }))} onChange={(value) => update("exam_id", value || "")} />
        <Select aria-label="复查题目" allowClear value={filter.question_id || undefined} placeholder="全部题目" options={(first?.questions ?? []).map((item) => ({ value: item.id, label: item.question_no }))} onChange={(value) => update("question_id", value || "")} />
        <Select aria-label="复查状态" value={filter.state || ""} options={[{ value: "", label: "全部状态" }, { value: "processable", label: "待处理" }, { value: "submitted", label: "已提交" }]} onChange={(value) => update("state", value)} />
        <Input aria-label="搜索复查" placeholder="搜索考试、题号或匿名码" value={keyword} onChange={(event) => setKeyword(event.target.value)} />
      </div>
      <div className="bookmark-list-summary"><span>{first ? `共 ${first.total_count} 条` : "个人复查清单"}</span><Button onClick={() => void list.refetch()}>刷新</Button></div>
      {controller.error ? <Alert type="warning" showIcon message={controller.error} action={controller.uncertainTask ? <Button loading={controller.busy} onClick={() => void controller.retry()}>重试原操作</Button> : undefined} /> : null}
      {list.isPending ? <Spin /> : list.isError ? <Alert type="error" message="复查清单读取失败" action={<Button onClick={() => void list.refetch()}>重试</Button>} /> : rows.length ? <ul className="bookmark-list">{rows.map((item) => <li key={item.id}>
        <button type="button" className="bookmark-open" onClick={() => setSelected(item.id!)}><small>{item.exam_name} · {item.question_no}</small><strong>{item.anonymous_code || "匿名答卷"}</strong><span>{item.state === "submitted" || item.state === "completed" ? "已提交·只读" : item.state === "archived" ? "考试归档·只读" : "答题材料"}</span><p>{item.reason || "未填写说明"}</p></button>
        <Space><Button size="small" icon={<Pencil size={14} />} disabled={!item.can_edit_reason || controller.busy} onClick={() => setEditing(item)}>说明</Button><Button size="small" icon={<Trash2 size={14} />} disabled={controller.busy || Boolean(controller.uncertainTask)} onClick={() => remove(item)}>移除</Button></Space>
      </li>)}</ul> : <div className="bookmark-empty"><p>{Object.values(filter).some(Boolean) ? "当前筛选下没有复查记录。" : "还没有加入复查的答卷。"}</p>{Object.values(filter).some(Boolean) ? <Button onClick={() => { setFilter({}); setKeyword(""); }}>清除筛选</Button> : null}</div>}
      {list.hasNextPage ? <Button block loading={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>{list.isFetchNextPageError ? "重试加载更多" : "加载更多"}</Button> : null}
      <details className="bookmark-cleanup" open={cleanupOpen} onToggle={(event) => setCleanupOpen(event.currentTarget.open)}><summary>清理已不可用的个人标记</summary><p className="muted">仅提供个人记录编号，不能查看原答卷或说明。</p>{cleanup.isPending ? <Spin /> : cleanup.isError ? <Button onClick={() => void cleanup.refetch()}>重试读取</Button> : cleanup.data?.length ? cleanup.data.map((item) => <div key={item.bookmark_id}><span>记录 {item.bookmark_id.slice(0, 8)}</span><Button size="small" disabled={controller.busy} onClick={() => remove({ id: item.bookmark_id, task_id: "", reason: "", revision: item.revision, marked: true, can_edit_reason: false })}>移除</Button></div>) : <p>暂无需要清理的记录。</p>}</details>
    </Drawer>
    {open && selected ? <ReviewBookmarkReadOnly scope={scope} bookmarkId={selected} onClose={() => setSelected("")} onUnavailable={unavailable} /> : null}
    {open && editing ? <ReviewBookmarkReasonDialog scope={scope} bookmark={editing} controller={controller} onClose={() => setEditing(null)} onDirtyChange={onDirtyChange} /> : null}
  </>;
}
