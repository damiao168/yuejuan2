import type { CreateQuestionBankItemRequest } from "@edugrade/sdk";

export type SearchFilters = {
	q: string;
	subject_code?: CreateQuestionBankItemRequest["metadata"]["subject_code"];
	knowledge_point?: string;
	question_type?: string;
	workflow_status?: "draft" | "reviewing" | "approved" | "published";
	difficulty_band?: string;
	intended_use?: string;
	mode: "default" | "published" | "my_drafts" | "all";
};

export type ACLFormValues = {
	bindings?: Array<{
		user_id: string;
		preset: "Viewer" | "Author" | "Reviewer" | "Publisher" | "Manager";
	}>;
	groups?: Array<{
		id?: string;
		name: string;
		preset: "Viewer" | "Author" | "Reviewer" | "Publisher" | "Manager";
		members?: string;
	}>;
};

export type MetadataSchemaFormValues = {
	fields?: Array<Record<string, unknown> & { options_text?: string }>;
	taxonomies?: Array<{ id: string; label: string; terms_text?: string }>;
};

export const emptySearch: SearchFilters = { q: "", mode: "default" };

export const workflowLabels = {
	draft: "草稿",
	reviewing: "审核中",
	approved: "已批准",
	published: "已发布",
};
