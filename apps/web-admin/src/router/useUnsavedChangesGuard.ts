import { useEffect, useRef } from "react";
import { useRouter } from "@tanstack/react-router";
import { App } from "antd";
import { subscribeBeforeUnload } from "@edugrade/sdk";

/** 调用方判断仍需处理的内容；站内导航先确认，刷新和关闭交给浏览器提示。 */
export function useUnsavedChangesGuard({ dirty, busy = false, description, title = "放弃未保存的内容并离开？", okText = "放弃并离开", cancelText = "继续编辑" }: { dirty: boolean; busy?: boolean; description: string; title?: string; okText?: string; cancelText?: string }) {
  const router = useRouter({ warn: false });
  const { modal, message } = App.useApp();
  const stateRef = useRef({ dirty, busy, description, title, okText, cancelText });
  stateRef.current = { dirty, busy, description, title, okText, cancelText };

  useEffect(() => {
    if (!router?.history) return;
    // history.block 同时覆盖左栏、考试内导航和浏览器前进/后退，避免只保护页面内选择。
    return router.history.block({
      enableBeforeUnload: false,
      blockerFn: ({ currentLocation, nextLocation }) => {
        if (currentLocation.href === nextLocation.href) return false;
        if (stateRef.current.busy) {
          message.warning("正在保存或提交，请等待结果后离开。");
          return true;
        }
        if (!stateRef.current.dirty) return false;
        return new Promise<boolean>((resolve) => {
          modal.confirm({
            title: stateRef.current.title,
            content: stateRef.current.description,
            okText: stateRef.current.okText,
            okButtonProps: { danger: true },
            cancelText: stateRef.current.cancelText,
            onOk: () => resolve(false),
            onCancel: () => resolve(true),
          });
        });
      },
    });
  }, [message, modal, router]);

  useEffect(() => {
    if (!dirty && !busy) return;
    return subscribeBeforeUnload(() => stateRef.current.dirty || stateRef.current.busy);
  }, [busy, dirty]);
}
