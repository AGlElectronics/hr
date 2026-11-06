import { apiRequest } from './client';

export type Qualification = {
  key: string;
  name: string;
  description?: string;
  valid_months?: number;
};

export type QualificationModule = {
  module: string;
  display_name: string;
  qualifications: Qualification[];
  source: string;
  status: 'ok' | 'error';
  error?: string;
};

export type RoleRequirement = {
  id: string;
  role_key: string;
  qualification_key: string;
  qualification_name?: string;
  revoke_on_expiry: boolean;
  created_at: string;
};

export type GrantStatus = 'pending' | 'granted' | 'overridden' | 'revoked';

export type PendingGrant = {
  id: string;
  person_id: string;
  person_name: string;
  draft_id?: string;
  role_key: string;
  group_name: string;
  status: GrantStatus;
  reason?: string;
  created_at: string;
  resolved_at?: string;
  waiting_for: { key: string; name: string }[];
};

export const getQualificationCatalog = () =>
  apiRequest<{ modules: QualificationModule[] }>('/api/qualification-catalog');

/** `active` is false when HR cannot ask Learn, so requirements are not enforced. */
export const listRoleRequirements = () =>
  apiRequest<{ active: boolean; items: RoleRequirement[] }>('/api/role-requirements');

export const createRoleRequirement = (body: { role_key: string; qualification_key: string; revoke_on_expiry: boolean }) =>
  apiRequest<RoleRequirement>('/api/role-requirements', { method: 'POST', body });

export const deleteRoleRequirement = (id: string) =>
  apiRequest<void>(`/api/role-requirements/${id}`, { method: 'DELETE' });

export const listPendingGrants = (status?: GrantStatus) =>
  apiRequest<{ active: boolean; items: PendingGrant[] }>('/api/pending-grants', { query: { status } });

export const overrideGrant = (id: string, reason: string) =>
  apiRequest<PendingGrant>(`/api/pending-grants/${id}/override`, { method: 'POST', body: { reason } });

/** "First aid, Forklift" */
export const waitingText = (g: PendingGrant) => g.waiting_for.map((q) => q.name || q.key).join(', ');
