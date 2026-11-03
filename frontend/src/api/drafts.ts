import { apiRequest } from './client';

export type DraftState =
  | 'draft'
  | 'provisioning'
  | 'pending_approval'
  | 'active'
  | 'rejected';

export type RolePlan = {
  id: string;
  draft_id: string;
  role_name: string;
  group_name?: string;
  scope?: string;
  created_at: string;
};

export type EmploymentDraft = {
  id: string;
  person_id: string;
  department_id?: string;
  title: string;
  start_date?: string | null;
  end_date?: string | null;
  state: DraftState;
  notes?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  roles?: RolePlan[];
};

export const listDrafts = (state?: DraftState) =>
  apiRequest<{ items: EmploymentDraft[] }>('/api/drafts', { query: state ? { state } : undefined });

export const getDraft = (id: string) =>
  apiRequest<EmploymentDraft>(`/api/drafts/${id}`);

export const createDraft = (body: {
  person_id: string;
  title: string;
  start_date?: string;
  end_date?: string;
  notes?: string;
}) => apiRequest<EmploymentDraft>('/api/drafts', { method: 'POST', body });

export const assignRoles = (
  id: string,
  body: { roles: Array<{ role_name: string; group_name?: string; scope?: string }> }
) => apiRequest<{ roles: RolePlan[] }>(`/api/drafts/${id}/roles`, { method: 'PUT', body });
