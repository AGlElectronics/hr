import { apiRequest } from './client';
import type { Department } from './departments';
import type { EmploymentDraft } from './drafts';
import type { ProvisioningJob } from './provision';
import type { Person, PersonInput } from './people';
import type { PendingGrant } from './roleGate';

/** The latest "Complete" job of an onboarding. */
export type ProvisioningStatus = {
  job_id: string;
  status: 'pending' | 'running' | 'succeeded' | 'failed';
  attempts: number;
  max_attempts: number;
  error?: string;
  result?: CompleteResult;
  queued_at: string;
};

export type OnboardingView = {
  draft: EmploymentDraft;
  person: Person;
  department?: Department;
  missing_fields: Record<string, string>;
  /** Roles of this onboarding still waiting for training. */
  pending_grants?: PendingGrant[];
  provisioning?: ProvisioningStatus;
};

export type OnboardingDetails = { person_id?: string; person: PersonInput; department_id: string };

export type CompleteResult = {
  ok: boolean;
  /** Complete answers right away with queued: true; the job reports the rest. */
  queued?: boolean;
  job_id?: string;
  error?: string;
  draft?: EmploymentDraft;
  jobs: ProvisioningJob[];
  groups_applied: string[] | null;
  account_created: boolean;
  password_setup_email_sent: boolean;
  password_setup_error?: string;
  /** No email by configuration (brokered accounts) or the account already existed. */
  password_setup_skipped?: boolean;
  /** Roles left out until the person finishes training. */
  pending_grants?: PendingGrant[];
  warnings?: string[];
};

export const startOnboarding = (body: OnboardingDetails) =>
  apiRequest<OnboardingView>('/api/onboardings', { method: 'POST', body });

export const getOnboarding = (id: string) => apiRequest<OnboardingView>(`/api/onboardings/${id}`);

export const updateOnboardingDetails = (id: string, body: OnboardingDetails) =>
  apiRequest<OnboardingView>(`/api/onboardings/${id}/details`, { method: 'PUT', body });

/** Queues the completion; poll getOnboarding until provisioning.status is succeeded or failed. */
export const completeOnboarding = (id: string) =>
  apiRequest<CompleteResult>(`/api/onboardings/${id}/complete`, { method: 'POST' });

/** Cancels an onboarding that is still a draft. */
export const cancelOnboarding = (id: string, reason: string) =>
  apiRequest<OnboardingView>(`/api/onboardings/${id}/cancel`, { method: 'POST', body: { reason } });
