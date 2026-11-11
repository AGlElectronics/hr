import { apiRequest } from './client';

export type RoleRef = { role_name: string; group_name?: string; scope?: string };

export type LeadRef = { person_id: string; display_name: string };

export type Department = {
  id: string;
  slug: string;
  name: string;
  description: string;
  group_name: string;
  /** IdP group of the department's leads ("dept-<slug>-lead"). */
  lead_group: string;
  leads: LeadRef[];
  default_roles: RoleRef[];
  /** Access groups every new member gets. */
  default_bundles: string[];
  idp_synced_at?: string;
  idp_sync_error?: string;
  member_count: number;
  /** Archived departments take no new onboardings; members keep their access. */
  archived_at?: string;
  created_at: string;
  updated_at: string;
};

export type DepartmentInput = {
  name: string;
  slug?: string;
  description?: string;
  default_roles: RoleRef[];
  default_bundles?: string[];
};

/** Departments people can be onboarded into (not archived). */
export const listDepartments = () => apiRequest<{ items: Department[] }>('/api/departments');

/** Every department, archived ones included. */
export const listAllDepartments = () => apiRequest<{ items: Department[] }>('/api/departments', { query: { archived: true } });

export const createDepartment = (body: DepartmentInput) =>
  apiRequest<Department>('/api/departments', { method: 'POST', body });

export const updateDepartment = (id: string, body: DepartmentInput) =>
  apiRequest<Department>(`/api/departments/${id}`, { method: 'PUT', body });

export const setDepartmentLeads = (id: string, personIds: string[]) =>
  apiRequest<{ department: Department; warnings: string[] }>(`/api/departments/${id}/leads`, {
    method: 'PUT',
    body: { person_ids: personIds },
  });

export const archiveDepartment = (id: string, archived: boolean) =>
  apiRequest<Department>(`/api/departments/${id}/${archived ? 'archive' : 'restore'}`, { method: 'POST' });
