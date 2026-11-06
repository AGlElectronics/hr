import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Trash2 } from 'lucide-react';

import { getAccessCatalog, roleRef } from '@/api/catalog';
import {
  createRoleRequirement,
  deleteRoleRequirement,
  getQualificationCatalog,
  listRoleRequirements,
} from '@/api/roleGate';
import { useRoleLabels } from '@/lib/roles';

const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2';

/** HR settings: roles that are only granted once a Learn qualification is held. */
export function TrainingRequirements() {
  const qc = useQueryClient();
  const reqs = useQuery({ queryKey: ['role-requirements'], queryFn: listRoleRequirements });
  const access = useQuery({ queryKey: ['access-catalog'], queryFn: getAccessCatalog, staleTime: 60_000 });
  const quals = useQuery({ queryKey: ['qualification-catalog'], queryFn: getQualificationCatalog, staleTime: 60_000 });
  const roles = useRoleLabels();
  const [role, setRole] = useState('');
  const [qual, setQual] = useState('');
  const [revoke, setRevoke] = useState(false);

  const add = useMutation({
    mutationFn: () => createRoleRequirement({ role_key: role, qualification_key: qual, revoke_on_expiry: revoke }),
    onSuccess: () => {
      setRole('');
      setQual('');
      setRevoke(false);
      void qc.invalidateQueries({ queryKey: ['role-requirements'] });
    },
  });
  const remove = useMutation({
    mutationFn: deleteRoleRequirement,
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['role-requirements'] }),
  });

  const roleModules = (access.data?.modules ?? []).filter((m) => m.status === 'ok' && m.roles.some((r) => r.assignable && r.group));
  const qualModules = quals.data?.modules ?? [];
  const failedQuals = qualModules.filter((m) => m.status === 'error');
  const items = reqs.data?.items ?? [];

  return (
    <section className="space-y-4 rounded-lg border border-border bg-card p-4">
      <div>
        <h2 className="font-semibold">Training requirements</h2>
        <p className="text-sm text-muted-foreground">
          A role with a requirement is held back until the person has the qualification in Learn. HR checks every minute
          and adds the role as soon as the training is done.
        </p>
      </div>
      {reqs.data && !reqs.data.active && (
        <p className="rounded-md border border-warning/40 bg-warning-soft px-3 py-2 text-sm text-warning-foreground">
          HR is not connected to Learn (LEARN_HOLDINGS_URL / LEARN_SERVICE_TOKEN), so these requirements are not enforced
          and roles are granted straight away.
        </p>
      )}
      {failedQuals.map((m) => (
        <p key={m.module} className="text-xs text-destructive">
          Could not load qualifications from {m.display_name}: {m.error}
        </p>
      ))}
      {qualModules.length === 0 && quals.isSuccess && (
        <p className="text-xs text-muted-foreground">No qualification sources are configured (QUALIFICATION_CATALOG_SOURCES).</p>
      )}

      <ul className="divide-y divide-border rounded-md border border-border">
        {items.map((r) => (
          <li key={r.id} className="flex items-center justify-between gap-3 px-3 py-2 text-sm">
            <div className="min-w-0">
              <div className="font-medium">{roles.text(r.role_key)}</div>
              <div className="text-xs text-muted-foreground">
                needs {r.qualification_name || r.qualification_key}
                {r.revoke_on_expiry ? ' · removed when it expires' : ''}
              </div>
            </div>
            <button type="button" title="Remove requirement" disabled={remove.isPending}
              onClick={() => remove.mutate(r.id)}
              className="shrink-0 rounded p-1 text-muted-foreground hover:text-destructive disabled:opacity-60">
              <Trash2 className="h-4 w-4" />
            </button>
          </li>
        ))}
        {items.length === 0 && <li className="px-3 py-3 text-sm text-muted-foreground">No requirements yet.</li>}
      </ul>

      <form className="grid gap-3 sm:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate();
        }}>
        <label className="block text-sm">
          <span className="mb-1 block font-medium">Role</span>
          <select className={inputCls} value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="">Pick a role…</option>
            {roleModules.map((m) => (
              <optgroup key={m.module} label={m.display_name}>
                {m.roles.filter((r) => r.assignable && r.group).map((r) => (
                  <option key={r.key} value={roleRef(m.module, r.key)}>{r.label}</option>
                ))}
              </optgroup>
            ))}
          </select>
        </label>
        <label className="block text-sm">
          <span className="mb-1 block font-medium">Qualification</span>
          <select className={inputCls} value={qual} onChange={(e) => setQual(e.target.value)}>
            <option value="">Pick a qualification…</option>
            {qualModules.filter((m) => m.status === 'ok').map((m) => (
              <optgroup key={m.module} label={m.display_name}>
                {m.qualifications.map((q) => (
                  <option key={q.key} value={q.key}>
                    {q.name}{q.valid_months ? ` (valid ${q.valid_months} months)` : ''}
                  </option>
                ))}
              </optgroup>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-2 text-sm sm:col-span-2">
          <input type="checkbox" checked={revoke} onChange={(e) => setRevoke(e.target.checked)} />
          Remove the role again when the qualification expires
        </label>
        <div className="flex items-center gap-3 sm:col-span-2">
          <button type="submit" disabled={!role || !qual || add.isPending}
            className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60">
            {add.isPending ? 'Adding…' : 'Add requirement'}
          </button>
          {add.error && <span className="text-sm text-destructive">{(add.error as Error).message}</span>}
          {remove.error && <span className="text-sm text-destructive">{(remove.error as Error).message}</span>}
        </div>
      </form>
    </section>
  );
}
