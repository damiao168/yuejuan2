import { ApiClientError } from "../../../api/client";
import {
  updateAnswerSheetTemplate,
  type AnswerSheetTemplate,
  type TemplatePayload
} from "../../../api/configuration";

export type TemplateSaveResult =
  | { status: "saved"; template: AnswerSheetTemplate }
  | { status: "revision_conflict"; error: unknown }
  | { status: "failed"; error: unknown };

export interface TemplateSaveCommand {
  templateId: string;
  payload: TemplatePayload;
  expectedRevision: number;
}

export async function saveTemplateDraft(
  command: TemplateSaveCommand,
  save: typeof updateAnswerSheetTemplate = updateAnswerSheetTemplate
): Promise<TemplateSaveResult> {
  try {
    const response = await save(command.templateId, command.payload, command.expectedRevision);
    return { status: "saved", template: response.template };
  } catch (error) {
    return error instanceof ApiClientError && error.status === 409
      ? { status: "revision_conflict", error }
      : { status: "failed", error };
  }
}
