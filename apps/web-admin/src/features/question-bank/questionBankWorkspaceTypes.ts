import type { FormInstance } from "antd";
import type { UseQueryResult } from "@tanstack/react-query";
import type {
  QuestionBank, QuestionBankItem, QuestionBankItemPage, QuestionBankMetadataSchemaResponse,
  QuestionBankReviewsResponse, QuestionBankVersion, QuestionBankVersionPage,
  QuestionBankVersionResponse, UpdateQuestionBankVersionRequest,
  QuestionBankScoring,
} from "@edugrade/sdk";
import type { SessionUser } from "../../auth/session";
import type { QuestionBankMember } from "./questionBankMembers";
import type { ScoringDraftSnapshot } from "./questionBankDraftMerge";

type QueryView<T> = Pick<UseQueryResult<T, Error>, "data" | "isError" | "isLoading" | "error" | "refetch">;
export type QuestionBankWorkspaceProps = Readonly<{
  bank?: QuestionBank;
  kind: "question" | "rubric_template";
  onKindChange: (kind: "question" | "rubric_template") => void;
  canManage: boolean;
  openSchema: () => void;
  openAcl: () => void;
  items: QueryView<QuestionBankItemPage>;
  selectItem: (item: QuestionBankItem, versionId?: string) => void;
  offset: number;
  setOffset: (offset: number) => void;
  item: QuestionBankItem | null;
  selected?: QuestionBankVersion;
  versions: QueryView<QuestionBankVersionPage>;
  version: QueryView<QuestionBankVersionResponse>;
  versionOffset: number;
  setVersionId: (id: string, offset?: number) => void;
  saving: boolean;
  loadLatest: () => void;
  canEdit: boolean;
  derive: () => void;
  canRetire: boolean;
  retireItem: () => void;
  boundSchema: QueryView<QuestionBankMetadataSchemaResponse>;
  editor: FormInstance;
  saveVersion: (values: UpdateQuestionBankVersionRequest) => void;
  contentDirty: boolean;
  setContentDirty: (dirty: boolean) => void;
  scoringDirty: boolean;
  conflict: boolean;
  editorVersion: QuestionBankVersion | null;
  scoringSaved: (version: QuestionBankVersion) => void;
  setScoringDirty: (dirty: boolean) => void;
  onScoringDraftSnapshot?: (draft: unknown) => void;
  scoringResetToken?: number;
  restoredScoringDraft?: ScoringDraftSnapshot;
  saveAllDraft?: () => void;
  saveCombinedScoring?: (scoring: QuestionBankScoring) => Promise<QuestionBankVersion>;
  registerScoringSubmit?: (submit: (() => void) | null) => void;
  user: SessionUser;
  comment: string;
  setComment: (comment: string) => void;
  transition: (decision: "submit-review" | "approve" | "return-to-draft" | "publish", assignees?: { reviewer_id: string; publisher_id: string }) => void;
  handoff?: (action: "assign" | "review" | "publish", assignees?: { reviewer_id: string; publisher_id: string }) => void;
  reviews: QueryView<QuestionBankReviewsResponse>;
  members?: QueryView<{members:QuestionBankMember[]}>;
}>;
