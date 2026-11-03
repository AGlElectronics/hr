import { apiRequest } from './client';
import type { Department } from './departments';

export type AttentionKind = 'missing_email' | 'incomplete_profile' | 'failed_provisioning' | 'stalled_onboarding';

export type AttentionItem = {
  kind: AttentionKind;
  person_id: string;
  display_name: string;
  draft_id?: string;
  detail: string;
};

export type DepartmentSummary = Department & { active_members: number; in_onboarding: number };

export type Dashboard = {
  status_counts: Record<string, number>;
  attention: AttentionItem[];
  departments: DepartmentSummary[];
};

export const getDashboard = () => apiRequest<Dashboard>('/api/dashboard');
