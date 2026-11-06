import { apiRequest } from './client';

export type Settings = {
  organization_name: string;
  org_email_domain: string;
  username_template: string;
  org_email_template: string;
  /** ISO 3166-1 alpha-2; preselected for phone numbers and addresses. */
  default_country: string;
  /** Photon-compatible address search; empty turns lookups off. */
  address_lookup_url: string;
  /** "den" while HR is paired with Den; Den then owns the shared fields. */
  managed_by?: 'den';
};

export const getSettings = () => apiRequest<Settings>('/api/settings');

export const updateSettings = (body: Settings) => apiRequest<Settings>('/api/settings', { method: 'PUT', body });
