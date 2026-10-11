import { useState } from "react";
import type { ReviewTaskContext } from "../../../api/review";
import { AIContextPanel } from "./AIContextPanel";
import { SubjectToolPanel } from "./SubjectToolPanel";

export function ReviewContextInspector({ context }: Readonly<{ context: ReviewTaskContext }>) {
  const [secondOpinionVisible, setSecondOpinionVisible] = useState(false);
  const reveal = () => setSecondOpinionVisible(true);
  return (
    <>
      <SubjectToolPanel context={context} secondOpinionVisible={secondOpinionVisible} onRevealSecondOpinion={reveal} />
      <AIContextPanel context={context} visible={secondOpinionVisible} onReveal={reveal} />
    </>
  );
}
