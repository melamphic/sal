import React, { useState, useEffect } from 'react';
import { useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { CheckCircle2, Clock, BarChart2 } from 'lucide-react';
import { notes, forms, recordings } from '../../services/api';
import type { Note, FormVersion, Recording } from '../../types';
import { useSSE } from '../../hooks/useSSE';

export const NoteComparison: React.FC = () => {
  const { noteId } = useParams<{ noteId: string }>();
  const lastEvent = useSSE();
  const [groundTruth, setGroundTruth] = useState<Record<string, string>>({});

  const { data: note, refetch: refetchNote } = useQuery({
    queryKey: ['note', noteId],
    queryFn: async () => {
      const resp = await notes.get(noteId!);
      return resp.data as Note;
    },
    enabled: !!noteId,
  });

  const { data: form } = useQuery({
    queryKey: ['formVersion', note?.form_version_id],
    queryFn: async () => {
      const resp = await forms.get(note!.form_version_id);
      return resp.data as FormVersion;
    },
    enabled: !!note?.form_version_id,
  });

  const { data: recording } = useQuery({
    queryKey: ['recording', note?.recording_id],
    queryFn: async () => {
      const resp = await recordings.get(note!.recording_id!);
      return resp.data as Recording;
    },
    enabled: !!note?.recording_id,
  });

  useEffect(() => {
    if (lastEvent && lastEvent.note_id === noteId) {
      refetchNote();
    }
  }, [lastEvent, noteId, refetchNote]);

  const calculateScore = (aiVal: string | undefined, expected: string | undefined) => {
    if (!expected) return null;
    if (!aiVal) return 0;
    
    // Simple similarity: 1 - distance/max_length
    const s1 = aiVal.toLowerCase().trim();
    const s2 = expected.toLowerCase().trim();
    if (s1 === s2) return 100;
    return 0; // Boolean match for simplicity, can be improved with Levenshtein
  };

  const getStatusColor = (status: string) => {
    switch (status) {
      case 'submitted': return '#10b981';
      case 'extracting': return '#6366f1';
      case 'failed': return '#ef4444';
      default: return '#f59e0b';
    }
  };

  if (!note || !form) return <div>Loading analysis...</div>;

  return (
    <div className="analysis-container">
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end', marginBottom: '2rem' }}>
        <div>
          <h1 style={{ fontSize: '1.875rem', fontWeight: 700, margin: 0 }}>AI Extraction Analysis</h1>
          <p style={{ color: 'var(--text-muted)', marginTop: '0.25rem' }}>Note ID: {note.id}</p>
        </div>
        <div style={{ 
          padding: '0.5rem 1rem', 
          borderRadius: '2rem', 
          backgroundColor: getStatusColor(note.status),
          color: 'white',
          fontSize: '0.875rem',
          fontWeight: 600,
          textTransform: 'uppercase'
        }}>
          {note.status}
        </div>
      </header>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '2rem' }}>
        {/* Left Column: Transcript & Recording Info */}
        <section>
          <div className="login-card" style={{ maxWidth: 'none', height: '100%', display: 'flex', flexDirection: 'column' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '1rem', color: 'var(--primary)' }}>
              <Clock size={20} />
              <h2 style={{ fontSize: '1.125rem', fontWeight: 600, margin: 0 }}>Audio Transcript</h2>
            </div>
            <div style={{ 
              flex: 1, 
              backgroundColor: '#f1f5f9', 
              padding: '1.5rem', 
              borderRadius: '0.5rem',
              fontSize: '0.9375rem',
              lineHeight: 1.6,
              whiteSpace: 'pre-wrap',
              maxHeight: '600px',
              overflowY: 'auto'
            }}>
              {recording?.transcript || 'Transcription in progress...'}
            </div>
            {note.policy_alignment_pct !== undefined && (
              <div style={{ marginTop: '1.5rem', padding: '1rem', backgroundColor: '#eff6ff', borderRadius: '0.5rem', border: '1px solid #bfdbfe' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', color: '#1e40af', fontWeight: 600 }}>
                  <BarChart2 size={18} />
                  Policy Alignment: {note.policy_alignment_pct.toFixed(1)}%
                </div>
              </div>
            )}
          </div>
        </section>

        {/* Right Column: Fields Comparison */}
        <section>
          <div className="login-card" style={{ maxWidth: 'none' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '1.5rem', color: 'var(--primary)' }}>
              <CheckCircle2 size={20} />
              <h2 style={{ fontSize: '1.125rem', fontWeight: 600, margin: 0 }}>Field Extraction Results</h2>
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem' }}>
              {form.fields?.map((fieldSpec) => {
                const noteField = note.fields?.find(f => f.field_id === fieldSpec.id);
                const score = calculateScore(noteField?.value, groundTruth[fieldSpec.id]);
                
                return (
                  <div key={fieldSpec.id} style={{ 
                    padding: '1rem', 
                    border: '1px solid var(--border)', 
                    borderRadius: '0.75rem',
                    backgroundColor: noteField?.confidence && noteField.confidence < 0.7 ? '#fffbeb' : 'white'
                  }}>
                    <div style={{ fontWeight: 600, marginBottom: '0.75rem', display: 'flex', justifyContent: 'space-between' }}>
                      <span>{fieldSpec.title}</span>
                      {noteField?.confidence && (
                        <span style={{ 
                          fontSize: '0.75rem', 
                          padding: '0.25rem 0.5rem', 
                          borderRadius: '1rem',
                          backgroundColor: noteField.confidence > 0.8 ? '#dcfce7' : '#fef3c7',
                          color: noteField.confidence > 0.8 ? '#166534' : '#92400e'
                        }}>
                          {(noteField.confidence * 100).toFixed(0)}% confidence
                        </span>
                      )}
                    </div>

                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
                      <div>
                        <label style={{ fontSize: '0.75rem', color: 'var(--text-muted)', display: 'block', marginBottom: '0.25rem' }}>AI Extracted</label>
                        <div style={{ 
                          padding: '0.5rem', 
                          backgroundColor: '#f8fafc', 
                          borderRadius: '0.375rem', 
                          fontSize: '0.875rem',
                          minHeight: '1.5rem',
                          border: '1px solid #e2e8f0'
                        }}>
                          {noteField?.value || <em style={{ color: '#94a3b8' }}>null</em>}
                        </div>
                        {noteField?.source_quote && (
                          <div style={{ fontSize: '0.75rem', color: '#6366f1', marginTop: '0.5rem', fontStyle: 'italic' }}>
                            "{noteField.source_quote}"
                          </div>
                        )}
                      </div>
                      <div>
                        <label style={{ fontSize: '0.75rem', color: 'var(--text-muted)', display: 'block', marginBottom: '0.25rem' }}>Expected (Ground Truth)</label>
                        <input 
                          type="text" 
                          value={groundTruth[fieldSpec.id] || ''} 
                          onChange={(e) => setGroundTruth(prev => ({ ...prev, [fieldSpec.id]: e.target.value }))}
                          placeholder="Enter expected value..."
                          style={{ 
                            width: '100%', 
                            padding: '0.5rem', 
                            borderRadius: '0.375rem', 
                            fontSize: '0.875rem',
                            border: '1px solid var(--border)',
                            boxSizing: 'border-box'
                          }}
                        />
                        {score !== null && (
                          <div style={{ 
                            fontSize: '0.75rem', 
                            marginTop: '0.5rem', 
                            fontWeight: 600,
                            color: score === 100 ? '#10b981' : '#ef4444'
                          }}>
                            Match: {score}%
                          </div>
                        )}
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        </section>
      </div>
    </div>
  );
};
