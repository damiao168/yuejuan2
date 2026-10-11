import { createContext, useContext } from "react";

export const StudentSessionContext = createContext<{
  draftOwner: string;
  submitting: boolean;
  // 页面重载会卸载申请；真实会话提供同步标记，测试或独立表单可省略。
  isSubmitting?: () => boolean;
  setSubmitting: (value: boolean) => void;
  reauthenticate: () => void;
}>({
  draftOwner: "",
  submitting: false,
  setSubmitting: (_value: boolean) => {},
  reauthenticate: () => {}
});

export function useStudentSession() { return useContext(StudentSessionContext); }
