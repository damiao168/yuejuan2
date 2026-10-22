// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import type { WorkbenchContext } from "./gradingWorkbench.types";
import { restoreWorkbenchDraft } from "./workbenchDraftLifecycle";

function context(shared: boolean): WorkbenchContext {
  return { task: { id: "task12", assigned_to: "teacher", question_id: "q12" },
    reviewContext: { claim: { can_renew: false }, draft: null }, ocrText: "", ocrResults: [], aiGrades: [],
    gradingGroupImageUrl: shared ? "/shared12-14" : undefined } as unknown as WorkbenchContext;
}
const options = { taskId: "task12", currentUserId: "teacher", canViewOriginalImage: false, isConflicted: false };

describe("共框是查看配置，独立评分草稿不合并", () => {
  it("共区模板首次打开默认整区，逐题模板仍默认单题", () => {
    expect(restoreWorkbenchDraft(context(true), options).restored.viewer.mode).toBe("group");
    expect(restoreWorkbenchDraft(context(false), options).restored.viewer.mode).toBe("segment");
  });

  it("已保存的单题选择与本题得分优先，组图片失效时回退单题", () => {
    const next = context(true);
    next.reviewContext.draft = { viewer_state: { mode: "segment" }, score: 3, rubric_selections: [], comments: "第12题", revision: 1 } as unknown as NonNullable<WorkbenchContext["reviewContext"]["draft"]>;
    const restored = restoreWorkbenchDraft(next, options);
    expect(restored.restored.viewer.mode).toBe("segment");
    expect(restored.restored.draft.score).toBe(3);
    expect(restored.restored.draft.comments).toBe("第12题");
    next.gradingGroupImageUrl = undefined;
    next.reviewContext.draft.viewer_state.mode = "group";
    expect(restoreWorkbenchDraft(next, options).restored.viewer.mode).toBe("segment");
  });
});
