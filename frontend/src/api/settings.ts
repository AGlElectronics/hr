import { apiRequest } from './client';

export type IdentitySettings = {
  /** own, brokered or existing; brokered/existing show the SSO warning when offboarding. */
  staff_mode: '' | 'own' | 'brokered' | 'existing';
  sso_name: string;
  sso_url: string;
  /** The identity provider's admin console ("Open Keycloak"). */
  admin_console_url: string;
  /** Values after applying Den's identity settings (read-only). */
  effective?: Omit<IdentitySettings, 'effective'>;
};

export type RetentionSettings = {
  enabled: boolean;
  applicant_days: number;
  paused_days: number;
  offboarded_audit_days: number;
  warning_days: number;
  /** "den" when Den's module settings set retention. */
  managed_by?: 'den';
};

export type WorkspaceSettings = {
  enabled: boolean;
  domain: string;
  admin_email: string;
  org_unit: string;
  delete_after_days: number;
  reminder_days: number;
  /** False until GOOGLE_WORKSPACE_CREDENTIALS_FILE is set (read-only). */
  available: boolean;
};

export type Settings = {
  organization_name: string;
  org_email_domain: string;
  username_template: string;
  org_email_template: string;
  /** ISO 3166-1 alpha-2; preselected for phone numbers and addresses. */
  default_country: string;
  /** Photon-compatible address search; empty turns lookups off. */
  address_lookup_url: string;
  /** Comma-separated; empty: the members of the HR admin groups. */
  admin_notification_emails: string;
  identity: IdentitySettings;
  retention: RetentionSettings;
  workspace: WorkspaceSettings;
  /** "den" while HR is paired with Den; Den then owns the shared fields. */
  managed_by?: 'den';
};

export const getSettings = () => apiRequest<Settings>('/api/settings');

/** Sends only what changes; the server keeps the rest. */
export const updateSettings = (body: Partial<Settings>) => apiRequest<Settings>('/api/settings', { method: 'PUT', body });

export type RetentionCandidate = {
  person_id: string;
  display_name: string;
  status: string;
  due_at: string;
  warned_at?: string;
};

export const getRetentionCandidates = () =>
  apiRequest<{ items: RetentionCandidate[]; retention: RetentionSettings }>('/api/retention/candidates');
