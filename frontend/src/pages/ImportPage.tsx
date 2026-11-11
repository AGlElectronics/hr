import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Download, FileSpreadsheet, Undo2, Upload } from 'lucide-react';

import {
  batchReport,
  commitImport,
  inspectImport,
  listImports,
  listImportTemplates,
  previewImport,
  previewReport,
  saveImportTemplate,
  undoImport,
  type ImportInspect,
  type ImportOptions,
} from '@/api/admin';
import { saveBlob } from '@/api/client';
import { listDepartments } from '@/api/departments';

const selectCls = 'w-full rounded-md border border-input bg-background px-2 py-1.5 text-sm';
const btn = 'inline-flex items-center gap-1 rounded-pill border border-border px-3 py-1.5 text-sm hover:bg-muted disabled:opacity-60';

/**
 * Imports volunteers from a spreadsheet (.xlsx or .csv) into the review
 * queue: map the columns, check a dry run, then import. A batch can be
 * undone while its rows are still waiting for review.
 */
export function ImportPage() {
  const qc = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [info, setInfo] = useState<ImportInspect | null>(null);
  const [opts, setOpts] = useState<ImportOptions>({ columns: {} });
  const [templateName, setTemplateName] = useState('');
  const depts = useQuery({ queryKey: ['departments'], queryFn: listDepartments });
  const templates = useQuery({ queryKey: ['import-templates'], queryFn: listImportTemplates });
  const batches = useQuery({ queryKey: ['imports'], queryFn: listImports });

  const inspect = useMutation({
    mutationFn: (args: { f: File; o?: Partial<ImportOptions> }) => inspectImport(args.f, args.o),
    onSuccess: (res) => {
      setInfo(res);
      setOpts((o) => ({ ...o, sheet: res.sheet, header_row: res.header_row, columns: { ...res.suggested_columns, ...o.columns } }));
    },
  });
  const preview = useMutation({ mutationFn: () => previewImport(file!, opts) });
  const commit = useMutation({
    mutationFn: () => commitImport(file!, opts),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['imports'] });
      void qc.invalidateQueries({ queryKey: ['review'] });
    },
  });
  const report = useMutation({ mutationFn: async () => saveBlob(await previewReport(file!, opts), 'import-check.xlsx') });
  const saveTemplate = useMutation({
    mutationFn: () => saveImportTemplate(templateName, opts),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['import-templates'] }),
  });

  const pick = (f: File | null) => {
    setFile(f);
    setInfo(null);
    preview.reset();
    commit.reset();
    if (f) inspect.mutate({ f });
  };
  const setColumn = (field: string, header: string) =>
    setOpts((o) => {
      const columns = { ...o.columns };
      if (header) columns[field] = header;
      else delete columns[field];
      return { ...o, columns };
    });
  const errors = preview.data?.summary.error ?? 0;

  return (
    <div className="p-6 max-w-4xl space-y-6">
      <div>
        <h1 className="text-2xl font-bold">Import volunteers</h1>
        <p className="text-sm text-muted-foreground">
          Upload a spreadsheet (.xlsx or .csv). Every row goes into the review queue; nobody gets an account until someone accepts them there.
        </p>
      </div>

      <section className="space-y-4 rounded-lg border border-border bg-card p-4">
        <label className="flex items-center gap-3 text-sm">
          <FileSpreadsheet className="h-5 w-5 text-primary" />
          <input type="file" accept=".xlsx,.csv,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
            onChange={(e) => pick(e.target.files?.[0] ?? null)} aria-label="Spreadsheet" />
        </label>
        {inspect.isPending && <p className="text-sm text-muted-foreground">Reading the file…</p>}
        {inspect.error && <p role="alert" className="text-sm text-destructive">{(inspect.error as Error).message}</p>}

        {info && file && (
          <>
            <p className="text-sm text-muted-foreground">
              {info.rows} rows · {info.format.toUpperCase()}
              {info.encoding ? ` · ${info.encoding}` : ''}
            </p>
            <div className="grid gap-3 sm:grid-cols-3">
              {info.sheets.length > 1 && (
                <label className="text-sm">
                  <span className="mb-1 block font-medium">Sheet</span>
                  <select className={selectCls} value={opts.sheet}
                    onChange={(e) => inspect.mutate({ f: file, o: { sheet: e.target.value } })}>
                    {info.sheets.map((s) => <option key={s}>{s}</option>)}
                  </select>
                </label>
              )}
              <label className="text-sm">
                <span className="mb-1 block font-medium">Header row</span>
                <input type="number" min={1} className={selectCls} value={opts.header_row ?? 1}
                  onChange={(e) => inspect.mutate({ f: file, o: { sheet: opts.sheet, header_row: Number(e.target.value) } })} />
              </label>
              <label className="text-sm">
                <span className="mb-1 block font-medium">Department for every row</span>
                <select className={selectCls} value={opts.default_department_id ?? ''}
                  onChange={(e) => setOpts((o) => ({ ...o, default_department_id: e.target.value || undefined }))}>
                  <option value="">From the sheet / none</option>
                  {depts.data?.items.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
                </select>
              </label>
              {(templates.data?.items.length ?? 0) > 0 && (
                <label className="text-sm">
                  <span className="mb-1 block font-medium">Saved mapping</span>
                  <select className={selectCls} defaultValue=""
                    onChange={(e) => {
                      const t = templates.data!.items.find((x) => x.id === e.target.value);
                      if (t) setOpts({ ...t.mapping, sheet: opts.sheet, header_row: t.mapping.header_row ?? opts.header_row });
                    }}>
                    <option value="">Choose…</option>
                    {templates.data!.items.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
                  </select>
                </label>
              )}
            </div>

            <table className="w-full text-sm">
              <thead className="text-left text-xs uppercase text-muted-foreground">
                <tr className="border-b border-border"><th className="py-1">HR field</th><th className="py-1">Column in the file</th></tr>
              </thead>
              <tbody>
                {info.fields.map((f) => (
                  <tr key={f}>
                    <td className="py-1 pr-3 font-mono text-xs">{f}</td>
                    <td className="py-1">
                      <select className={selectCls} value={opts.columns[f] ?? ''} onChange={(e) => setColumn(f, e.target.value)} aria-label={f}>
                        <option value="">(not in the file)</option>
                        {info.headers.map((h) => <option key={h} value={h}>{h}</option>)}
                      </select>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>

            <div className="flex flex-wrap items-center gap-2">
              <input className="rounded-md border border-input bg-background px-3 py-1.5 text-sm" placeholder="Name this mapping"
                value={templateName} onChange={(e) => setTemplateName(e.target.value)} />
              <button type="button" className={btn} disabled={!templateName.trim() || saveTemplate.isPending} onClick={() => saveTemplate.mutate()}>
                Save mapping
              </button>
              {saveTemplate.isSuccess && <span className="text-xs text-muted-foreground">Saved.</span>}
            </div>

            <div className="flex flex-wrap gap-2 border-t border-border pt-3">
              <button type="button" className={btn} disabled={preview.isPending} onClick={() => preview.mutate()}>
                {preview.isPending ? 'Checking…' : 'Check (dry run)'}
              </button>
              <button type="button" className={btn} disabled={!preview.data || report.isPending} onClick={() => report.mutate()}>
                <Download className="h-3.5 w-3.5" /> Download check as spreadsheet
              </button>
              <button type="button" disabled={!preview.data || commit.isPending || !!commit.data}
                onClick={() => commit.mutate()}
                className="inline-flex items-center gap-1 rounded-pill bg-primary px-4 py-1.5 text-sm font-semibold text-primary-foreground disabled:opacity-60">
                <Upload className="h-3.5 w-3.5" /> {errors > 0 ? `Import (skip ${errors} with errors)` : 'Import'}
              </button>
            </div>
            {[preview.error, commit.error, report.error, saveTemplate.error].filter(Boolean).map((e) => (
              <p key={(e as Error).message} role="alert" className="text-sm text-destructive">{(e as Error).message}</p>
            ))}
            {commit.data && (
              <p className="text-sm text-primary">
                Imported: {commit.data.rows_created} new, {commit.data.rows_updated} updated, {commit.data.rows_failed} skipped. Review them in the review queue.
              </p>
            )}
          </>
        )}
      </section>

      {preview.data && (
        <section className="rounded-lg border border-border bg-card">
          <header className="border-b border-border px-4 py-2 text-sm">
            {Object.entries(preview.data.summary).map(([k, v]) => `${v} ${k}`).join(' · ')}
          </header>
          <table className="w-full text-sm">
            <thead className="text-left text-xs uppercase text-muted-foreground">
              <tr className="border-b border-border">
                <th className="px-4 py-2">Row</th><th className="px-4 py-2">Name</th><th className="px-4 py-2">Email</th><th className="px-4 py-2">Result</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {preview.data.rows.map((r) => (
                <tr key={r.row} className={r.status === 'error' ? 'bg-destructive/5' : ''}>
                  <td className="px-4 py-1.5 text-muted-foreground">{r.row}</td>
                  <td className="px-4 py-1.5">{r.name}</td>
                  <td className="px-4 py-1.5">{r.email}</td>
                  <td className="px-4 py-1.5">
                    <span className="font-medium">{r.status}</span>
                    {[...r.problems, ...r.warnings].map((p) => <span key={p} className="block text-xs text-muted-foreground">{p}</span>)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      <section className="rounded-lg border border-border bg-card">
        <h2 className="border-b border-border px-4 py-2 font-semibold">Earlier imports</h2>
        <ul className="divide-y divide-border text-sm">
          {(batches.data?.items ?? []).map((b) => <BatchRow key={b.id} batch={b} />)}
          {batches.data?.items.length === 0 && <li className="px-4 py-3 text-muted-foreground">None yet.</li>}
        </ul>
      </section>
    </div>
  );
}

function BatchRow({ batch: b }: { batch: Awaited<ReturnType<typeof listImports>>['items'][number] }) {
  const qc = useQueryClient();
  const undo = useMutation({
    mutationFn: () => undoImport(b.id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['imports'] });
      void qc.invalidateQueries({ queryKey: ['review'] });
    },
  });
  const report = useMutation({ mutationFn: async () => saveBlob(await batchReport(b.id), `import-${b.id.slice(0, 8)}-errors.xlsx`) });
  return (
    <li className="flex flex-wrap items-center gap-3 px-4 py-2">
      <span className="flex-1 min-w-0">
        <span className="font-medium">{b.file_name}</span>
        <span className="block text-xs text-muted-foreground">
          {new Date(b.created_at).toLocaleString()} by {b.created_by} · {b.rows_created} new, {b.rows_updated} updated, {b.rows_failed} skipped
          {b.undone_at ? ` · undone ${new Date(b.undone_at).toLocaleDateString()}` : ` · ${b.open} still in review`}
        </span>
      </span>
      {b.rows_failed > 0 && (
        <button type="button" className={btn} disabled={report.isPending} onClick={() => report.mutate()}>
          <Download className="h-3.5 w-3.5" /> Errors
        </button>
      )}
      {!b.undone_at && b.open > 0 && (
        <button type="button" className={btn} disabled={undo.isPending}
          onClick={() => window.confirm(`Remove the ${b.open} rows of this import that are still waiting for review?`) && undo.mutate()}>
          <Undo2 className="h-3.5 w-3.5" /> Undo
        </button>
      )}
      {(undo.error || report.error) && <p role="alert" className="w-full text-xs text-destructive">{((undo.error || report.error) as Error).message}</p>}
    </li>
  );
}
