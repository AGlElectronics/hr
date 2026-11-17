import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FileSignature, Plus, Trash2, Upload } from 'lucide-react';

import { canGlobal, getMe } from '@/api/me';
import {
  createDocumentTemplate,
  deleteDocumentTemplate,
  getDocumentTemplate,
  inspectPDF,
  listDocumentTemplates,
  listTemplateVersions,
  publishTemplateVersion,
  updateDocumentTemplate,
  type DocumentTemplateInput,
} from '@/api/signing';
import { noAutofill } from '@/lib/noAutofill';
import { cn } from '@/lib/utils';

const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2 text-sm';

const HR_ATTRS: { value: string; label: string }[] = [
  { value: '', label: '— no autofill —' },
  { value: 'first_name', label: 'First name' },
  { value: 'last_name', label: 'Last name' },
  { value: 'legal_name', label: 'Legal name' },
  { value: 'display_name', label: 'Display name' },
  { value: 'nickname', label: 'Nickname' },
  { value: 'email', label: 'Private email' },
  { value: 'org_email', label: 'Org email' },
  { value: 'phone', label: 'Phone' },
  { value: 'date_of_birth', label: 'Date of birth' },
  { value: 'address_line', label: 'Address (one line)' },
  { value: 'address_street', label: 'Street' },
  { value: 'address_city', label: 'City' },
  { value: 'address_postal_code', label: 'Postal code' },
  { value: 'address_country', label: 'Country' },
];

type FieldRow = { pdf_field_name: string; field_type: string; hr_attribute: string; from_pdf?: boolean };

/** Best-effort match of a PDF field name to a volunteer attribute. */
function guessHrAttribute(pdfName: string): string {
  const n = pdfName.toLowerCase().replace(/[^a-z0-9]+/g, '');
  const rules: [RegExp, string][] = [
    [/(givenname|firstname|vorname)/, 'first_name'],
    [/(familyname|lastname|surname|nachname)/, 'last_name'],
    [/(legalname|fullname|vollstandigername|vollstaendigername)/, 'legal_name'],
    [/(displayname)/, 'display_name'],
    [/(nickname|^nick)/, 'nickname'],
    [/(orgemail|workemail|conemail)/, 'org_email'],
    [/(email|privateemail|e-?mail|^mail)/, 'email'],
    [/(phone|mobile|telefon|^tel)/, 'phone'],
    [/(dateofbirth|birthday|geburtsdatum|^dob)/, 'date_of_birth'],
    [/(addressline|address1|address2|adresse|^address)/, 'address_line'],
    [/(housenr|street|strasse|straße)/, 'address_street'],
    [/(^city|stadt|^ort)/, 'address_city'],
    [/(postcode|postalcode|^postal|^zip|^plz)/, 'address_postal_code'],
    [/(country|^land)/, 'address_country'],
  ];
  for (const [re, attr] of rules) {
    if (re.test(n)) return attr;
  }
  return '';
}

