import React, { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { UserPlus, Shield, Mail } from 'lucide-react';
import { staff as staffApi } from '../../services/api';

export const StaffManagement: React.FC = () => {
  const [showInvite, setShowInvite] = useState(false);
  const [inviteData, setInviteData] = useState({
    email: '',
    full_name: '',
    role: 'vet',
    note_tier: 'standard',
  });
  const [loading, setLoading] = useState(false);

  const { data: staffList, refetch } = useQuery({
    queryKey: ['staff'],
    queryFn: async () => {
      const resp = await staffApi.list();
      return resp.data.items as any[];
    },
  });

  const handleInvite = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    try {
      await staffApi.invite({
        ...inviteData,
        permissions: {}, // Backend will apply defaults based on role
      });
      setShowInvite(false);
      setInviteData({ email: '', full_name: '', role: 'vet', note_tier: 'standard' });
      refetch();
    } catch (err: any) {
      alert(err.response?.data?.title || 'Failed to invite staff');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '2rem' }}>
        <div>
          <h1 style={{ fontSize: '1.875rem', fontWeight: 700, margin: 0 }}>Staff & Permissions</h1>
          <p style={{ color: 'var(--text-muted)', marginTop: '0.25rem' }}>Manage your clinic team and their access levels.</p>
        </div>
        <button 
          onClick={() => setShowInvite(true)}
          style={{ width: 'auto', display: 'flex', alignItems: 'center', gap: '0.5rem' }}
        >
          <UserPlus size={20} />
          Invite Staff
        </button>
      </header>

      {showInvite && (
        <div className="login-card" style={{ maxWidth: 'none', marginBottom: '2rem' }}>
          <h2 style={{ fontSize: '1.25rem', fontWeight: 600, marginBottom: '1.5rem' }}>Invite New Team Member</h2>
          <form onSubmit={handleInvite} style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1.5rem' }}>
            <div className="form-group">
              <label>Full Name</label>
              <input 
                type="text" 
                value={inviteData.full_name} 
                onChange={e => setInviteData({...inviteData, full_name: e.target.value})}
                required 
              />
            </div>
            <div className="form-group">
              <label>Email Address</label>
              <input 
                type="email" 
                value={inviteData.email} 
                onChange={e => setInviteData({...inviteData, email: e.target.value})}
                required 
              />
            </div>
            <div className="form-group">
              <label>Role</label>
              <select 
                value={inviteData.role} 
                onChange={e => setInviteData({...inviteData, role: e.target.value})}
                className="form-select"
                style={{ width: '100%', padding: '0.75rem', borderRadius: '0.5rem', border: '1px solid var(--border)' }}
              >
                <option value="vet">Veterinarian</option>
                <option value="vet_nurse">Vet Nurse</option>
                <option value="admin">Administrator</option>
                <option value="receptionist">Receptionist</option>
              </select>
            </div>
            <div className="form-group">
              <label>Billing Tier</label>
              <select 
                value={inviteData.note_tier} 
                onChange={e => setInviteData({...inviteData, note_tier: e.target.value})}
                className="form-select"
                style={{ width: '100%', padding: '0.75rem', borderRadius: '0.5rem', border: '1px solid var(--border)' }}
              >
                <option value="standard">Standard (Full access)</option>
                <option value="nurse">Nurse (50% quota)</option>
                <option value="none">None (Admin only)</option>
              </select>
            </div>
            <div style={{ gridColumn: 'span 2', display: 'flex', gap: '1rem', justifyContent: 'flex-end' }}>
              <button type="button" onClick={() => setShowInvite(false)} style={{ width: 'auto', backgroundColor: 'transparent', color: 'var(--text)', border: '1px solid var(--border)' }}>Cancel</button>
              <button type="submit" disabled={loading} style={{ width: 'auto' }}>{loading ? 'Sending...' : 'Send Invitation'}</button>
            </div>
          </form>
        </div>
      )}

      <div className="login-card" style={{ maxWidth: 'none', padding: 0 }}>
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ textAlign: 'left', borderBottom: '1px solid var(--border)', backgroundColor: '#f8fafc' }}>
              <th style={{ padding: '1rem 1.5rem', fontSize: '0.75rem', fontWeight: 600, color: 'var(--text-muted)' }}>MEMBER</th>
              <th style={{ padding: '1rem 1.5rem', fontSize: '0.75rem', fontWeight: 600, color: 'var(--text-muted)' }}>ROLE</th>
              <th style={{ padding: '1rem 1.5rem', fontSize: '0.75rem', fontWeight: 600, color: 'var(--text-muted)' }}>STATUS</th>
              <th style={{ padding: '1rem 1.5rem', fontSize: '0.75rem', fontWeight: 600, color: 'var(--text-muted)' }}>PERMISSIONS</th>
            </tr>
          </thead>
          <tbody>
            {staffList?.map((s) => (
              <tr style={{ borderBottom: '1px solid var(--border)' }} key={s.id}>
                <td style={{ padding: '1.25rem 1.5rem' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
                    <div style={{ width: '32px', height: '32px', borderRadius: '50%', backgroundColor: '#e2e8f0', display: 'flex', alignItems: 'center', justifyContent: 'center', fontWeight: 600, color: '#475569' }}>
                      {s.full_name?.charAt(0) || '?'}
                    </div>
                    <div>
                      <div style={{ fontWeight: 600, fontSize: '0.875rem' }}>{s.full_name || 'Pending Invite'}</div>
                      <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)', display: 'flex', alignItems: 'center', gap: '0.25rem' }}>
                        <Mail size={12} /> {s.email}
                      </div>
                    </div>
                  </div>
                </td>
                <td style={{ padding: '1.25rem 1.5rem', fontSize: '0.875rem' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '0.375rem' }}>
                    <Shield size={14} style={{ color: 'var(--primary)' }} />
                    {s.role.replace('_', ' ').toUpperCase()}
                  </div>
                </td>
                <td style={{ padding: '1.25rem 1.5rem' }}>
                  <span style={{ 
                    fontSize: '0.75rem', 
                    fontWeight: 600, 
                    padding: '0.25rem 0.5rem', 
                    borderRadius: '1rem',
                    backgroundColor: s.status === 'active' ? '#dcfce7' : '#fef3c7',
                    color: s.status === 'active' ? '#166534' : '#92400e'
                  }}>
                    {s.status.toUpperCase()}
                  </span>
                </td>
                <td style={{ padding: '1.25rem 1.5rem' }}>
                  <div style={{ display: 'flex', gap: '0.25rem', flexWrap: 'wrap' }}>
                    {Object.entries(s.permissions || {})
                      .filter(([_, val]) => val === true)
                      .slice(0, 3)
                      .map(([key]) => (
                        <span key={key} style={{ fontSize: '0.625rem', backgroundColor: '#f1f5f9', padding: '0.125rem 0.375rem', borderRadius: '0.25rem' }}>
                          {key.replace('manage_', '').replace('view_', '')}
                        </span>
                      ))
                    }
                    {Object.values(s.permissions || {}).filter(v => v === true).length > 3 && (
                      <span style={{ fontSize: '0.625rem', color: 'var(--text-muted)' }}>+ more</span>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
};
