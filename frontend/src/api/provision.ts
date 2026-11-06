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
