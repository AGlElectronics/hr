import { apiRequest } from './client';

export type RoleRef = { role_name: string; group_name?: string; scope?: string };

export type Department = {
  id: string;
  slug: string;
  name: string;
  description: string;
  group_name: string;
  default_roles: RoleRef[];
  idp_synced_at?: string;
  idp_sync_error?: string;
  created_at: string;
  updated_at: string;
};

export type DepartmentInput = {
  name: string;
  slug?: string;
  description?: string;
  default_roles: RoleRef[];
};

export const listDepartments = () => apiRequest<{ items: Department[] }>('/api/departments');

export const createDepartment = (body: DepartmentInput) =>
  apiRequest<Department>('/api/departments', { method: 'POST', body });

export const updateDepartment = (id: string, body: DepartmentInput) =>
  apiRequest<Department>(`/api/departments/${id}`, { method: 'PUT', body });
