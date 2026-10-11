import { useCallback, useEffect, useRef } from "react";
import { App } from "antd";
import { useUnsavedChangesGuard } from "../../../../router/useUnsavedChangesGuard";
import { getBusinessCommandSession, isBusinessCommandSessionCurrent } from "../../../../api/businessCommand";

/** 独立评分尚未提交时不能跨任务恢复；抽屉关闭和站内导航使用同一丢弃规则。 */
export function useIndependentReviewGuard(dirty: boolean, busy: boolean, kind: string, reset: () => void) {
  const { modal, message } = App.useApp();
  const busyRef = useRef(false);
  const mounted = useRef(true);
  const session = getBusinessCommandSession();
  const resetRef = useRef(reset); resetRef.current = reset;
  const isCurrent = useCallback(() => mounted.current && isBusinessCommandSessionCurrent(session), [session]);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => {
    // 同页换账号或解锁也会更换会话，旧任务内容须立即清空，迟到响应只能丢弃。
    const changed = () => { busyRef.current = false; resetRef.current(); };
    window.addEventListener("business-command-scope-changed", changed);
    return () => window.removeEventListener("business-command-scope-changed", changed);
  }, []);
  useUnsavedChangesGuard({ dirty, busy, description: `${kind}的分数、评分点和说明尚未提交，离开后无法恢复。` });
  const leave = (action: () => void) => {
    // ref 在 React 下一次渲染前即生效，避免快速关闭或双击突破提交等待保护。
    if (!isCurrent()) return;
    if (busyRef.current) { message.warning("正在加载或提交，请等待结果后操作。"); return; }
    if (!dirty) { action(); return; }
    modal.confirm({ title: `放弃未提交的${kind}意见？`, content: "当前分数、评分点和说明将被清除。", okText: "放弃并返回", cancelText: "继续评分", okButtonProps: { danger: true }, onOk: () => { if (isCurrent() && !busyRef.current) action(); } });
  };
  return { busyRef, isCurrent, leave };
}
