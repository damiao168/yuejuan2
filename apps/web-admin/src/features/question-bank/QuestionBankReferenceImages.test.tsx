// @vitest-environment jsdom
import type { QuestionBankReferenceMaterials } from "@edugrade/sdk";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../api/client";
import { QuestionBankReferenceImages } from "./QuestionBankReferenceImages";

const materials = {
  source_exam_id: "source/exam", source_question_id: "source/question", manifest_id: "frozen-manifest", mode: "reference_images", revision: 2, content_hash: "a".repeat(64), image_count: 1,
  items: [{ id: "item/one", crop_ref: "crop/one", role: "question_diagram", ordinal: 1, required: true, sha256: "b".repeat(64), width: 3, height: 3, size_bytes: 80, pixel_bbox: { x: 0, y: 0, width: 3, height: 3 }, source: { page_ref: "page", name: "synthetic", kind: "import_image", page_no: 1, sha256: "b".repeat(64), width: 3, height: 3, source_sha256: "b".repeat(64), source_version: "generation" } }],
} satisfies QuestionBankReferenceMaterials;

describe("题库冻结参考图", () => {
  beforeEach(() => {
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:synthetic-reference"), revokeObjectURL: vi.fn() }));
  });
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it("保存版本只请求准确授权的版本图片路由", async () => {
    const request = vi.spyOn(apiClient, "requestBlob").mockResolvedValue({ blob: new Blob(["synthetic"]), contentType: "image/png", filename: undefined, watermark: undefined });
    render(<QuestionBankReferenceImages materials={materials} versionId="version/one" />);
    await screen.findByRole("img", { name: "题目图 1" });
    expect(request).toHaveBeenCalledWith("/api/v1/question-bank/versions/version%2Fone/reference-materials/item%2Fone/image");
  });

  it("预览按原题精确读裁图，撤权失败明确显示不可用", async () => {
    const request = vi.spyOn(apiClient, "requestBlob").mockRejectedValue(new Error("source authorization withdrawn"));
    render(<QuestionBankReferenceImages materials={materials} />);
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("图片暂不可用"));
    expect(request).toHaveBeenCalledWith("/api/v1/exams/source%2Fexam/questions/source%2Fquestion/reference-material-crops/crop%2Fone/image");
  });
});
