import { useState } from "react";
import type { ReviewTaskContext } from "../../../api/review";
import { AIContextPanel } from "./AIContextPanel";

export function ReviewContextInspector({ context }: Readonly<{ context: ReviewTaskContext }>) {
  const [secondOpinionVisible, setSecondOpinionVisible] = useState(false);
  const reveal = () => setSecondOpinionVisible(true);
  return (
    <AIContextPanel context={context} visible={secondOpinionVisible} onReveal={reveal} />
  );
}
