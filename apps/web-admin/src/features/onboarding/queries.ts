import { useQuery } from "@tanstack/react-query";
import { getOnboardingReadiness } from "../../api/onboarding";

export const onboardingQueryKey = ["onboarding", "readiness"] as const;

export function useOnboardingReadiness(enabled = true) {
  return useQuery({
    queryKey: onboardingQueryKey,
    queryFn: getOnboardingReadiness,
    enabled,
    staleTime: 15_000,
    retry: 1
  });
}
