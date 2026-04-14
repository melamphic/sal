import React from 'react';
import { useQuery } from '@tanstack/react-query';
import { forms } from '../../services/api';
import type { Form } from '../../types';

interface FormSelectorProps {
  value: string;
  onChange: (id: string) => void;
}

export const FormSelector: React.FC<FormSelectorProps> = ({ value, onChange }) => {
  const { data, isLoading, error } = useQuery({
    queryKey: ['forms'],
    queryFn: async () => {
      const resp = await forms.list();
      return resp.data.items as Form[];
    },
  });

  if (isLoading) return <div>Loading forms...</div>;
  if (error) return <div className="message error">Error loading forms</div>;

  return (
    <div className="form-group">
      <label>Select Form Template</label>
      <select 
        value={value} 
        onChange={(e) => onChange(e.target.value)}
        className="form-select"
        style={{
          width: '100%',
          padding: '0.75rem',
          borderRadius: '0.5rem',
          border: '1px solid var(--border)',
          backgroundColor: 'white'
        }}
      >
        <option value="">-- Choose a form --</option>
        {data?.map((f) => (
          <option key={f.id} value={f.latest_published?.id || ''}>
            {f.name} {f.latest_published ? `(v${f.latest_published.version_major}.${f.latest_published.version_minor})` : '(No published version)'}
          </option>
        ))}
      </select>
    </div>
  );
};
