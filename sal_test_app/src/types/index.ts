export type NoteStatus = 'extracting' | 'draft' | 'submitted' | 'failed';
export type RecordingStatus = 'pending_upload' | 'uploaded' | 'transcribing' | 'transcribed' | 'failed';
export type TransformationType = 'direct' | 'inference';
export type FormVersionStatus = 'draft' | 'published' | 'archived';

export interface TokenPair {
  access_token: string;
  refresh_token: string;
  expires_at: string;
}

export interface User {
  id: string;
  email: string;
  full_name: string;
  role: string;
  clinic_id: string;
}

export interface Clinic {
  id: string;
  name: string;
  vertical: 'veterinary' | 'dental' | 'aged_care';
  status: string;
}

export interface Subject {
  id: string;
  name: string;
  status: string;
  // Add other fields as needed
}

export interface Recording {
  id: string;
  clinic_id: string;
  staff_id: string;
  subject_id?: string;
  status: RecordingStatus;
  content_type: string;
  duration_seconds?: number;
  transcript?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateRecordingResponse {
  recording: Recording;
  upload_url: string;
}

export interface FormField {
  id: string;
  position: number;
  title: string;
  type: string;
  config: any;
  ai_prompt?: string;
  required: boolean;
  skippable: boolean;
}

export interface FormVersion {
  id: string;
  form_id: string;
  status: FormVersionStatus;
  version_major?: number;
  version_minor?: number;
  fields?: FormField[];
  created_at: string;
}

export interface Form {
  id: string;
  name: string;
  description?: string;
  latest_published?: FormVersion;
  draft?: FormVersion;
}

export interface NoteField {
  field_id: string;
  value?: string;
  confidence?: number;
  source_quote?: string;
  transformation_type?: TransformationType;
  overridden_by?: string;
  overridden_at?: string;
}

export interface Note {
  id: string;
  clinic_id: string;
  recording_id?: string;
  form_version_id: string;
  subject_id?: string;
  status: NoteStatus;
  error_message?: string;
  policy_alignment_pct?: number;
  created_at: string;
  updated_at: string;
  fields?: NoteField[];
}

export interface SSEEvent {
  clinic_id: string;
  event_id: string;
  note_id: string;
  event_type: string;
}
