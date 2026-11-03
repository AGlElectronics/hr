import { apiRequest } from './client';

export type Settings = {
  organization_name: string;
  org_email_domain: string;
  username_template: string;
  org_email_template: string;
};

export const getSettings = () => apiRequest<Settings>('/api/settings');

export const updateSettings = (body: Settings) => apiRequest<Settings>('/api/settings', { method: 'PUT', body });
