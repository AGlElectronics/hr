import { apiRequest } from './client';
import type { Department } from './departments';

export type AttentionKind = 'incomplete_profile' | 'failed_provisioning' | 'stalled_onboarding';

export type AttentionItem = {
  kind: AttentionKind;
  person_id: string;
  display_name: string;
  legal_name: string;
  email: string;
  draft_id?: string;
  /** Plain-language summary. */
  detail: string;
  /** Missing field labels (incomplete_profile). */
  fields?: string[];
  /** Raw error, for the Details expander. */
  technical?: string;
};

export type DepartmentSummary = Department & { active_members: number; in_onboarding: number };

export type Dashboard = {
  status_counts: Record<string, number>;
  attention: AttentionItem[];
  departments: DepartmentSummary[];
};

export const getDashboard = () => apiRequest<Dashboard>('/api/dashboard');
