import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, List, Popconfirm } from "antd";
import { getPersonalReviewBookmarkCleanup } from "../../../../api/reviewBookmarks";
import { reviewBookmarkKeys } from "../../../../query/reviewBookmarks";
import { assertReviewTaskContextScope, useReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import { usePersonalReviewBookmarks } from "../hooks/usePersonalReviewBookmarks";

// 账号入口只读本人待清理标识，撤销阅卷权限后也不调用任务清单、材料或说明接口。
export function PersonalReviewBookmarkCleanup() {
  const scope = useReviewTaskContextScope();
  const controller = usePersonalReviewBookmarks(scope, "");
  const [open, setOpen] = useState(false);
  const records = useQuery({ queryKey: [...reviewBookmarkKeys.scope(scope), "cleanup"], enabled: open && Boolean(scope.session.scope), retry: false, gcTime: 0,
    queryFn: async ({ signal }) => { assertReviewTaskContextScope(scope); const result = await getPersonalReviewBookmarkCleanup(signal); assertReviewTaskContextScope(scope); return result.items; } });
  return <section className="workspace-section">
    <div className="section-heading compact"><div><h2>个人复查标记</h2><p>权限变化后可移除已不可用的个人标记；此处仅显示记录编号。</p></div><Button aria-expanded={open} onClick={() => setOpen((value) => !value)}>{open ? "收起清理" : "清理不可用标记"}</Button></div>
    {open ? <>
      {controller.error ? <Alert type="warning" message={controller.error} action={controller.uncertainTask ? <Button loading={controller.busy} onClick={async () => { if (await controller.retry()) await records.refetch(); }}>重试原操作</Button> : undefined} /> : null}
      {records.isError ? <Alert type="warning" message="个人标记读取失败" action={<Button onClick={() => void records.refetch()}>重试</Button>} /> : <List loading={records.isPending} locale={{ emptyText: "暂无需要清理的标记" }} dataSource={records.data ?? []} rowKey="bookmark_id" renderItem={(item) => <List.Item actions={[<Popconfirm key="remove" title="移除这条个人标记？" okText="移除" cancelText="保留" onConfirm={async () => { const success = await controller.write({ id: item.bookmark_id, task_id: "", marked: true, reason: "", revision: item.revision, can_edit_reason: false }, null); if (success) await records.refetch(); }}><Button disabled={controller.busy || Boolean(controller.uncertainTask)}>移除</Button></Popconfirm>]}><span>记录 {item.bookmark_id}</span></List.Item>} />}
    </> : null}
  </section>;
}
