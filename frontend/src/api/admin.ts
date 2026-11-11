// Admin-side APIs: access groups, audit log, review queue, imports, Google Workspace.
import { apiBlob, apiRequest, type Page } from './client';
import type { RoleRef } from './departments';
import type { OnboardingView, CompleteResult } from './onboarding';
import type { Address, EmergencyContact, PersonInput } from './people';

// ---- Access groups ---------------------------------------------------------

export type AccessBundle = {
  id: string;
  name: string;
  description: string;
  roles: RoleRef[];
  members: number;
  created_at: string;
  updated_at: string;
};

export type AccessBundleInput = { name: string; description: string; roles: { role_name: string }[] };

export const listBundles = () => apiRequest<{ items: AccessBundle[] }>('/api/access-bundles');
export const createBundle = (body: AccessBundleInput) => apiRequest<AccessBundle>('/api/access-bundles', { method: 'POST', body });
export const updateBundle = (id: string, body: AccessBundleInput) =>
  apiRequest<AccessBundle>(`/api/access-bundles/${id}`, { method: 'PUT', body });
export const deleteBundle = (id: string) => apiRequest<void>(`/api/access-bundles/${id}`, { method: 'DELETE' });

// ---- Audit log -------------------------------------------------------------

export type AuditEvent = {
  id: string;
  actor_name: string;
  actor_issuer: string;
  actor_subject: string;
  action: string;
  entity_type: string;
  entity_id: string;
  metadata?: Record<string, unknown>;
  occurred_at: string;
  purge_after?: string;
};

export const listAudit = (filter: { entity_id?: string; entity_type?: string; actor?: string; action?: string; since?: string; before?: string; limit?: number; offset?: number }) =>
  apiRequest<Page<AuditEvent>>('/api/audit', { query: filter });

// ---- Review queue ----------------------------------------------------------

export type ReviewItem = {
  id: string;
  source: 'idp_scan' | 'self_service' | 'import';
  status: 'pending' | 'ready' | 'accepted' | 'merged' | 'rejected';
  idp_user_id?: string;
  email: string;
  email_verified: boolean;
  first_name: string;
  last_name: string;
  nickname: string;
  phone: string;
  data: {
    date_of_birth?: string;
    address: Address;
    emergency_contact: EmergencyContact;
    social_handles?: Record<string, string>;
    org_email?: string;
    notes?: string;
    roles?: string[];
    warnings?: string[];
  };
  department_id?: string;
  bundle_ids: string[];
  /** An HR person with the same email: verified_email (verified by the IdP) or possible. Never applied automatically. */
  match_person_id?: string;
  match_kind?: '' | 'verified_email' | 'possible';
  import_batch_id?: string;
  person_id?: string;
  draft_id?: string;
  decided_by?: string;
  decided_at?: string;
  decision_note?: string;
  created_at: string;
};

export const listReview = (filter: { status?: string; source?: string; q?: string; import_batch_id?: string; limit?: number; offset?: number }) =>
  apiRequest<Page<ReviewItem>>('/api/review', { query: filter });
export const scanReview = () => apiRequest<{ seen: number; created: number; linked: number }>('/api/review/scan', { method: 'POST' });
export const updateReview = (id: string, body: { department_id?: string | null; bundle_ids: string[]; notes?: string }) =>
  apiRequest<ReviewItem>(`/api/review/${id}`, { method: 'PUT', body });
export const acceptReview = (id: string, body: { person: PersonInput; department_id: string; bundle_ids?: string[]; complete?: boolean; note?: string }) =>
  apiRequest<{ item: ReviewItem; onboarding: OnboardingView; complete?: CompleteResult }>(`/api/review/${id}/accept`, { method: 'POST', body });
export const mergeReview = (id: string, personId: string, note?: string) =>
  apiRequest<ReviewItem>(`/api/review/${id}/merge`, { method: 'POST', body: { person_id: personId, note } });
export const rejectReview = (id: string, reason: string) =>
  apiRequest<{ item: ReviewItem; warnings: string[] }>(`/api/review/${id}/reject`, { method: 'POST', body: { reason } });

// ---- Imports ---------------------------------------------------------------

