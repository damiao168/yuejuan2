import { apiClient } from "./client";

export type OnboardingCheckState = "ready" | "action_required" | "warning" | "optional" | "unavailable";
export type OnboardingSeverity = "blocking" | "recommended" | "optional";

export interface OnboardingCheck {
  key: string;
  state: OnboardingCheckState;
  severity: OnboardingSeverity;
  title: string;
  description?: string;
  action_code?: string;
  action_path?: string;
  metadata?: Record<string, unknown>;
}

export interface OnboardingReadiness {
  scope: "platform" | "school";
  ready_for_use: boolean;
  completed_count: number;
  total_required: number;
  checks: OnboardingCheck[];
  next_action?: { code: string; path: string };
}

export function getOnboardingReadiness() {
  return apiClient.request<OnboardingReadiness>("/api/v1/onboarding/readiness");
}
