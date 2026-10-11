import { apiClient } from "./client";
import { EduGradeApi } from "@edugrade/sdk";
export type { WorkflowActor } from "@edugrade/sdk";
const workflowApi = new EduGradeApi(apiClient);
export async function listReviewActors(examId: string) { return (await workflowApi.listExamReviewActors({path:{examId}})).actors; }
export async function listProcessingActors(examId: string) { return (await workflowApi.listExamProcessingActors({path:{examId}})).actors; }
