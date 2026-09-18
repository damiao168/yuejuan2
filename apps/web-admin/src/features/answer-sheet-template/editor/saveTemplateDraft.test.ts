import { describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../api/client";
import type { AnswerSheetTemplate, TemplatePayload } from "../../../api/configuration";
import { saveTemplateDraft } from "./saveTemplateDraft";

const payload: TemplatePayload = {
  exam_paper_id: "paper-1",
  name: "template",
  page_count: 1,
  layout: { pages: [] }
};

describe("answer sheet template save controller", () => {
  it("saves with the current revision and returns the authoritative revision", async () => {
    const template = { id: "template-1", revision: 8 } as AnswerSheetTemplate;
    const save = vi.fn().mockResolvedValue({ template });

    const result = await saveTemplateDraft({ templateId: "template-1", payload, expectedRevision: 7 }, save);

    expect(save).toHaveBeenCalledWith("template-1", payload, 7);
    expect(result).toEqual({ status: "saved", template });
  });

  it("classifies a revision conflict so the page can reload server state", async () => {
    const conflict = new ApiClientError(409, "template_revision_conflict", "stale revision");
    const result = await saveTemplateDraft(
      { templateId: "template-1", payload, expectedRevision: 7 },
      vi.fn().mockRejectedValue(conflict)
    );

    expect(result).toEqual({ status: "revision_conflict", error: conflict });
  });

  it("keeps non-conflict API failures distinct", async () => {
    const failure = new Error("offline");
    const result = await saveTemplateDraft(
      { templateId: "template-1", payload, expectedRevision: 7 },
      vi.fn().mockRejectedValue(failure)
    );
    expect(result).toEqual({ status: "failed", error: failure });
  });
});
