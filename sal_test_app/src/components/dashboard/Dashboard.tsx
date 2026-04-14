import React from 'react';
import { useQuery } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { format } from 'date-fns';
import { PlayCircle, Eye, Activity } from 'lucide-react';
import { notes } from '../../services/api';
import type { Note } from '../../types';

export const Dashboard: React.FC = () => {
  const navigate = useNavigate();
  const { data, isLoading } = useQuery({
    queryKey: ['notes'],
    queryFn: async () => {
      const resp = await notes.list({ limit: 10 });
      return resp.data.items as Note[];
    },
  });

  const getStatusBadge = (status: string) => {
    let color = '#94a3b8';
    if (status === 'submitted') color = '#10b981';
    if (status === 'extracting') color = '#6366f1';
    if (status === 'failed') color = '#ef4444';
    
    return (
      <span style={{ 
        padding: '0.25rem 0.5rem', 
        borderRadius: '0.375rem', 
        fontSize: '0.75rem', 
        fontWeight: 600,
        backgroundColor: color + '20',
        color: color,
        border: `1px solid ${color}40`
      }}>
        {status.toUpperCase()}
      </span>
    );
  };

  return (
    <div>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '2rem' }}>
        <div>
          <h1 style={{ fontSize: '1.875rem', fontWeight: 700, margin: 0 }}>Test Bench</h1>
          <p style={{ color: 'var(--text-muted)', marginTop: '0.25rem' }}>Overview of recent AI extraction runs.</p>
        </div>
        <button 
          onClick={() => navigate('/test/new')}
          style={{ width: 'auto', display: 'flex', alignItems: 'center', gap: '0.5rem', padding: '0.75rem 1.25rem' }}
        >
          <PlayCircle size={20} />
          New Test Run
        </button>
      </header>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '1.5rem', marginBottom: '2.5rem' }}>
        <div className="login-card" style={{ maxWidth: 'none', padding: '1.5rem' }}>
          <div style={{ color: 'var(--text-muted)', fontSize: '0.875rem', fontWeight: 500, marginBottom: '0.5rem' }}>Total Tests</div>
          <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>{data?.length || 0}</div>
        </div>
        <div className="login-card" style={{ maxWidth: 'none', padding: '1.5rem' }}>
          <div style={{ color: 'var(--text-muted)', fontSize: '0.875rem', fontWeight: 500, marginBottom: '0.5rem' }}>Avg. Accuracy</div>
          <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>-- %</div>
        </div>
        <div className="login-card" style={{ maxWidth: 'none', padding: '1.5rem' }}>
          <div style={{ color: 'var(--text-muted)', fontSize: '0.875rem', fontWeight: 500, marginBottom: '0.5rem' }}>Success Rate</div>
          <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>
            {data ? ((data.filter(n => n.status === 'submitted').length / data.length) * 100).toFixed(0) : 0}%
          </div>
        </div>
      </div>

      <div className="login-card" style={{ maxWidth: 'none', padding: 0, overflow: 'hidden' }}>
        <div style={{ padding: '1.25rem 1.5rem', borderBottom: '1px solid var(--border)', display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <Activity size={18} style={{ color: 'var(--primary)' }} />
          <h2 style={{ fontSize: '1rem', fontWeight: 600, margin: 0 }}>Recent Note Generations</h2>
        </div>
        <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left' }}>
          <thead>
            <tr style={{ backgroundColor: '#f8fafc', borderBottom: '1px solid var(--border)' }}>
              <th style={{ padding: '1rem 1.5rem', fontSize: '0.75rem', fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase' }}>Date</th>
              <th style={{ padding: '1rem 1.5rem', fontSize: '0.75rem', fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase' }}>Status</th>
              <th style={{ padding: '1rem 1.5rem', fontSize: '0.75rem', fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase' }}>Alignment</th>
              <th style={{ padding: '1rem 1.5rem', fontSize: '0.75rem', fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr><td colSpan={4} style={{ padding: '2rem', textAlign: 'center' }}>Loading results...</td></tr>
            ) : data?.length === 0 ? (
              <tr><td colSpan={4} style={{ padding: '2rem', textAlign: 'center' }}>No tests run yet.</td></tr>
            ) : data?.map((note) => (
              <tr key={note.id} style={{ borderBottom: '1px solid var(--border)' }}>
                <td style={{ padding: '1rem 1.5rem', fontSize: '0.875rem' }}>
                  <div style={{ fontWeight: 500 }}>{format(new Date(note.created_at), 'MMM d, h:mm a')}</div>
                  <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>ID: {note.id.slice(0, 8)}...</div>
                </td>
                <td style={{ padding: '1rem 1.5rem' }}>
                  {getStatusBadge(note.status)}
                </td>
                <td style={{ padding: '1rem 1.5rem', fontSize: '0.875rem', fontWeight: 600 }}>
                  {note.policy_alignment_pct !== undefined ? `${note.policy_alignment_pct.toFixed(0)}%` : '--'}
                </td>
                <td style={{ padding: '1rem 1.5rem' }}>
                  <button 
                    onClick={() => navigate(`/test/results/${note.id}`)}
                    style={{ 
                      width: 'auto', 
                      padding: '0.4rem 0.8rem', 
                      fontSize: '0.75rem', 
                      display: 'flex', 
                      alignItems: 'center', 
                      gap: '0.375rem',
                      backgroundColor: 'white',
                      color: 'var(--text)',
                      border: '1px solid var(--border)'
                    }}
                  >
                    <Eye size={14} />
                    Analyze
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
};
