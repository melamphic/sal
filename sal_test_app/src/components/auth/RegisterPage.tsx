import React, { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { clinic as clinicApi } from '../../services/api';

export const RegisterPage: React.FC = () => {
  const [formData, setFormData] = useState({
    name: '',
    email: '',
    vertical: 'veterinary' as const,
    admin_email: '',
    admin_name: '',
  });
  const [loading, setLoading] = useState(false);
  const [success, setSuccess] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const navigate = useNavigate();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError(null);
    try {
      await clinicApi.register({
        ...formData,
        data_region: 'ap-southeast-2',
      });
      setSuccess(true);
      // Don't auto-redirect — tell them to check email for magic link.
    } catch (err: any) {
      setError(err.response?.data?.title || 'Registration failed. Try again.');
    } finally {
      setLoading(false);
    }
  };

  if (success) {
    return (
      <div className="login-container">
        <div className="login-card" style={{ maxWidth: '450px', textAlign: 'center' }}>
          <h1>Check your email</h1>
          <p>
            Clinic created! A magic link has been sent to <strong>{formData.admin_email}</strong>.
            Click the link to log in as super admin.
          </p>
          <p style={{ marginTop: '1rem', fontSize: '0.875rem' }}>
            <Link to="/login" style={{ color: 'var(--primary)', fontWeight: 600 }}>Go to login</Link>
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="login-container">
      <div className="login-card" style={{ maxWidth: '450px' }}>
        <h1>Create Clinic</h1>
        <p>Register your clinic to start using Salvia AI.</p>

        <form onSubmit={handleSubmit}>
          <div className="form-group">
            <label>Clinic Name</label>
            <input
              type="text"
              value={formData.name}
              onChange={(e) => setFormData({ ...formData, name: e.target.value })}
              placeholder="e.g. Southside Veterinary Hospital"
              required
            />
          </div>

          <div className="form-group">
            <label>Clinic Contact Email</label>
            <input
              type="email"
              value={formData.email}
              onChange={(e) => setFormData({ ...formData, email: e.target.value })}
              placeholder="clinic@example.com"
              required
            />
          </div>

          <div className="form-group">
            <label>Admin Full Name</label>
            <input
              type="text"
              value={formData.admin_name}
              onChange={(e) => setFormData({ ...formData, admin_name: e.target.value })}
              placeholder="Dr. Jane Smith"
              required
            />
          </div>

          <div className="form-group">
            <label>Admin Email</label>
            <input
              type="email"
              value={formData.admin_email}
              onChange={(e) => setFormData({ ...formData, admin_email: e.target.value })}
              placeholder="admin@clinic.com"
              required
            />
          </div>

          <div className="form-group">
            <label>Clinical Vertical</label>
            <select
              value={formData.vertical}
              onChange={(e) => setFormData({ ...formData, vertical: e.target.value as any })}
              className="form-select"
              style={{ width: '100%', padding: '0.75rem', borderRadius: '0.5rem', border: '1px solid var(--border)' }}
            >
              <option value="veterinary">Veterinary</option>
              <option value="dental">Dental</option>
              <option value="aged_care">Aged Care</option>
            </select>
          </div>

          <button type="submit" disabled={loading}>
            {loading ? 'Creating Account...' : 'Register Clinic'}
          </button>
        </form>

        {error && <div className="message error" style={{ marginTop: '1rem' }}>{error}</div>}

        <p style={{ marginTop: '1.5rem', fontSize: '0.875rem' }}>
          Already have an account? <Link to="/login" style={{ color: 'var(--primary)', fontWeight: 600 }}>Log In</Link>
        </p>
      </div>
    </div>
  );
};
