import { useEffect, useState } from "react";
import { QuestionBankInspector, QuestionBankList } from "./QuestionBankPanels";
import type { QuestionBankWorkspaceProps } from "./questionBankWorkspaceTypes";

export function QuestionBankWorkspace(props: QuestionBankWorkspaceProps) {
  const { item } = props;
  const [mobilePane, setMobilePane] = useState<"list" | "detail">("list");
  // 只随题目身份切换面板；同一题的后台刷新不应把用户从列表重新拉回详情。
  useEffect(() => {
    setMobilePane(item ? "detail" : "list");
  }, [item?.id]);
  return <div className={`question-bank-workspace${item ? " has-selection" : ""} is-${mobilePane}`}>
    <QuestionBankList {...props} onReselect={() => setMobilePane("detail")} />
    <QuestionBankInspector {...props} onBack={() => setMobilePane("list")} />
  </div>;
}

