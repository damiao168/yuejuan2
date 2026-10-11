import { createAssessmentApi } from "@edugrade/sdk";
import type {
  ExamQuestionAssessmentSnapshot,
  ExamRiskTier,
  PutQuestionAssessmentProfileRequest,
  QuestionAssessmentProfile,
  QuestionScoringPolicy,
  ScoringMode
} from "@edugrade/sdk";
import { apiClient } from "./client";

const assessmentApi = createAssessmentApi(apiClient);

export type {
  EducationStage,
  EvidenceType,
  QuestionArchetype,
  QuestionArchetypeCode,
  SubjectCode,
  SubjectProfile
} from "@edugrade/sdk";
export type AssessmentScoringMode = ScoringMode;
export type AssessmentRiskTier = ExamRiskTier;
export type AssessmentProfileInput = PutQuestionAssessmentProfileRequest;
export type AssessmentScoringPolicy = QuestionScoringPolicy;
export type AssessmentProfile = QuestionAssessmentProfile;
export type AssessmentSnapshot = ExamQuestionAssessmentSnapshot;

export const listSubjectProfiles = assessmentApi.listSubjectProfiles;
export const listQuestionArchetypes = assessmentApi.listQuestionArchetypes;
export const updateQuestionAssessmentProfile = assessmentApi.updateQuestionAssessmentProfile;
export const getQuestionAssessmentProfile = assessmentApi.getQuestionAssessmentProfile;
export const getQuestionAssessmentSnapshot = assessmentApi.getQuestionAssessmentSnapshot;
