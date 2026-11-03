import { apiRequest } from './client';
import type { Department } from './departments';
import type { EmploymentDraft } from './drafts';
import type { ProvisioningJob } from './provision';
import type { Person, PersonInput } from './people';

export type OnboardingView = {
  draft: EmploymentDraft;
  person: Person;
  department?: Department;
  missing_fields: Record<string, string>;
};

export type OnboardingDetails = { person_id?: string; person: PersonInput; department_id: string };

export type CompleteResult = {
  ok: boolean;
  error?: string;
  draft: EmploymentDraft;
  jobs: ProvisioningJob[];
  groups_applied: string[] | null;
  account_created: boolean;
  password_setup_email_sent: boolean;
  password_setup_error?: string;
  temporary_password?: string;
};

export const startOnboarding = (body: OnboardingDetails) =>
  apiRequest<OnboardingView>('/api/onboardings', { method: 'POST', body });

export const getOnboarding = (id: string) => apiRequest<OnboardingView>(`/api/onboardings/${id}`);

export const updateOnboardingDetails = (id: string, body: OnboardingDetails) =>
  apiRequest<OnboardingView>(`/api/onboardings/${id}/details`, { method: 'PUT', body });

export const completeOnboarding = (id: string) =>
  apiRequest<CompleteResult>(`/api/onboardings/${id}/complete`, { method: 'POST' });
