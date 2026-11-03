import { apiRequest } from './client';
import type { EmploymentDraft } from './drafts';

export type Approval = {
  id: string;
  draft_id: string;
  decision: 'approved' | 'rejected';
  reason?: string;
  approved_by: string;
  approved_at: string;
};

export const decide = (
  draftID: string,
  body: { decision: 'approved' | 'rejected'; reason?: string }
) =>
  apiRequest<{ draft: EmploymentDraft; approval: Approval }>(
    `/api/drafts/${draftID}/approvals`,
    { method: 'POST', body }
  );

export const listApprovals = (draftID: string) =>
  apiRequest<{ items: Approval[] }>(`/api/drafts/${draftID}/approvals`);
