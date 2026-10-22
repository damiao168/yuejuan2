import { useEffect, useState } from "react";
import { Alert, Button, Checkbox, Modal, Select, Space } from "antd";
import type { useTemplateLibrary } from "./useTemplateLibrary";

export function TemplateLibraryPanel({ library, canManage, canEditSources, canCollect, busy, hasTemplate, confirmDiscard, reviewKey }: {
  library: ReturnType<typeof useTemplateLibrary>;
  canManage: boolean; canEditSources: boolean; canCollect: boolean; busy: boolean; hasTemplate: boolean;
  confirmDiscard: (action: () => void) => void;
  reviewKey: string;
}) {
  const [selectedId, setSelectedId] = useState<string>();
  const [reviewOpen, setReviewOpen] = useState(false);
  const [blankConfirmed, setBlankConfirmed] = useState(false);
  const [layoutConfirmed, setLayoutConfirmed] = useState(false);
  // 核对窗口打开后继续改框或换版本，会使先前的核对勾选失效。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 核对版本是失效触发器；恢复到旧布局也不能复活旧勾选。
  useEffect(() => { setReviewOpen(false); setBlankConfirmed(false); setLayoutConfirmed(false); }, [reviewKey]);
  const blocked = busy || library.busy;
  return <section className="template-library-panel" aria-label="学校答题卡模板库">
    <details>
    <summary>学校模板库</summary>
    <Space wrap>
      <Select aria-label="学校答题卡模板" style={{ minWidth: 240 }} placeholder="选择已收录模板用于本场考试"
        value={selectedId} loading={library.loading} disabled={blocked || !canEditSources}
        options={library.libraries.map(item => ({ value: item.id, label: `${item.name} · 第 ${item.version_no} 版 · ${item.page_count} 页` }))}
        onChange={setSelectedId} />
      <Button disabled={blocked || !canEditSources || !selectedId || !library.libraries.some(item => item.id === selectedId)}
        onClick={() => confirmDiscard(() => void library.instantiate(selectedId!))}>用于本场考试</Button>
      <Button disabled={blocked} loading={library.loading} onClick={() => void library.refresh()}>刷新模板库</Button>
      {canManage && hasTemplate ? <Button disabled={blocked || !canCollect} onClick={() => {
        setBlankConfirmed(false); setLayoutConfirmed(false); setReviewOpen(true);
      }}>核对并收录模板库</Button> : null}
    </Space>
    <p className="muted">选择学校已收录的答题卡，或将当前空白答题卡收录供下次复用。</p>
    </details>
    {/* 复用入口默认收起；加载失败、匹配冲突和操作结果仍须直接可见，不能随折叠隐藏。 */}
    {library.listError ? <Alert type="warning" showIcon message={library.listError} /> : null}
    {library.error ? <Alert type="error" showIcon message={library.error}
      action={canEditSources && library.errorStage === "match" ? <Button disabled={blocked} onClick={() => void library.resolveSource()}>重试模板匹配</Button> : undefined} /> : null}
    {library.busy ? <p role="status">正在处理学校模板，请稍候…</p> : null}
    {library.resolution?.status === "ambiguous" ? <Alert type="warning" showIcon message="模板匹配存在冲突，请先核对已有模板。"
      description={<Space wrap>{library.resolution.candidates.map(item => <Button key={item.id} disabled={blocked || !canEditSources}
        onClick={() => confirmDiscard(() => void library.instantiate(item.id))}>核对后使用：{item.name}</Button>)}</Space>} /> : null}
    {library.published ? <Alert type="success" showIcon message={`已收录：${library.published.name} · 第 ${library.published.version_no} 版`}
      description="学校模板库已保存。本场考试是否启用仍需在“确认答题卡设置”中完成。" /> : null}
    {reviewOpen ? <Modal title="核对并收录学校模板库" open onCancel={() => { if (!library.busy) setReviewOpen(false); }}
      okText="确认收录" cancelText="继续核对" confirmLoading={library.busy}
      okButtonProps={{ disabled: !canCollect || !blankConfirmed || !layoutConfirmed || blocked }}
      onOk={() => { if (!blankConfirmed || !layoutConfirmed || !canCollect || blocked) return; setReviewOpen(false); void library.publish(); }}>
      <p>收录会保存并锁定当前版本；已收录布局供学校后续考试复用。本场启用另行确认。</p>
      <p><Checkbox checked={blankConfirmed} onChange={event => setBlankConfirmed(event.target.checked)}>确认底图为空白答题卡，且无学生姓名、学号、条码或手写答案等个人信息</Checkbox></p>
      <p><Checkbox checked={layoutConfirmed} onChange={event => setLayoutConfirmed(event.target.checked)}>已逐页核对题号和完整作答区域，区域适用于同版式答题卡</Checkbox></p>
      <Alert type="info" showIcon message="已填写的学生答题卡可辅助识别；请换成经过核对的空白来源后再收录学校模板库。" />
    </Modal> : null}
  </section>;
}
