import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { GraduationCap } from 'lucide-react';

import { listPendingGrants, overrideGrant, waitingText, type PendingGrant } from '@/api/roleGate';
import { useRoleLabels } from '@/lib/roles';

/** People whose roles are held back until they finish training, with an HR override. */
export function WaitingForTraining() {
  const qc = useQueryClient();
  const pending = useQuery({ queryKey: ['pending-grants', 'pending'], queryFn: () => listPendingGrants('pending') });
  const revoked = useQuery({ queryKey: ['pending-grants', 'revoked'], queryFn: () => listPendingGrants('revoked') });
  const roles = useRoleLabels();

  const override = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => overrideGrant(id, reason),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['pending-grants'] }),
  });
  const ask = (g: PendingGrant) => {
    const reason = window.prompt(`Grant ${roles.text(g.role_key)} to ${g.person_name} without the training? Reason (saved in the audit log):`);
    if (reason?.trim()) override.mutate({ id: g.id, reason: reason.trim() });
  };

  const items = [...(pending.data?.items ?? []), ...(revoked.data?.items ?? [])];
  if (items.length === 0 && !pending.error) return null;

  return (
    <section className="rounded-lg border border-border bg-card">
      <header className="flex items-center gap-2 border-b border-border px-4 py-3">
        <GraduationCap className="h-4 w-4 text-muted-foreground" />
        <span className="font-semibold">Waiting for training</span>
        <span className="text-xs text-muted-foreground">Roles are added automatically once the training is done.</span>
      </header>
      {pending.error && <p className="px-4 py-3 text-sm text-destructive">{(pending.error as Error).message}</p>}
      {override.error && <p className="px-4 py-2 text-sm text-destructive">{(override.error as Error).message}</p>}
      <ul className="divide-y divide-border">
        {items.map((g) => (
          <li key={g.id} className="flex items-center justify-between gap-3 px-4 py-3 text-sm">
            <div className="min-w-0">
              <div className="font-medium">
                <Link to="/people/$id" params={{ id: g.person_id }} className="hover:underline">{g.person_name}</Link>
                <span className="font-normal text-muted-foreground"> · {roles.text(g.role_key)}</span>
              </div>
              <div className="truncate text-xs text-muted-foreground">
                {g.status === 'revoked' ? 'Expired, waiting for renewal' : 'Waiting for training'}: {waitingText(g) || '—'}
              </div>
            </div>
            <button type="button" onClick={() => ask(g)} disabled={override.isPending}
              className="shrink-0 rounded-pill border border-border px-3 py-1 text-xs font-medium hover:bg-muted disabled:opacity-60">
              Grant anyway
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
