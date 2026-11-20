import { apiBlob, apiRequest } from './client';

export type DocumentTemplateField = {
  id?: string;
  pdf_field_name: string;
  field_type: string;
  hr_attribute: string;
};

export type DocumentTemplateVersion = {
  id: string;
  template_id: string;
  version: number;
  file_name?: string;
  file_size?: number;
  has_pdf: boolean;
  change_note?: string;
  published_by?: string;
  published_at: string;
  fields?: DocumentTemplateField[];
};

export type DocumentTemplate = {
  id: string;
  name: string;
  description: string;
  required: boolean;
  current_version_id?: string;
  current_version?: DocumentTemplateVersion;
  created_at: string;
  updated_at: string;
};

export type DocumentTemplateInput = {
  name: string;
  description: string;
  required: boolean;
};

export type OnboardingDocument = {
  id: string;
  draft_id: string;
  template_id: string;
  template_name: string;
  template_version?: number;
  template_version_id?: string;
  required: boolean;
  status: 'pending' | 'sent' | 'signed' | 'waived' | 'stub_blocked';
  status_note?: string;
  waived_by?: string;
  waived_at?: string;
  created_at: string;
  updated_at: string;
};

export type SendResult = {
  document: OnboardingDocument;
};

export const listDocumentTemplates = () =>
  apiRequest<{ items: DocumentTemplate[] }>('/api/document-templates');

export const getDocumentTemplate = (id: string) =>
  apiRequest<DocumentTemplate>(`/api/document-templates/${id}`);

export type DiscoveredPDFField = { name: string; type: string };

/** Upload a PDF and get its AcroForm field names (no publish yet). */
export async function inspectPDF(file: File): Promise<{ fields: DiscoveredPDFField[] }> {
  const fd = new FormData();
  fd.append('file', file);
  return apiRequest<{ fields: DiscoveredPDFField[] }>('/api/document-templates/inspect', {
    method: 'POST',
    body: fd,
  });
}

export const createDocumentTemplate = (body: DocumentTemplateInput) =>
  apiRequest<DocumentTemplate>('/api/document-templates', { method: 'POST', body });

export const updateDocumentTemplate = (id: string, body: DocumentTemplateInput) =>
  apiRequest<DocumentTemplate>(`/api/document-templates/${id}`, { method: 'PUT', body });

export const deleteDocumentTemplate = (id: string) =>
  apiRequest<void>(`/api/document-templates/${id}`, { method: 'DELETE' });

export const listTemplateVersions = (id: string) =>
  apiRequest<{ items: DocumentTemplateVersion[] }>(`/api/document-templates/${id}/versions`);

/** Multipart publish: PDF file + optional fields JSON + change_note. */
export async function publishTemplateVersion(
  id: string,
  file: File,
  fields: Array<{ pdf_field_name: string; field_type: string; hr_attribute: string }>,
  changeNote: string,
): Promise<DocumentTemplateVersion> {
  const fd = new FormData();
  fd.append('file', file);
  fd.append('change_note', changeNote);
  if (fields.length) fd.append('fields', JSON.stringify(fields));
  return apiRequest<DocumentTemplateVersion>(`/api/document-templates/${id}/versions`, {
    method: 'POST',
    body: fd,
  });
}

export const listOnboardingDocuments = (draftId: string) =>
  apiRequest<{ items: OnboardingDocument[] }>(`/api/onboardings/${draftId}/documents`);

export const attachOnboardingDocument = (draftId: string, templateId: string) =>
  apiRequest<OnboardingDocument>(`/api/onboardings/${draftId}/documents`, {
    method: 'POST',
    body: { template_id: templateId },
  });

export const detachOnboardingDocument = (draftId: string, docId: string) =>
  apiRequest<void>(`/api/onboardings/${draftId}/documents/${docId}`, { method: 'DELETE' });

export const sendOnboardingDocument = (draftId: string, docId: string) =>
  apiRequest<SendResult>(`/api/onboardings/${draftId}/documents/${docId}/send`, { method: 'POST' });

export const waiveOnboardingDocument = (draftId: string, docId: string, reason: string) =>
  apiRequest<OnboardingDocument>(`/api/onboardings/${draftId}/documents/${docId}/waive`, {
    method: 'POST',
    body: { reason },
  });

export const unwaiveOnboardingDocument = (draftId: string, docId: string) =>
  apiRequest<OnboardingDocument>(`/api/onboardings/${draftId}/documents/${docId}/unwaive`, { method: 'POST' });

/** Filled PDF for staff preview (same mappings as sign-hr). */
export const fetchOnboardingDocumentPdf = (draftId: string, docId: string) =>
  apiBlob(`/api/onboardings/${draftId}/documents/${docId}/pdf`);
