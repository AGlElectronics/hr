import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';

import { listDrafts } from '@/api/drafts';
import { formatDateTime } from '@/lib/utils';

export function ApprovalsPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['drafts', 'pending_approval'],
    queryFn: () => listDrafts('pending_approval'),
  });

  return (
    <div className="p-6 space-y-4">
      <h1 className="text-2xl font-bold">Pending approvals</h1>
      {isLoading && <p className="text-muted-foreground">Loading…</p>}
      {error && <p className="text-destructive">Error: {(error as Error).message}</p>}
      <ul className="rounded-lg border border-border bg-card divide-y divide-border">
        {(data?.items ?? []).map((d) => (
          <li key={d.id} className="px-4 py-3 flex items-center justify-between">
            <div>
              <Link to="/drafts/$id" params={{ id: d.id }} className="font-medium hover:underline">
                {d.title}
              </Link>
              <div className="text-xs text-muted-foreground">
                Updated {formatDateTime(d.updated_at)}
              </div>
            </div>
            <Link
              to="/drafts/$id"
              params={{ id: d.id }}
              className="text-sm text-primary hover:underline"
            >
              Review
            </Link>
          </li>
        ))}
        {(data?.items ?? []).length === 0 && !isLoading && (
          <li className="px-4 py-6 text-sm text-muted-foreground">Nothing waiting.</li>
        )}
      </ul>
    </div>
  );
}
