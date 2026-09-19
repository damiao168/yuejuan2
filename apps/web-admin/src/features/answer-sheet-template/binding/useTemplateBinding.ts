import { useState } from "react";
import { App } from "antd";
import { bindExamTemplate, unbindExamTemplate, type AnswerSheetTemplate, type ExamTemplateBinding } from "../../../api/configuration";
import { getUserErrorMessage } from "../../../api/client";

export function useTemplateBinding(examId: string) {
  const { message, modal } = App.useApp();
  const [examBinding, setExamBinding] = useState<ExamTemplateBinding | null>(null);
  const [bindingBusy, setBindingBusy] = useState(false);

  const bindTemplateForExam = (template?: AnswerSheetTemplate) => {
    if (!template || template.status !== "locked") return;
    modal.confirm({
      title: "将模板用于本场考试",
      content: `后续无条码答卷将优先使用 v${template.version_no}，并在每页处理前进行版式一致性检查。`,
      okText: "确认使用",
      cancelText: "取消",
      onOk: async () => {
        setBindingBusy(true);
        try {
          const response = await bindExamTemplate(examId, template.id, examBinding?.revision ?? 0);
          setExamBinding(response.binding);
          message.success("已将该模板锁定为本场考试模板");
        } catch (error) {
          message.error(getUserErrorMessage(error, "考试模板绑定失败，请刷新后重试"));
        } finally {
          setBindingBusy(false);
        }
      }
    });
  };

  const releaseExamBinding = () => {
    if (!examBinding) return;
    modal.confirm({
      title: "解除本场考试模板",
      content: "解除后，无条码答卷需要重新进行模板判断。已经完成的页面仍保留实际使用的模板版本和校验码。",
      okText: "确认解除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        setBindingBusy(true);
        try {
          await unbindExamTemplate(examId, examBinding.revision, "管理员解除本场考试模板绑定");
          setExamBinding(null);
          message.success("已解除本场考试模板");
        } catch (error) {
          message.error(getUserErrorMessage(error, "解除失败，请刷新后重试"));
        } finally {
          setBindingBusy(false);
        }
      }
    });
  };

  return { examBinding, setExamBinding, bindingBusy, bindTemplateForExam, releaseExamBinding };
}
