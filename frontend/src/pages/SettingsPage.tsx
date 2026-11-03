import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { ApiError } from '@/api/client';
import { getSettings, updateSettings } from '@/api/settings';

export function SettingsPage() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: getSettings });
  const [domain, setDomain] = useState('');
  useEffect(() => {
    if (settings.data) setDomain(settings.data.org_email_domain);
  }, [settings.data]);

  const save = useMutation({
    mutationFn: () => updateSettings({ org_email_domain: domain }),
    onSuccess: (s) => {
      qc.setQueryData(['settings'], s);
      setDomain(s.org_email_domain);
    },
  });
  const fieldError =
    save.error instanceof ApiError ? (save.error.details as Record<string, string> | undefined)?.org_email_domain : undefined;

  return (
    <div className="p-6 max-w-2xl space-y-6">
      <div>
        <h1 className="text-2xl font-bold">HR settings</h1>
        <p className="text-sm text-muted-foreground">Defaults used across HR.</p>
      </div>

      <form className="space-y-3 rounded-lg border border-border bg-card p-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}>
        <label className="block text-sm">
          <span className="mb-1 block font-medium">Organization email domain</span>
          <div className="flex items-stretch overflow-hidden rounded-md border border-input bg-background">
            <span className="flex items-center border-r border-input bg-muted px-3 text-muted-foreground">name@</span>
            <input className="min-w-0 flex-1 bg-transparent px-3 py-2 outline-none" value={domain}
              onChange={(e) => setDomain(e.target.value)} placeholder="your-org.example" />
          </div>
          <span className={`mt-1 block text-xs ${fieldError ? 'text-destructive' : 'text-muted-foreground'}`}>
            {fieldError ?? 'In profiles, HR only types the part before the @ and this domain is added. Leave empty to type full addresses.'}
          </span>
        </label>
        {save.error && !fieldError && <p className="text-sm text-destructive">{(save.error as Error).message}</p>}
        <div className="flex items-center gap-3">
          <button type="submit" disabled={save.isPending || domain === (settings.data?.org_email_domain ?? '')}
            className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60">
            {save.isPending ? 'Saving…' : 'Save'}
          </button>
          {save.isSuccess && <span className="text-sm text-muted-foreground">Saved.</span>}
        </div>
      </form>
    </div>
  );
}
