import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import {
  createDocumentTemplate,
  deleteDocumentTemplate,
  listDocumentTemplates,
  publishTemplateVersion,
  updateDocumentTemplate,
  type DocumentTemplate,
  type DocumentTemplateInput,
} from '@/api/signing';
import { noAutofill } from '@/lib/noAutofill';

const inputCls = 'w-full rounded-md border border-input bg-background px-3 py-2 text-sm';
const empty: DocumentTemplateInput = { name: '', description: '', required: true };

const HR_ATTRS = [
  '',
  'first_name',
  'last_name',
  'legal_name',
  'display_name',
  'nickname',
  'email',
  'org_email',
  'phone',
  'date_of_birth',
  'address_line',
];

/** Settings → Forms: library entries + publish immutable PDF/mapping versions. */
export function SigningTemplates() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ['document-templates'], queryFn: listDocumentTemplates });
  const [form, setForm] = useState<DocumentTemplateInput>(empty);
  const [editId, setEditId] = useState<string | null>(null);
  const [publishFor, setPublishFor] = useState<string | null>(null);
  const [pdf, setPdf] = useState<File | null>(null);
  const [changeNote, setChangeNote] = useState('');
  const [fieldRows, setFieldRows] = useState<Array<{ pdf_field_name: string; field_type: string; hr_attribute: string }>>([
    { pdf_field_name: '', field_type: 'text', hr_attribute: '' },
  ]);
  const [err, setErr] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => (editId ? updateDocumentTemplate(editId, form) : createDocumentTemplate(form)),
    onSuccess: () => {
      setForm(empty);
      setEditId(null);
      setErr(null);
      void qc.invalidateQueries({ queryKey: ['document-templates'] });
    },
    onError: (e: Error) => setErr(e.message),
  });
  const del = useMutation({
    mutationFn: deleteDocumentTemplate,
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['document-templates'] }),
  });
  const publish = useMutation({
    mutationFn: async () => {
      if (!publishFor || !pdf) throw new Error('PDF required');
      const fields = fieldRows.filter((f) => f.pdf_field_name.trim());
      return publishTemplateVersion(publishFor, pdf, fields, changeNote);
    },
    onSuccess: () => {
      setPublishFor(null);
      setPdf(null);
      setChangeNote('');
      setFieldRows([{ pdf_field_name: '', field_type: 'text', hr_attribute: '' }]);
      setErr(null);
      void qc.invalidateQueries({ queryKey: ['document-templates'] });
    },
    onError: (e: Error) => setErr(e.message),
  });

  const startEdit = (t: DocumentTemplate) => {
    setEditId(t.id);
    setForm({ name: t.name, description: t.description, required: t.required });
  };

  return (
    <section className="space-y-4 rounded-lg border border-border bg-card p-4">
      <div>
        <h2 className="font-semibold">Forms library</h2>
        <p className="text-xs text-muted-foreground">
          Create named forms (NDA, GDPR, …), then publish a version with an AcroForm PDF and field mappings.
          Editing the PDF or mappings publishes a new version; in-flight onboardings keep the version they were sent.
        </p>
      </div>
      {err && <p className="text-sm text-destructive">{err}</p>}
      {list.isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {list.error && <p className="text-sm text-destructive">{(list.error as Error).message}</p>}
      <ul className="space-y-2">
        {(list.data?.items ?? []).map((t) => (
          <li key={t.id} className="rounded-md border border-border px-3 py-2 text-sm space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span>
                <strong>{t.name}</strong>
                {t.required ? ' · required by default' : ' · optional'}
                {t.current_version ? ` · current v${t.current_version.version}` : ' · no version yet'}
                {t.description ? <span className="block text-xs text-muted-foreground">{t.description}</span> : null}
              </span>
              <span className="flex gap-2 text-xs">
                <button type="button" className="text-primary hover:underline" onClick={() => startEdit(t)}>
                  Edit
                </button>
                <button type="button" className="text-primary hover:underline" onClick={() => setPublishFor(t.id)}>
                  Publish version
                </button>
                <button
                  type="button"
                  className="text-destructive hover:underline"
                  disabled={del.isPending}
                  onClick={() => window.confirm(`Delete form “${t.name}”?`) && del.mutate(t.id)}
                >
                  Delete
                </button>
              </span>
            </div>
            {publishFor === t.id && (
              <div className="space-y-2 border-t border-border pt-2">
                <label className="block text-xs">
                  AcroForm PDF
                  <input type="file" accept="application/pdf" className="mt-1 block text-sm" onChange={(e) => setPdf(e.target.files?.[0] ?? null)} />
                </label>
                <label className="block text-xs">
                  Change note
                  <input className={inputCls} value={changeNote} onChange={(e) => setChangeNote(e.target.value)} placeholder="What changed in this version" {...noAutofill} />
                </label>
                <p className="text-xs text-muted-foreground">
                  Optional mappings (leave empty to auto-list PDF fields with blank attributes). HR attributes fill from the volunteer profile.
                </p>
                {fieldRows.map((row, i) => (
                  <div key={i} className="grid grid-cols-3 gap-2">
                    <input
                      className={inputCls}
                      placeholder="PDF field name"
                      value={row.pdf_field_name}
                      onChange={(e) => setFieldRows((rows) => rows.map((r, j) => (j === i ? { ...r, pdf_field_name: e.target.value } : r)))}
                      {...noAutofill}
                    />
                    <select
                      className={inputCls}
                      value={row.hr_attribute}
                      onChange={(e) => setFieldRows((rows) => rows.map((r, j) => (j === i ? { ...r, hr_attribute: e.target.value } : r)))}
                    >
                      {HR_ATTRS.map((a) => (
                        <option key={a || 'none'} value={a}>
                          {a || '(no autofill)'}
                        </option>
                      ))}
                    </select>
                    <button
                      type="button"
                      className="text-xs text-muted-foreground"
                      onClick={() => setFieldRows((rows) => rows.filter((_, j) => j !== i))}
                    >
                      Remove
                    </button>
                  </div>
                ))}
                <button type="button" className="text-xs text-primary" onClick={() => setFieldRows((r) => [...r, { pdf_field_name: '', field_type: 'text', hr_attribute: '' }])}>
                  Add mapping row
                </button>
                <div className="flex gap-2">
                  <button
                    type="button"
                    disabled={publish.isPending || !pdf}
                    onClick={() => publish.mutate()}
                    className="rounded-pill bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground disabled:opacity-50"
                  >
                    Publish
                  </button>
                  <button type="button" className="text-xs" onClick={() => setPublishFor(null)}>
                    Cancel
                  </button>
                </div>
              </div>
            )}
          </li>
        ))}
      </ul>

      <form
        className="space-y-2 border-t border-border pt-3"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <h3 className="text-sm font-medium">{editId ? 'Edit form' : 'New form'}</h3>
        <input className={inputCls} placeholder="Name (e.g. NDA)" value={form.name} onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))} required {...noAutofill} />
        <textarea className={inputCls} placeholder="Description" value={form.description} onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))} rows={2} {...noAutofill} />
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={form.required} onChange={(e) => setForm((f) => ({ ...f, required: e.target.checked }))} />
          Required by default when attached
        </label>
        <div className="flex gap-2">
          <button type="submit" disabled={save.isPending} className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
            {editId ? 'Save' : 'Create'}
          </button>
          {editId && (
            <button
              type="button"
              className="text-sm"
              onClick={() => {
                setEditId(null);
                setForm(empty);
              }}
            >
              Cancel
            </button>
          )}
        </div>
      </form>
    </section>
  );
}
