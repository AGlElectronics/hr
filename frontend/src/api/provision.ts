import { apiRequest } from './client';
import type { EmploymentDraft } from './drafts';

export type ProvisioningJob = {
  id: string;
  draft_id: string;
  target: 'idp' | 'workspace';
  status: 'pending' | 'running' | 'succeeded' | 'failed';
  attempt: number;
  message?: string;
  external_id?: string;
  started_at: string;
  finished_at?: string | null;
};

export const triggerProvision = (draftID: string) =>
  apiRequest<{ draft: EmploymentDraft; jobs: ProvisioningJob[] }>(
    `/api/drafts/${draftID}/provision`,
    { method: 'POST' }
  );

export const listProvisioningJobs = (draftID: string) =>
  apiRequest<{ items: ProvisioningJob[] }>(`/api/drafts/${draftID}/provisioning-jobs`);

export const notifyUser = (draftID: string, body: { subject?: string; message?: string } = {}) =>
  apiRequest<{ channel: string; recipient: string; subject: string }>(
    `/api/drafts/${draftID}/notify`,
    { method: 'POST', body }
  );
