import React, { useState, useEffect } from 'react';
import { useSearchParams, useNavigate, Link } from 'react-router-dom';
import { useAuth } from '../../hooks/useAuth';

export const LoginPage: React.FC = () => {
  const [email, setEmail] = useState('');
  const [loading, setLoading] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [message, setMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const { login, verify } = useAuth();

  useEffect(() => {
    const token = searchParams.get('token');
    const registered = searchParams.get('registered');
    
    if (registered) {
      setMessage({ type: 'success', text: 'Clinic created! Check your email for a magic link to log in.' });
    }

    if (token) {
      setVerifying(true);
      verify(token)
        .then(() => {
          navigate('/');
        })
        .catch((_err) => {
          setMessage({ type: 'error', text: 'Invalid or expired token.' });
        })
        .finally(() => {
          setVerifying(false);
        });
    }
  }, [searchParams, verify, navigate]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setMessage(null);
    try {
      await login(email);
      setMessage({ type: 'success', text: 'Check your email for the magic link!' });
    } catch (err) {
      console.error(err);
      setMessage({ type: 'error', text: 'Failed to request magic link. Try again.' });
    } finally {
      setLoading(false);
    }
  };

  if (verifying) {
    return (
      <div className="login-container">
        <div className="login-card">
          <h1>Verifying...</h1>
          <p>Please wait while we log you in.</p>
        </div>
      </div>
    );
  }

  return (
    <div className="login-container">
      <div className="login-card">
        <h1>Salvia Test Hub</h1>
        <p>Sign in to your clinic test bench.</p>
        <form onSubmit={handleSubmit}>
          <div className="form-group">
            <label htmlFor="email">Email Address</label>
            <input
              id="email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="name@clinic.com"
              required
            />
          </div>
          <button type="submit" disabled={loading}>
            {loading ? 'Sending...' : 'Send Magic Link'}
          </button>
        </form>
        {message && (
          <div className={`message ${message.type}`} style={{ marginTop: '1.5rem' }}>
            {message.text}
          </div>
        )}

        <div style={{ marginTop: '2rem', textAlign: 'center', borderTop: '1px solid var(--border)', paddingTop: '1.5rem' }}>
          <p style={{ fontSize: '0.875rem', color: 'var(--text-muted)' }}>
            Need to test a new clinic? <br />
            <Link to="/register" style={{ color: 'var(--primary)', fontWeight: 600 }}>Create a Clinic Account</Link>
          </p>
        </div>
      </div>
    </div>
  );
};
