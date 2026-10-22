import { useEffect, useRef, useState } from "react";
import { App } from "antd";
import { deleteAnswerSheetSource } from "../../../api/answerSheetSources";
import { getUserErrorMessage } from "../../../api/client";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import type { PaperVersion } from "../../../api/papers";

export function useAnswerSheetSourceDelete(input: {
  examId: string; canManage: boolean; identityKey?: string; selectedPaper?: PaperVersion;
  templates?: readonly AnswerSheetTemplate[]; busy?: boolean;
  confirmDiscard?: (action: () => void) => void;
  onDeleted: (paper: PaperVersion) => Promise<void> | void;
  onBusyChange?: (busy: boolean) => void;
}) {
  const { modal, message } = App.useApp();
  const current = useRef(input);
  current.current = input;
  const mounted = useRef(true);
  const generation = useRef(0);
  const inFlight = useRef(false);
  const confirmation = useRef<ReturnType<typeof modal.confirm> | undefined>(undefined);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string>();
  const [deletedPaperId, setDeletedPaperId] = useState("");
  const { examId, canManage, identityKey = "", selectedPaper } = input;
  // biome-ignore lint/correctness/useExhaustiveDependencies: 身份、权限或来源变化必须销毁旧确认并隔离旧删除响应。
  useEffect(() => {
    mounted.current = true;
    generation.current++;
    inFlight.current = false;
    confirmation.current?.destroy(); confirmation.current = undefined;
    setDeleting(false); setDeleteError(undefined); setDeletedPaperId("");
    current.current.onBusyChange?.(false);
    return () => { mounted.current = false; generation.current++; confirmation.current?.destroy(); confirmation.current = undefined; };
  }, [examId, canManage, identityKey, selectedPaper?.id]);

  const protectedTemplate = input.templates?.some(template => template.exam_paper_id === selectedPaper?.id && template.status !== "draft");
  const deleteDisabledReason = !canManage ? "当前考试不可修改答题卡文件"
    : !selectedPaper ? "请先选择答题卡文件"
    : deletedPaperId === selectedPaper.id ? "此来源已删除，请刷新文件列表"
    : selectedPaper.purpose !== "answer_sheet" ? "历史试卷来源不能在答题卡入口删除"
    : protectedTemplate ? "此答题卡已有锁定或历史模板，不能删除"
    : input.busy || deleting ? "请等待当前操作完成" : undefined;

  const removeCurrent = () => {
    if (!mounted.current || deleteDisabledReason || inFlight.current || confirmation.current) return;
    const scopeGeneration = generation.current;
    const paper = selectedPaper!;
    const isCurrent = () => mounted.current && generation.current === scopeGeneration
      && current.current.examId === examId && current.current.identityKey === input.identityKey
      && current.current.selectedPaper?.id === paper.id && current.current.canManage;
    const confirm = () => {
      if (!isCurrent() || current.current.busy || confirmation.current) return;
      const draftCount = current.current.templates?.filter(template => template.exam_paper_id === paper.id && template.status === "draft").length ?? 0;
      confirmation.current = modal.confirm({
        title: "删除当前答题卡文件？",
        content: `将删除“${paper.file.original_name}”的本场来源登记${draftCount ? `及 ${draftCount} 个关联模板草稿` : ""}。已收录、锁定或被正式使用的来源由系统阻止删除。`,
        okText: "删除答题卡", okButtonProps: { danger: true }, cancelText: "取消",
        onCancel: () => { confirmation.current = undefined; },
        onOk: async () => {
          // 危险确认可能在换考试后才触发；再次验证作用域，不能删除旧账户或旧页面的来源。
          if (!isCurrent() || current.current.busy) { confirmation.current = undefined; return; }
          if (inFlight.current) return;
          inFlight.current = true; setDeleting(true); setDeleteError(undefined); current.current.onBusyChange?.(true);
          let deleted = false;
          try {
            const result = await deleteAnswerSheetSource(examId, paper.id);
            if (!isCurrent()) return;
            if (result.deleted !== true || result.paper_id !== paper.id) throw new Error("删除结果与当前答题卡不一致，请刷新页面核对");
            deleted = true;
            setDeletedPaperId(paper.id);
            confirmation.current = undefined;
            // 服务端成功后才清理选择和重载；失败时保留当前图片、草稿和选择。
            await current.current.onDeleted(paper);
            if (mounted.current && current.current.examId === examId && current.current.identityKey === input.identityKey)
              message.success("答题卡文件及关联草稿已删除");
          } catch (error) {
            if (isCurrent()) {
              const notice = deleted ? "答题卡已删除，但列表刷新失败，请刷新页面核对。"
                : getUserErrorMessage(error, "答题卡删除失败，请稍后重试；当前文件和草稿已保留。");
              setDeleteError(notice); message.error(notice);
            }
            // 请求失败保留确认框供重试，不能把后端引用冲突当作本地删除成功。
            if (!deleted && isCurrent()) throw error;
          } finally {
            if (generation.current === scopeGeneration) {
              inFlight.current = false; setDeleting(false); current.current.onBusyChange?.(false);
            }
          }
        }
      });
    };
    if (input.confirmDiscard) input.confirmDiscard(confirm); else confirm();
  };
  return { deleting, deleteError, canDelete: !deleteDisabledReason, deleteDisabledReason, removeCurrent };
}
