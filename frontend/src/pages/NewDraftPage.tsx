import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { useNavigate, useSearch } from '@tanstack/react-router';

import { listPeople } from '@/api/people';
import { createDraft } from '@/api/drafts';

export function NewDraftPage() {
  const navigate = useNavigate();
  const search = useSearch({ from: '/app/drafts/new' });
  const peopleQuery = useQuery({ queryKey: ['people'], queryFn: () => listPeople() });

  const [personID, setPersonID] = useState(search.person ?? '');
  const selected = peopleQuery.data?.items.find((p) => p.id === personID);
  const [title, setTitle] = useState('');
  const [notes, setNotes] = useState('');

  const createMutation = useMutation({
    mutationFn: () =>
      createDraft({
        person_id: personID,
        title,
        notes: notes || undefined,
      }),
    onSuccess: (d) => {
      void navigate({ to: '/drafts/$id', params: { id: d.id } });
    },
  });

  return (
    <div className="p-6 max-w-xl space-y-6">
      <h1 className="text-2xl font-bold">New employment draft</h1>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          createMutation.mutate();
        }}
        className="space-y-3"
      >
        <label className="block text-sm">
          <span className="block mb-1 font-medium">Person</span>
          <select
            required
            value={personID}
            onChange={(e) => setPersonID(e.target.value)}
            className="w-full rounded-md border border-input bg-background px-3 py-2"
          >
            <option value="">— choose —</option>
            {(peopleQuery.data?.items ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.display_name} ({p.legal_name}){p.email_missing ? ' — email missing' : ` — ${p.email}`}
              </option>
            ))}
          </select>
          {selected?.email_missing && (
            <span className="mt-1 block text-xs text-amber-700">
              This person has no email yet; provisioning will be refused until one is set on their profile.
            </span>
          )}
        </label>

        <label className="block text-sm">
          <span className="block mb-1 font-medium">Title</span>
          <input
            required
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="e.g. Lead Programme Organiser"
            className="w-full rounded-md border border-input bg-background px-3 py-2"
          />
        </label>

        <label className="block text-sm">
          <span className="block mb-1 font-medium">Notes</span>
          <textarea
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            rows={4}
            className="w-full rounded-md border border-input bg-background px-3 py-2"
          />
        </label>

        <button
          type="submit"
          disabled={createMutation.isPending || !personID || !title.trim()}
          className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60"
        >
          {createMutation.isPending ? 'Creating…' : 'Create draft'}
        </button>
        {createMutation.error && (
          <p className="text-sm text-destructive">{(createMutation.error as Error).message}</p>
        )}
      </form>
    </div>
  );
}