/** Dedicated Forms library: create forms, upload AcroForm PDFs, map fields to the volunteer. */
export function FormsPage() {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ['me'], queryFn: getMe });
  const canManage = canGlobal(me.data, 'settings.manage');
  const list = useQuery({ queryKey: ['document-templates'], queryFn: listDocumentTemplates });
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const items = list.data?.items ?? [];
  useEffect(() => {
    if (!selectedId && !creating && items.length) setSelectedId(items[0].id);
  }, [items, selectedId, creating]);

  return (
    <div className="p-6 max-w-6xl space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <FileSignature className="h-7 w-7 text-primary" />
            Forms
          </h1>
          <p className="text-sm text-muted-foreground mt-1 max-w-2xl">
            Library of onboarding PDFs (NDA, GDPR, …). Upload an AcroForm, map each PDF field to a volunteer
            attribute, then attach forms on each onboarding&apos;s Documents step. Publishing creates a new
            immutable version so in-flight signers keep what they were sent.
          </p>
        </div>
        {canManage && (
          <button
            type="button"
            className="inline-flex items-center gap-2 rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground"
            onClick={() => {
              setCreating(true);
              setSelectedId(null);
            }}
          >
            <Plus className="h-4 w-4" />
            New form
          </button>
        )}
      </div>

      {list.isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {list.error && <p className="text-sm text-destructive">{(list.error as Error).message}</p>}

      <div className="grid gap-4 lg:grid-cols-[16rem_1fr]">
        <aside className="rounded-lg border border-border bg-card overflow-hidden">
          <div className="px-3 py-2 border-b border-border text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Library
          </div>
          {items.length === 0 && !creating ? (
            <p className="p-3 text-sm text-muted-foreground">No forms yet. Create NDA, GDPR, or another form to start.</p>
          ) : (
            <ul className="max-h-[28rem] overflow-y-auto">
              {items.map((t) => (
                <li key={t.id}>
                  <button
                    type="button"
                    onClick={() => {
                      setCreating(false);
                      setSelectedId(t.id);
                    }}
                    className={cn(
                      'w-full text-left px-3 py-2.5 text-sm border-b border-border/60 hover:bg-muted/60',
                      selectedId === t.id && !creating && 'bg-primary-soft text-primary',
                    )}
                  >
                    <span className="font-medium block truncate">{t.name}</span>
                    <span className="text-xs text-muted-foreground">
                      {t.current_version ? `v${t.current_version.version}` : 'no PDF yet'}
                      {t.required ? ' · required' : ''}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </aside>

        <main className="min-w-0">
          {creating ? (
            <CreateFormCard
              canManage={!!canManage}
              onCancel={() => setCreating(false)}
              onCreated={(id) => {
                setCreating(false);
                setSelectedId(id);
                void qc.invalidateQueries({ queryKey: ['document-templates'] });
              }}
            />
          ) : selectedId ? (
            <FormEditor key={selectedId} templateId={selectedId} canManage={!!canManage} />
          ) : (
            <div className="rounded-lg border border-dashed border-border p-10 text-center text-sm text-muted-foreground">
              Select a form or create one to upload a PDF and configure field bindings.
            </div>
          )}
        </main>
      </div>
    </div>
  );
}

function CreateFormCard({
  canManage,
  onCancel,
  onCreated,
}: {
  canManage: boolean;
  onCancel: () => void;
  onCreated: (id: string) => void;
}) {
  const [form, setForm] = useState<DocumentTemplateInput>({ name: '', description: '', required: true });
  const [err, setErr] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: () => createDocumentTemplate(form),
    onSuccess: (t) => onCreated(t.id),
    onError: (e: Error) => setErr(e.message),
  });
  if (!canManage) return <p className="text-sm text-muted-foreground">Only HR admins can create forms.</p>;
  return (
    <section className="rounded-lg border border-border bg-card p-5 space-y-3">
      <h2 className="font-semibold text-lg">New form</h2>
      <p className="text-sm text-muted-foreground">Name it first (e.g. NDA). You&apos;ll upload the PDF and map fields next.</p>
      {err && <p className="text-sm text-destructive">{err}</p>}
      <label className="block text-sm">
        Name
        <input className={cn(inputCls, 'mt-1')} value={form.name} onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))} placeholder="NDA" required {...noAutofill} />
      </label>
      <label className="block text-sm">
        Description
        <textarea className={cn(inputCls, 'mt-1')} rows={2} value={form.description} onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))} {...noAutofill} />
      </label>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={form.required} onChange={(e) => setForm((f) => ({ ...f, required: e.target.checked }))} />
        Required by default when attached to an onboarding
      </label>
      <div className="flex gap-2 pt-1">
        <button type="button" disabled={save.isPending || !form.name.trim()} onClick={() => save.mutate()} className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-50">
          Create
        </button>
        <button type="button" onClick={onCancel} className="rounded-pill border border-border px-4 py-2 text-sm">
          Cancel
        </button>
      </div>
    </section>
  );
}

