import React, { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { subjects as subjectsApi } from '../../services/api';
import { Check } from 'lucide-react';

interface SubjectCreatorProps {
  value: string;
  onChange: (id: string) => void;
}

export const SubjectCreator: React.FC<SubjectCreatorProps> = ({ value, onChange }) => {
  const [showAdd, setShowAdd] = useState(false);
  const [name, setName] = useState('');
  const [creating, setCreating] = useState(false);

  const { data: subjectList, refetch } = useQuery({
    queryKey: ['subjects'],
    queryFn: async () => {
      const resp = await subjectsApi.list();
      return resp.data.items as any[];
    },
  });

  const handleCreate = async () => {
    if (!name) return;
    setCreating(true);
    try {
      const resp = await subjectsApi.create({ name });
      await refetch();
      onChange(resp.data.id);
      setShowAdd(false);
      setName('');
    } catch (err) {
      alert('Failed to create subject');
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="form-group">
      <label style={{ display: 'flex', justifyContent: 'space-between' }}>
        Select Subject (Patient)
        <button 
          onClick={() => setShowAdd(!showAdd)} 
          style={{ width: 'auto', padding: '0 0.5rem', background: 'transparent', color: 'var(--primary)', fontSize: '0.75rem', border: 'none' }}
        >
          {showAdd ? 'Cancel' : '+ Create New'}
        </button>
      </label>

      {showAdd ? (
        <div style={{ display: 'flex', gap: '0.5rem' }}>
          <input 
            type="text" 
            placeholder="Patient Name" 
            value={name} 
            onChange={e => setName(e.target.value)} 
            style={{ flex: 1, padding: '0.5rem' }}
          />
          <button 
            onClick={handleCreate} 
            disabled={creating}
            style={{ width: 'auto', padding: '0.5rem' }}
          >
            {creating ? '...' : <Check size={18} />}
          </button>
        </div>
      ) : (
        <select 
          value={value} 
          onChange={e => onChange(e.target.value)}
          className="form-select"
          style={{ width: '100%', padding: '0.75rem', borderRadius: '0.5rem', border: '1px solid var(--border)' }}
        >
          <option value="">-- Choose a patient --</option>
          {subjectList?.map(s => (
            <option key={s.id} value={s.id}>{s.name}</option>
          ))}
        </select>
      )}
    </div>
  );
};
