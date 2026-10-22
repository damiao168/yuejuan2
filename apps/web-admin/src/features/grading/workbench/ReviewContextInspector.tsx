import { useState } from "react";
import type { ReviewTaskContext } from "../../../api/review";
import { AIContextPanel } from "./AIContextPanel";
import { SubjectToolPanel } from "./SubjectToolPanel";
import { isChoiceReview } from "./reviewContext";

export function ReviewContextInspector({ context }: Readonly<{ context: ReviewTaskContext }>) {
  const [secondOpinionVisible, setSecondOpinionVisible] = useState(false);
  const reveal = () => setSecondOpinionVisible(true);
  return (
    <>
      {!isChoiceReview(context) ? <details className="grading-reference-details"><summary>学科工具与任务信息</summary><SubjectToolPanel context={context} secondOpinionVisible={secondOpinionVisible} onRevealSecondOpinion={reveal} /></details> : null}
      <AIContextPanel context={context} visible={secondOpinionVisible} onReveal={reveal} />
    </>
  );
}