function FormEditor({ templateId, canManage }: { templateId: string; canManage: boolean }) {
  const qc = useQueryClient();
  const detail = useQuery({ queryKey: ['document-template', templateId], queryFn: () => getDocumentTemplate(templateId) });
  const versions = useQuery({ queryKey: ['document-template-versions', templateId], queryFn: () => listTemplateVersions(templateId) });
  const [meta, setMeta] = useState<DocumentTemplateInput | null>(null);
  const [pdf, setPdf] = useState<File | null>(null);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [changeNote, setChangeNote] = useState('');
  const [fields, setFields] = useState<FieldRow[]>([]);
  const [discovered, setDiscovered] = useState(0);
  const [err, setErr] = useState<string | null>(null);
  const [dragOver, setDragOver] = useState(false);
  const [inspecting, setInspecting] = useState(false);

  const t = detail.data;
  useEffect(() => {
    if (!t) return;
    setMeta({ name: t.name, description: t.description, required: t.required });
    const existing = t.current_version?.fields ?? [];
    setFields(
      existing.length
        ? existing.map((f) => ({
            pdf_field_name: f.pdf_field_name,
            field_type: f.field_type || 'text',
            hr_attribute: f.hr_attribute || '',
            from_pdf: true,
          }))
        : [],
    );
    setPdf(null);
    setDiscovered(0);
    setChangeNote('');
    setErr(null);
  }, [t]);

  useEffect(() => {
    if (!pdf) {
      setPreviewUrl((prev) => {
        if (prev) URL.revokeObjectURL(prev);
        return null;
      });
      return;
    }
    const url = URL.createObjectURL(pdf);
    setPreviewUrl((prev) => {
      if (prev) URL.revokeObjectURL(prev);
      return url;
    });
    return () => URL.revokeObjectURL(url);
  }, [pdf]);

  const saveMeta = useMutation({
    mutationFn: () => updateDocumentTemplate(templateId, meta!),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['document-templates'] });
      void qc.invalidateQueries({ queryKey: ['document-template', templateId] });
    },
    onError: (e: Error) => setErr(e.message),
  });
  const del = useMutation({
    mutationFn: () => deleteDocumentTemplate(templateId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['document-templates'] });
    },
    onError: (e: Error) => setErr(e.message),
  });
  const publish = useMutation({
    mutationFn: async () => {
      if (!pdf) throw new Error('Choose a PDF to publish');
      const rows = fields.filter((f) => f.pdf_field_name.trim());
      return publishTemplateVersion(templateId, pdf, rows, changeNote);
    },
    onSuccess: () => {
      setPdf(null);
      setDiscovered(0);
      setChangeNote('');
      void qc.invalidateQueries({ queryKey: ['document-templates'] });
      void qc.invalidateQueries({ queryKey: ['document-template', templateId] });
      void qc.invalidateQueries({ queryKey: ['document-template-versions', templateId] });
    },
    onError: (e: Error) => setErr(e.message),
  });

  const mappedCount = useMemo(() => fields.filter((f) => f.pdf_field_name && f.hr_attribute).length, [fields]);

  if (detail.isLoading || !t || !meta) return <p className="text-sm text-muted-foreground">Loading form…</p>;
  if (detail.error) return <p className="text-sm text-destructive">{(detail.error as Error).message}</p>;

  const onFile = async (file: File | null) => {
    if (!file) return;
    if (file.type !== 'application/pdf' && !file.name.toLowerCase().endsWith('.pdf')) {
      setErr('Upload a PDF with AcroForm fields');
      return;
    }
    setPdf(file);
    setErr(null);
    setDiscovered(0);
    setInspecting(true);
    try {
      const res = await inspectPDF(file);
      if (!res.fields.length) {
        setFields([]);
        setDiscovered(0);
        setErr('No fillable AcroForm fields found. Open the PDF in a form editor, add fields, then upload again — or Add row to map manually.');
      } else {
        setDiscovered(res.fields.length);
        setFields((prev) => {
          const byName = new Map(prev.map((p) => [p.pdf_field_name, p.hr_attribute]));
          return res.fields.map((f) => ({
            pdf_field_name: f.name,
            field_type: f.type || 'text',
            hr_attribute: byName.get(f.name) || guessHrAttribute(f.name),
            from_pdf: true,
          }));
        });
      }
    } catch (e) {
      setFields([]);
      setDiscovered(0);
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setInspecting(false);
    }
  };

  return (
    <div className="space-y-4">
      <section className="rounded-lg border border-border bg-card p-5 space-y-3">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div>
            <h2 className="font-semibold text-lg">{t.name}</h2>
            <p className="text-xs text-muted-foreground">
              {t.current_version ? `Current published version: v${t.current_version.version}` : 'No version published yet — upload a PDF below'}
            </p>
          </div>
          {canManage && (
            <button
              type="button"
              className="inline-flex items-center gap-1 text-xs text-destructive hover:underline"
              disabled={del.isPending}
              onClick={() => window.confirm(`Delete form “${t.name}”?`) && del.mutate()}
            >
              <Trash2 className="h-3.5 w-3.5" /> Delete form
            </button>
          )}
        </div>
        {err && <p className="text-sm text-destructive">{err}</p>}
        {canManage && (
          <>
            <div className="grid gap-3 md:grid-cols-2">
              <label className="block text-sm">
                Name
                <input className={cn(inputCls, 'mt-1')} value={meta.name} onChange={(e) => setMeta((m) => m && { ...m, name: e.target.value })} {...noAutofill} />
              </label>
              <label className="flex items-center gap-2 text-sm mt-6">
                <input type="checkbox" checked={meta.required} onChange={(e) => setMeta((m) => m && { ...m, required: e.target.checked })} />
                Required by default when attached
              </label>
            </div>
            <label className="block text-sm">
              Description
              <textarea className={cn(inputCls, 'mt-1')} rows={2} value={meta.description} onChange={(e) => setMeta((m) => m && { ...m, description: e.target.value })} {...noAutofill} />
            </label>
            <button type="button" disabled={saveMeta.isPending} onClick={() => saveMeta.mutate()} className="rounded-pill border border-border px-3 py-1.5 text-sm hover:bg-muted">
              Save details
            </button>
          </>
        )}
      </section>

      <section className="rounded-lg border border-border bg-card p-5 space-y-4">
        <div>
          <h3 className="font-semibold">PDF upload &amp; field binding</h3>
          <p className="text-sm text-muted-foreground mt-1">
            Drop an AcroForm PDF, then map each PDF field name to a volunteer attribute. Leave the attribute empty to
            skip autofill for that field. Publishing freezes this as the next version.
          </p>
        </div>

        <div className="grid gap-4 lg:grid-cols-2">
          <div className="space-y-3">
            <div
              onDragOver={(e) => {
                e.preventDefault();
                setDragOver(true);
              }}
              onDragLeave={() => setDragOver(false)}
              onDrop={(e) => {
                e.preventDefault();
                setDragOver(false);
                void onFile(e.dataTransfer.files?.[0] ?? null);
              }}
              className={cn(
                'rounded-lg border-2 border-dashed px-4 py-6 text-center transition-colors',
                dragOver ? 'border-primary bg-primary-soft/40' : 'border-border bg-muted/20',
                !canManage && 'opacity-60 pointer-events-none',
              )}
            >
              <Upload className="mx-auto h-8 w-8 text-muted-foreground mb-2" />
              <p className="text-sm font-medium">
                {inspecting ? 'Reading form fields…' : pdf ? pdf.name : 'Drop PDF here or choose a file'}
              </p>
              <p className="text-xs text-muted-foreground mt-1">
                {inspecting
                  ? 'Detecting AcroForm fields'
                  : pdf
                    ? `${Math.round(pdf.size / 1024)} KB`
                    : 'Upload fills the mapping table from the PDF’s form fields'}
              </p>
              {canManage && (
                <label className="inline-block mt-3 cursor-pointer rounded-pill border border-border bg-background px-3 py-1.5 text-xs font-medium hover:bg-muted">
                  Choose PDF
                  <input
                    type="file"
                    accept="application/pdf"
                    className="hidden"
                    onChange={(e) => {
                      void onFile(e.target.files?.[0] ?? null);
                      e.target.value = '';
                    }}
                  />
                </label>
              )}
            </div>

            {discovered > 0 && (
              <p className="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm">
                Recognized <strong>{discovered}</strong> fillable field{discovered === 1 ? '' : 's'}. Choose what each
                one fills from the volunteer, then publish.
              </p>
            )}
          </div>

          <div className="rounded-lg border border-border bg-muted/10 overflow-hidden min-h-[18rem] flex flex-col">
            {previewUrl ? (
              <>
                <iframe title="PDF preview" src={`${previewUrl}#view=FitH`} className="h-[20rem] w-full bg-background" />
                <div className="border-t border-border px-3 py-1.5 text-xs text-muted-foreground flex justify-between gap-2">
                  <span className="truncate">{pdf?.name}</span>
                  <a href={previewUrl} target="_blank" rel="noreferrer" className="text-primary hover:underline shrink-0">
                    Open in new tab
                  </a>
                </div>
              </>
            ) : (
              <div className="flex h-[22rem] items-center justify-center px-4 text-center text-sm text-muted-foreground">
                PDF preview appears here after you choose a file
              </div>
            )}
          </div>
        </div>

        {err && (
          <p role="alert" className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {err}
          </p>
        )}

        <div className="space-y-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h4 className="text-sm font-semibold">
              Field bindings
              {fields.length ? ` · ${fields.length} field${fields.length === 1 ? '' : 's'}` : ''}
              {mappedCount ? ` · ${mappedCount} mapped` : ''}
            </h4>
            {canManage && (
              <button
                type="button"
                className="text-xs text-primary hover:underline"
                onClick={() => setFields((r) => [...r, { pdf_field_name: '', field_type: 'text', hr_attribute: '' }])}
              >
                Add row
              </button>
            )}
          </div>
          {fields.length === 0 ? (
            <p className="rounded-md border border-dashed border-border px-3 py-6 text-center text-sm text-muted-foreground">
              No fields yet. Upload an AcroForm PDF to list them, or Add row to enter names by hand.
            </p>
          ) : (
            <div className="overflow-x-auto rounded-md border border-border">
              <table className="w-full text-sm">
                <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
                  <tr>
                    <th className="px-3 py-2 font-medium">PDF field</th>
                    <th className="px-3 py-2 font-medium w-24">Type</th>
                    <th className="px-3 py-2 font-medium">Fills from volunteer</th>
                    <th className="px-3 py-2 w-16" />
                  </tr>
                </thead>
                <tbody>
                  {fields.map((row, i) => (
                    <tr key={`${row.pdf_field_name}-${i}`} className="border-t border-border">
                      <td className="px-2 py-1.5">
                        {row.from_pdf ? (
                          <code className="block rounded-md border border-border bg-muted/40 px-3 py-2 text-xs">
                            {row.pdf_field_name}
                          </code>
                        ) : (
                          <input
                            className={inputCls}
                            placeholder="e.g. FullName"
                            value={row.pdf_field_name}
                            disabled={!canManage}
                            onChange={(e) =>
                              setFields((rows) => rows.map((r, j) => (j === i ? { ...r, pdf_field_name: e.target.value } : r)))
                            }
                            {...noAutofill}
                          />
                        )}
                      </td>
                      <td className="px-2 py-1.5">
                        <span className="text-xs text-muted-foreground uppercase tracking-wide">{row.field_type || 'text'}</span>
                      </td>
                      <td className="px-2 py-1.5">
                        <select
                          className={inputCls}
                          value={row.hr_attribute}
                          disabled={!canManage}
                          onChange={(e) =>
                            setFields((rows) => rows.map((r, j) => (j === i ? { ...r, hr_attribute: e.target.value } : r)))
                          }
                        >
                          {HR_ATTRS.map((a) => (
                            <option key={a.value || 'none'} value={a.value}>
                              {a.label}
                            </option>
                          ))}
                        </select>
                      </td>
                      <td className="px-2 py-1.5 text-center">
                        {canManage && (
                          <button
                            type="button"
                            className="text-xs text-muted-foreground hover:text-destructive"
                            onClick={() => setFields((rows) => rows.filter((_, j) => j !== i))}
                          >
                            Remove
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <p className="text-xs text-muted-foreground">
            Leave “Fills from volunteer” on — no autofill — for fields the volunteer completes themselves (signature,
            checkboxes, free text).
          </p>
        </div>

        {canManage && (
          <div className="flex flex-wrap items-end gap-3 border-t border-border pt-4">
            <label className="block text-sm flex-1 min-w-[12rem]">
              Change note (optional)
              <input className={cn(inputCls, 'mt-1')} value={changeNote} onChange={(e) => setChangeNote(e.target.value)} placeholder="What changed in this version" {...noAutofill} />
            </label>
            <button
              type="button"
              disabled={publish.isPending || !pdf}
              onClick={() => publish.mutate()}
              className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-50"
            >
              {publish.isPending ? 'Publishing…' : t.current_version ? 'Publish new version' : 'Publish version 1'}
            </button>
          </div>
        )}
      </section>

      <section className="rounded-lg border border-border bg-card p-5">
        <h3 className="font-semibold mb-2">Version history</h3>
        {(versions.data?.items ?? []).length === 0 ? (
          <p className="text-sm text-muted-foreground">No versions yet.</p>
        ) : (
          <ul className="space-y-2 text-sm">
            {(versions.data?.items ?? []).map((v) => (
              <li key={v.id} className="flex flex-wrap justify-between gap-2 rounded-md border border-border px-3 py-2">
                <span>
                  <strong>v{v.version}</strong>
                  {v.file_name ? ` · ${v.file_name}` : ''}
                  {v.change_note ? <span className="block text-xs text-muted-foreground">{v.change_note}</span> : null}
                </span>
                <span className="text-xs text-muted-foreground">{new Date(v.published_at).toLocaleString()}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
