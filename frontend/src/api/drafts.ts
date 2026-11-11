import { apiRequest, type Page } from './client';

export type DraftState =
  | 'draft'
  | 'provisioning'
  | 'active'
  | 'rejected';

export type RolePlan = {
  id: string;
  draft_id: string;
  role_name: string;
  group_name?: string;
  scope?: string;
  created_at: string;
  /** The access group the role came from (empty: picked individually). */
  bundle_id?: string;
  bundle_name?: string;
  /** The module no longer offers this role; remove it by hand. */
  retired?: boolean;
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
  bundle_ids?: string[];
  /** Filled in lists. */
  person_name?: string;
  department_name?: string;
};

export const listDrafts = (filter?: { state?: DraftState; q?: string; limit?: number; offset?: number }) =>
  apiRequest<Page<EmploymentDraft>>('/api/drafts', { query: filter });

export const getDraft = (id: string) =>
  apiRequest<EmploymentDraft>(`/api/drafts/${id}`);

export const createDraft = (body: {
  person_id: string;
  title: string;
  start_date?: string;
  end_date?: string;
  notes?: string;
}) => apiRequest<EmploymentDraft>('/api/drafts', { method: 'POST', body });

/** Individually picked roles plus access groups; groups always come from the catalogs. */
export const assignRoles = (
  id: string,
  body: { roles: Array<{ role_name: string; scope?: string }>; bundle_ids?: string[] }
) => apiRequest<{ roles: RolePlan[] }>(`/api/drafts/${id}/roles`, { method: 'PUT', body });
