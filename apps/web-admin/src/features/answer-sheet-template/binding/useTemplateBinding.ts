import { useCallback, useEffect, useRef, useState } from "react";
import { App } from "antd";
import { bindExamTemplate, unbindExamTemplate, type AnswerSheetTemplate, type ExamTemplateBindingState } from "../../../api/configuration";
import { getUserErrorMessage } from "../../../api/client";

export function useTemplateBinding(examId: string, onChanged?: () => void, canEdit = true) {
  const { message, modal } = App.useApp();
  const [loadedBinding, setLoadedBinding] = useState<{ examId: string; state: ExamTemplateBindingState } | null>(null);
  const bindingRef = useRef<typeof loadedBinding>(null);
  const bindingState = loadedBinding?.examId === examId ? loadedBinding.state : null;
  const examBinding = bindingState?.binding ?? null;
  const [bindingBusy, setBindingBusy] = useState(false);
  const mounted = useRef(true);
  const scope = useRef({ examId, canEdit, version: 0 });
  const changed = useRef(onChanged);
  changed.current = onChanged;
  if (scope.current.examId !== examId || scope.current.canEdit !== canEdit) {
    scope.current = { examId, canEdit, version: scope.current.version + 1 };
  }
  const version = scope.current.version;
  const pending = useRef<{ version: number } | null>(null);
  const isCurrent = () => mounted.current && scope.current.version === version && scope.current.examId === examId && scope.current.canEdit;
  // 旧确认窗口不能在换科、权限恢复或卸载后重新提交；ref 同步拦截同一帧的双击。
  const begin = () => {
    if (!isCurrent() || pending.current?.version === version) return null;
    const operation = { version };
    pending.current = operation; setBindingBusy(true);
    return operation;
  };
  const finish = (operation: { version: number }) => {
    if (pending.current !== operation) return;
    pending.current = null;
    if (isCurrent()) setBindingBusy(false);
  };
  const setBindingState = useCallback((state: ExamTemplateBindingState) => {
    if (scope.current.examId !== examId || !mounted.current) return;
    if (!Number.isSafeInteger(state.revision) || state.revision < 0 ||
      (state.binding && (state.binding.exam_id !== examId || state.binding.revision !== state.revision))) {
      throw new Error("考试模板状态不完整，请重新读取后重试");
    }
    // 绑定和空绑定共用递增游标；迟到的读取不能覆盖本页刚完成的绑定或解绑。
    if (bindingRef.current?.examId === examId && bindingRef.current.state.revision > state.revision) return;
    const loaded = { examId, state };
    bindingRef.current = loaded;
    setLoadedBinding(loaded);
  }, [examId]);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  useEffect(() => {
    if (scope.current.examId !== examId) return;
    // 展示状态按考试隔离；切换时只接续属于当前考试版本的忙碌状态。
    setBindingBusy(pending.current?.version === scope.current.version);
  }, [examId]);
  useEffect(() => { if (!canEdit) setBindingBusy(false); }, [canEdit]);

  const activateTemplate = async (template: AnswerSheetTemplate, expectedRevision?: number) => {
    if (template.status !== "locked") throw new Error("请先确认答题区域");
    if (template.exam_id !== examId) throw new Error("模板不属于当前考试，请重新核对");
    const operation = begin();
    if (!operation) throw new Error("当前考试已切换、不可修改，或模板绑定正在保存");
    try {
      const current = bindingRef.current?.examId === examId ? bindingRef.current.state : null;
      if (!current || (expectedRevision !== undefined && expectedRevision !== current.revision)) {
        throw new Error("考试模板状态已变化或尚未读取，请刷新后重试");
      }
      const response = await bindExamTemplate(examId, template.id, current.revision);
      if (!isCurrent()) throw new Error("考试或权限已变化，请重新读取当前考试的模板状态");
      setBindingState(response);
      return response.binding;
    } finally {
      finish(operation);
    }
  };

  const bindTemplateForExam = (template?: AnswerSheetTemplate) => {
    if (!isCurrent() || !bindingState || pending.current?.version === version || template?.status !== "locked" || template.exam_id !== examId) return;
    const expectedRevision = bindingState.revision;
    modal.confirm({
      title: "将模板用于本场考试",
      content: `后续无条码答卷将优先使用 v${template.version_no}，并在每页处理前进行版式一致性检查。`,
      okText: "确认使用",
      cancelText: "取消",
      onOk: async () => {
        if (!isCurrent() || pending.current?.version === version) return;
        try {
          // 绑定有独立版本号；模板已锁定并不代表考试绑定仍是打开确认框时的版本。
          await activateTemplate(template, expectedRevision);
          if (!isCurrent()) return;
          // 草稿启用由生命周期统一通知；此入口仅重试已锁定模板的绑定，成功后也要更新准备检查。
          changed.current?.();
          message.success("已将该模板锁定为本场考试模板");
        } catch (error) {
          if (isCurrent()) message.error(getUserErrorMessage(error, "考试模板绑定失败，请刷新后重试"));
        }
      }
    });
  };

  const releaseExamBinding = () => {
    if (!isCurrent() || pending.current?.version === version || !examBinding) return;
    modal.confirm({
      title: "解除本场考试模板",
      content: "解除后，无条码答卷需要重新进行模板判断。已经完成的页面仍保留实际使用的模板版本和校验码。",
      okText: "确认解除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        const operation = begin();
        if (!operation) return;
        try {
          const current = bindingRef.current?.examId === examId ? bindingRef.current.state : null;
          if (!current?.binding || current.revision !== examBinding.revision) {
            throw new Error("考试模板状态已变化，请刷新后重试");
          }
          const response = await unbindExamTemplate(examId, current.revision, "管理员解除本场考试模板绑定");
          if (!isCurrent()) return;
          setBindingState({ binding: null, revision: response.revision });
          changed.current?.();
          message.success("已解除本场考试模板");
        } catch (error) {
          if (isCurrent()) message.error(getUserErrorMessage(error, "解除失败，请刷新后重试"));
        } finally {
          finish(operation);
        }
      }
    });
  };

  return { examBinding, bindingRevision: bindingState?.revision, setBindingState, bindingBusy, activateTemplate, bindTemplateForExam, releaseExamBinding };
}
