import type { QuestionBankReferenceMaterials } from "@edugrade/sdk";
import { getQuestionBankReferenceImage } from "../../api/questionBank";
import { getReferenceCropImage } from "../../api/referenceMaterials";
import { ProtectedReferenceImage, referenceRoleLabel } from "../paper-rubric/ReferenceMaterialEditor";

export function QuestionBankReferenceImages({ materials, versionId }: Readonly<{
  materials?: QuestionBankReferenceMaterials;
  versionId?: string;
}>) {
  if (!materials) return null;
  return <section aria-label="题目冻结参考图">
    <h3>冻结参考图</h3>
    <p>{versionId ? "本版本引用这些图片；原材料授权撤回或图片不可用时，将停止使用。" : "导入后本草稿将引用这些图片；原材料授权撤回后，将停止使用。"}</p>
    {materials.items.map((item) => <figure key={item.id}>
      <figcaption>{referenceRoleLabel(item.role)} {item.ordinal}</figcaption>
      <ProtectedReferenceImage identity={`${versionId ?? materials.source_exam_id}:${materials.manifest_id}:${item.id}`} alt={`${referenceRoleLabel(item.role)} ${item.ordinal}`}
        load={() => versionId ? getQuestionBankReferenceImage(versionId, item.id) : getReferenceCropImage(materials.source_exam_id, materials.source_question_id, item.crop_ref)} />
    </figure>)}
  </section>;
}
