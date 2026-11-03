import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';

import { AccessPicker } from '@/components/AccessPicker';
import { assignRoles, getDraft } from '@/api/drafts';
import {
  listProvisioningJobs,
  notifyUser,
  triggerProvision,
} from '@/api/provision';
import { decide } from '@/api/approvals';
import { formatDateTime } from '@/lib/utils';

export function DraftDetailPage() {
  // `strict: false` lets us read `id` without threading a route-id literal
  // through the component tree; the scaffold only exposes one `$id` route
  // so there is no ambiguity.
  const { id } = useParams({ strict: false }) as { id: string };
  const qc = useQueryClient();

  const draftQuery = useQuery({ queryKey: ['draft', id], queryFn: () => getDraft(id) });
  const jobsQuery = useQuery({
    queryKey: ['draft-jobs', id],
    queryFn: () => listProvisioningJobs(id),
  });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['draft', id] });
    qc.invalidateQueries({ queryKey: ['draft-jobs', id] });
    qc.invalidateQueries({ queryKey: ['drafts'] });
  };

  const provisionMutation = useMutation({
    mutationFn: () => triggerProvision(id),
    onSuccess: invalidate,
  });

  const approveMutation = useMutation({
    mutationFn: (decision: 'approved' | 'rejected') => decide(id, { decision }),
    onSuccess: invalidate,
  });

  const notifyMutation = useMutation({
    mutationFn: () => notifyUser(id),
  });

  const d = draftQuery.data;

  return (
    <div className="p-6 space-y-6 max-w-4xl">
      {draftQuery.isLoading && <p className="text-muted-foreground">Loading…</p>}
      {draftQuery.error && (
        <p className="text-destructive">Error: {(draftQuery.error as Error).message}</p>
      )}

      {d && (
        <>
          <header>
            <h1 className="text-2xl font-bold">{d.title}</h1>
            <p className="text-sm text-muted-foreground">
              State: <span className="font-semibold text-foreground">{d.state}</span> · updated{' '}
              {formatDateTime(d.updated_at)} · by {d.created_by}
            </p>
          </header>

          <AccessPicker
            plan={d.roles}
            editable={d.state === 'draft'}
            onSave={async (roles) => {
              await assignRoles(d.id, { roles });
              invalidate();
            }}
          />

          <section className="rounded-lg border border-border bg-card p-4 space-y-3">
            <h2 className="font-semibold">Lifecycle</h2>
            <div className="flex flex-wrap gap-2">
              {d.state === 'draft' && (
                <button
                  onClick={() => provisionMutation.mutate()}
                  disabled={provisionMutation.isPending}
                  className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60"
                >
                  {provisionMutation.isPending ? 'Provisioning…' : 'Trigger provision'}
                </button>
              )}
              {d.state === 'pending_approval' && (
                <>
                  <button
                    onClick={() => approveMutation.mutate('approved')}
                    disabled={approveMutation.isPending}
                    className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60"
                  >
                    Approve
                  </button>
                  <button
                    onClick={() => approveMutation.mutate('rejected')}
                    disabled={approveMutation.isPending}
                    className="rounded-pill border border-destructive px-4 py-2 text-sm font-semibold text-destructive disabled:opacity-60"
                  >
                    Reject
                  </button>
                </>
              )}
              {d.state === 'active' && (
                <button
                  onClick={() => notifyMutation.mutate()}
                  disabled={notifyMutation.isPending}
                  className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60"
                >
                  {notifyMutation.isPending ? 'Notifying…' : 'Notify user'}
                </button>
              )}
            </div>
            {notifyMutation.data && (
              <p className="text-sm text-success">Notified {notifyMutation.data.recipient}.</p>
            )}
          </section>

          <section className="rounded-lg border border-border bg-card p-4">
            <h2 className="font-semibold mb-2">Provisioning jobs</h2>
            {jobsQuery.isLoading && <p className="text-muted-foreground text-sm">Loading…</p>}
            <ul className="divide-y divide-border text-sm">
              {(jobsQuery.data?.items ?? []).map((j) => (
                <li key={j.id} className="py-2">
                  <span className="font-medium">{j.target}</span> · {j.status} ·{' '}
                  <span className="text-muted-foreground">{formatDateTime(j.started_at)}</span>
                  {j.external_id ? ` → ${j.external_id}` : ''}
                  {j.message?.startsWith('temporary_password=') ? (
                    <div className="mt-1 rounded-md bg-muted px-2 py-1 font-mono text-xs">
                      Keycloak temp password:{' '}
                      <span className="font-semibold text-foreground">
                        {j.message.slice('temporary_password='.length)}
                      </span>{' '}
                      <span className="text-muted-foreground">(must change on first login)</span>
                    </div>
                  ) : j.message ? (
                    <div className="mt-1 text-xs text-destructive">{j.message}</div>
                  ) : null}
                </li>
              ))}
              {(jobsQuery.data?.items ?? []).length === 0 && (
                <li className="py-2 text-muted-foreground">No jobs yet.</li>
              )}
            </ul>
          </section>
        </>
      )}
    </div>
  );
}