export type ImportOptions = {
  sheet?: string;
  header_row?: number;
  /** HR field -> column header. */
  columns: Record<string, string>;
  department_values?: Record<string, string>;
  default_department_id?: string;
};

export type ImportInspect = {
  format: 'xlsx' | 'csv';
  encoding?: string;
  delimiter?: string;
  sheets: string[];
  sheet: string;
  header_row: number;
  headers: string[];
  sample: string[][];
  rows: number;
  suggested_columns: Record<string, string>;
  fields: string[];
};

export type ImportPreviewRow = {
  row: number;
  status: 'new' | 'update' | 'unchanged' | 'error' | 'duplicate';
  email: string;
  name: string;
  problems: string[];
  warnings: string[];
  match_person_id?: string;
  review_item_id?: string;
  department_id?: string;
};

export type ImportBatch = {
  id: string;
  file_name: string;
  created_by: string;
  created_at: string;
  rows_total: number;
  rows_created: number;
  rows_updated: number;
  rows_failed: number;
  undone_at?: string;
  open: number;
};

function form(file: File, opts?: ImportOptions) {
  const fd = new FormData();
  fd.append('file', file);
  if (opts) fd.append('options', JSON.stringify(opts));
  return fd;
}

export const inspectImport = (file: File, opts?: Partial<ImportOptions>) =>
  apiRequest<ImportInspect>('/api/imports/inspect', { method: 'POST', body: form(file, opts as ImportOptions) });
export const previewImport = (file: File, opts: ImportOptions) =>
  apiRequest<{ rows: ImportPreviewRow[]; summary: Record<string, number> }>('/api/imports/preview', { method: 'POST', body: form(file, opts) });
export const previewReport = (file: File, opts: ImportOptions) =>
  apiBlob('/api/imports/preview', { method: 'POST', body: form(file, opts), query: { report: 'xlsx' } });
export const commitImport = (file: File, opts: ImportOptions) =>
  apiRequest<ImportBatch>('/api/imports', { method: 'POST', body: form(file, opts) });
export const listImports = () => apiRequest<{ items: ImportBatch[] }>('/api/imports');
export const batchReport = (id: string) => apiBlob(`/api/imports/${id}/errors`);
export const undoImport = (id: string) => apiRequest<ImportBatch>(`/api/imports/${id}`, { method: 'DELETE' });

export type ImportTemplate = { id: string; name: string; mapping: ImportOptions; updated_at: string };
export const listImportTemplates = () => apiRequest<{ items: ImportTemplate[] }>('/api/import-templates');
export const saveImportTemplate = (name: string, mapping: ImportOptions) =>
  apiRequest<ImportTemplate>('/api/import-templates', { method: 'POST', body: { name, mapping } });

// ---- Google Workspace ------------------------------------------------------

export type WorkspaceAccount = {
  id: string;
  person_id?: string;
  primary_email: string;
  display_name: string;
  status: 'active' | 'suspended' | 'deleted';
  suspended_at?: string;
  delete_after?: string;
  reminded_at?: string;
  deleted_at?: string;
  last_error?: string;
};

export const listWorkspaceAccounts = (status?: string) =>
  apiRequest<{ items: WorkspaceAccount[] }>('/api/workspace/accounts', { query: { status } });
export const deleteWorkspaceAccount = (id: string) =>
  apiRequest<WorkspaceAccount>(`/api/workspace/accounts/${id}/delete`, { method: 'POST' });

// ---- Self-service ("waiting for a coordinator") -----------------------------

export type SelfServiceInput = {
  first_name: string;
  last_name: string;
  nickname: string;
  phone: string;
  date_of_birth: string;
  address: Address;
  emergency_contact: EmergencyContact;
  department_id?: string;
};

export type SelfServiceView = {
  status: ReviewItem['status'];
  email: string;
  item: SelfServiceInput;
  missing_fields: Record<string, string>;
};

export const getSelfService = () => apiRequest<SelfServiceView>('/api/self-service/registration');
export const submitSelfService = (body: SelfServiceInput) =>
  apiRequest<SelfServiceView>('/api/self-service/registration', { method: 'PUT', body });
export const listSelfServiceDepartments = () =>
  apiRequest<{ items: { id: string; name: string; description: string }[] }>('/api/self-service/departments');
