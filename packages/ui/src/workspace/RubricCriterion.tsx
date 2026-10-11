import type { ReactNode } from "react";

/** 评分细则行，在阅卷界面中保持分值和必填提示一致。 */
export function RubricCriterion({
  label,
  score,
  required = false,
  leading,
  children
}: Readonly<{
  label: ReactNode;
  score: number;
  required?: boolean;
  /** 可选控件，显示在采分点标签前，例如复选框。 */
  leading?: ReactNode;
  /** 可选的尾部控件，例如实际得分输入框。 */
  children?: ReactNode;
}>) {
  return (
    <div className="eg-rubric-criterion">
      <div className="eg-rubric-criterion-main">{leading}<span>{label}</span>{children}</div>
      <div className="eg-rubric-criterion-meta"><b>{score} 分</b>{required ? <em>必选</em> : null}</div>
    </div>
  );
}
