export type QuestionSaveArea = "assessment" | "references";

// prepare 只校验并冻结输入，返回的动作才写入；整题保存必须先完成所有区域校验。
export interface QuestionSaveParticipant {
  owner: string;
  dirty: boolean;
  busy: boolean;
  prepare: () => Promise<() => Promise<void>>;
  discard: () => void;
}

export type RegisterQuestionSaveParticipant = (area: QuestionSaveArea, participant: QuestionSaveParticipant | null) => void;
