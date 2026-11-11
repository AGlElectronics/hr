import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Layers, Pencil, Plus, ShieldAlert, Trash2 } from 'lucide-react';

import { createBundle, deleteBundle, listBundles, updateBundle, type AccessBundle } from '@/api/admin';
import { ApiError } from '@/api/client';
import { getAccessCatalog, isAdminRole, roleRef } from '@/api/catalog';
import { canGlobal, getMe } from '@/api/me';
import { noAutofill } from '@/lib/noAutofill';

const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2';

/**
 * Access groups bundle module roles under one name ("Standard volunteer").
 * Onboarding assigns them; editing one updates everyone who holds it in the
 * background.
 */
export function AccessGroupsPage() {
  const me = useQuery({ queryKey: ['me'], queryFn: getMe });
  const bundles = useQuery({ queryKey: ['access-bundles'], queryFn: listBundles });
  const catalog = useQuery({ queryKey: ['access-catalog'], queryFn: getAccessCatalog });
  const [editing, setEditing] = useState<AccessBundle | 'new' | null>(null);
  const mayManage = canGlobal(me.data, 'bundles.manage');
  const label = (ref: string) => {
    const [mod, key] = ref.split(':', 2);
    const m = catalog.data?.modules.find((x) => x.module === mod);
    const r = m?.roles.find((x) => x.key === key);
    return r ? `${m!.display_name} · ${r.label}` : ref;
  };

  return (
    <div className="p-6 max-w-3xl space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Access groups</h1>
          <p className="text-sm text-muted-foreground">
            Named sets of module roles, such as &ldquo;Standard volunteer&rdquo;. Onboarding assigns access groups; changing one
            updates everyone who has it.
          </p>
        </div>
        {mayManage && editing === null && (
          <button type="button" onClick={() => setEditing('new')}
            className="inline-flex shrink-0 items-center gap-1 rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
            <Plus className="h-4 w-4" /> New access group
          </button>
        )}
      </div>

      {editing === 'new' && <BundleForm onDone={() => setEditing(null)} />}
      {bundles.isLoading && <p className="text-muted-foreground">Loading…</p>}
      {bundles.error && <p className="text-destructive">{(bundles.error as Error).message}</p>}
      {bundles.data?.items.length === 0 && editing === null && (
        <p className="rounded-lg border border-dashed border-border p-6 text-sm text-muted-foreground">No access groups yet.</p>
      )}
      <div className="space-y-3">
        {bundles.data?.items.map((b) =>
          editing !== 'new' && editing?.id === b.id ? (
            <BundleForm key={b.id} bundle={b} onDone={() => setEditing(null)} />
          ) : (
            <BundleRow key={b.id} bundle={b} label={label} mayManage={mayManage} onEdit={() => setEditing(b)} />
          ),
        )}
      </div>
    </div>
  );
}

function BundleRow({ bundle, label, mayManage, onEdit }: {
  bundle: AccessBundle;
  label: (ref: string) => string;
  mayManage: boolean;
  onEdit: () => void;
}) {
  const qc = useQueryClient();
  const del = useMutation({
    mutationFn: () => deleteBundle(bundle.id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['access-bundles'] }),
  });
  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="flex items-center gap-2 font-semibold"><Layers className="h-4 w-4 text-primary" /> {bundle.name}</h2>
          {bundle.description && <p className="text-sm text-muted-foreground">{bundle.description}</p>}
          <p className="mt-1 text-xs text-muted-foreground">
            {bundle.members === 1 ? '1 membership' : `${bundle.members} memberships`}
          </p>
        </div>
        {mayManage && (
          <div className="flex shrink-0 gap-3 text-sm">
            <button type="button" onClick={onEdit} className="inline-flex items-center gap-1 text-primary hover:underline">
              <Pencil className="h-3.5 w-3.5" /> Edit
            </button>
            <button type="button" disabled={del.isPending}
              onClick={() => window.confirm(`Delete the access group “${bundle.name}”?`) && del.mutate()}
              className="inline-flex items-center gap-1 text-destructive hover:underline">
              <Trash2 className="h-3.5 w-3.5" /> Delete
            </button>
          </div>
        )}
      </div>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {bundle.roles.map((r) => (
          <span key={r.role_name} className="rounded-pill border border-border bg-muted px-2 py-0.5 text-xs">{label(r.role_name)}</span>
        ))}
      </div>
      {del.error && <p role="alert" className="mt-2 text-sm text-destructive">{(del.error as Error).message}</p>}
    </section>
  );
}

function BundleForm({ bundle, onDone }: { bundle?: AccessBundle; onDone: () => void }) {
  const qc = useQueryClient();
  const catalog = useQuery({ queryKey: ['access-catalog'], queryFn: getAccessCatalog });
  const me = useQuery({ queryKey: ['me'], queryFn: getMe });
  const mayAssignAdmin = canGlobal(me.data, 'access.assign_privileged');
  const [name, setName] = useState(bundle?.name ?? '');
  const [description, setDescription] = useState(bundle?.description ?? '');
  const [roles, setRoles] = useState<Set<string>>(() => new Set(bundle?.roles.map((r) => r.role_name) ?? []));
  const save = useMutation({
    mutationFn: () => {
      const body = { name, description, roles: [...roles].map((role_name) => ({ role_name })) };
      return bundle ? updateBundle(bundle.id, body) : createBundle(body);
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['access-bundles'] });
      onDone();
    },
  });
  const errs = save.error instanceof ApiError ? ((save.error.details as Record<string, string> | undefined) ?? {}) : {};
  const toggle = (ref: string) =>
    setRoles((prev) => {
      const next = new Set(prev);
      if (next.has(ref)) next.delete(ref);
      else next.add(ref);
      return next;
    });

  return (
    <form className="space-y-4 rounded-lg border border-primary/40 bg-card p-4" autoComplete="off"
      onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
      <h2 className="font-semibold">{bundle ? `Edit ${bundle.name}` : 'New access group'}</h2>
      <label className="block text-sm">
        <span className="mb-1 block font-medium">Name</span>
        <input {...noAutofill} required className={inputCls} value={name} onChange={(e) => setName(e.target.value)} placeholder="Standard volunteer" />
        {errs.name && <span className="mt-1 block text-xs text-destructive">{errs.name}</span>}
      </label>
      <label className="block text-sm">
        <span className="mb-1 block font-medium">Description</span>
        <input {...noAutofill} className={inputCls} value={description} onChange={(e) => setDescription(e.target.value)} />
      </label>
      <fieldset className="space-y-3 text-sm">
        <legend className="mb-1 font-medium">Roles</legend>
        {catalog.isLoading && <p className="text-muted-foreground">Loading roles…</p>}
        {catalog.data?.modules.filter((m) => m.status !== 'error').map((m) => (
          <div key={m.module}>
            <p className="text-xs font-semibold uppercase text-muted-foreground">{m.display_name}</p>
            <div className="mt-1 grid gap-1 sm:grid-cols-2">
              {m.roles.map((r) => {
                const ref = roleRef(m.module, r.key);
                const admin = isAdminRole(r);
                const locked = !r.assignable || (admin && !mayAssignAdmin && !roles.has(ref));
                return (
                  <label key={ref} className={`flex items-start gap-2 ${locked ? 'opacity-60' : ''}`}>
                    <input type="checkbox" checked={roles.has(ref)} disabled={locked} onChange={() => toggle(ref)} className="mt-1" />
                    <span>
                      {r.label}
                      {admin && <ShieldAlert className="ml-1 inline h-3 w-3 text-destructive" aria-label="admin role" />}
                      {r.description && <span className="block text-xs text-muted-foreground">{r.description}</span>}
                    </span>
                  </label>
                );
              })}
            </div>
          </div>
        ))}
        {errs.roles && <p className="text-xs text-destructive">{errs.roles}</p>}
      </fieldset>
      {bundle && bundle.members > 0 && (
        <p className="text-xs text-muted-foreground">
          Saving updates the access of everyone who has this group ({bundle.members}) in the background.
        </p>
      )}
      {save.error && Object.keys(errs).length === 0 && <p role="alert" className="text-sm text-destructive">{(save.error as Error).message}</p>}
      <div className="flex gap-2">
        <button type="submit" disabled={save.isPending || !name.trim() || roles.size === 0}
          className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60">
          {save.isPending ? 'Saving…' : 'Save'}
        </button>
        <button type="button" onClick={onDone} className="rounded-pill border border-border px-4 py-2 text-sm">Cancel</button>
      </div>
    </form>
  );
}
