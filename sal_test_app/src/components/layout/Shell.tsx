import React from 'react';
import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import { LayoutDashboard, PlayCircle, LogOut, Beaker, Users } from 'lucide-react';
import { useAuth } from '../../hooks/useAuth';

export const Shell: React.FC = () => {
  const { logout } = useAuth();
  const navigate = useNavigate();

  const handleLogout = () => {
    logout();
    navigate('/login');
  };

  return (
    <div style={{ display: 'flex', minHeight: '100vh' }}>
      <aside style={{
        width: '260px',
        backgroundColor: '#1e293b',
        color: 'white',
        display: 'flex',
        flexDirection: 'column',
        padding: '1.5rem 1rem'
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', marginBottom: '2.5rem', padding: '0 0.5rem' }}>
          <Beaker size={28} style={{ color: '#818cf8' }} />
          <span style={{ fontSize: '1.25rem', fontWeight: 700, letterSpacing: '-0.025em' }}>SAL Test Hub</span>
        </div>

        <nav style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
          <NavLink 
            to="/" 
            style={({ isActive }) => ({
              display: 'flex',
              alignItems: 'center',
              gap: '0.75rem',
              padding: '0.75rem 1rem',
              borderRadius: '0.5rem',
              textDecoration: 'none',
              color: isActive ? 'white' : '#94a3b8',
              backgroundColor: isActive ? '#334155' : 'transparent',
              fontSize: '0.875rem',
              fontWeight: 500,
              transition: 'all 0.2s'
            })}
          >
            <LayoutDashboard size={20} />
            Dashboard
          </NavLink>
          <NavLink 
            to="/test/new" 
            style={({ isActive }) => ({
              display: 'flex',
              alignItems: 'center',
              gap: '0.75rem',
              padding: '0.75rem 1rem',
              borderRadius: '0.5rem',
              textDecoration: 'none',
              color: isActive ? 'white' : '#94a3b8',
              backgroundColor: isActive ? '#334155' : 'transparent',
              fontSize: '0.875rem',
              fontWeight: 500,
              transition: 'all 0.2s'
            })}
          >
            <PlayCircle size={20} />
            New AI Test
          </NavLink>
          <NavLink 
            to="/staff" 
            style={({ isActive }) => ({
              display: 'flex',
              alignItems: 'center',
              gap: '0.75rem',
              padding: '0.75rem 1rem',
              borderRadius: '0.5rem',
              textDecoration: 'none',
              color: isActive ? 'white' : '#94a3b8',
              backgroundColor: isActive ? '#334155' : 'transparent',
              fontSize: '0.875rem',
              fontWeight: 500,
              transition: 'all 0.2s'
            })}
          >
            <Users size={20} />
            Staff & Permissions
          </NavLink>
        </nav>

        <button 
          onClick={handleLogout}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '0.75rem',
            padding: '0.75rem 1rem',
            borderRadius: '0.5rem',
            backgroundColor: 'transparent',
            border: 'none',
            color: '#94a3b8',
            cursor: 'pointer',
            fontSize: '0.875rem',
            fontWeight: 500,
            textAlign: 'left',
            marginTop: 'auto'
          }}
        >
          <LogOut size={20} />
          Sign Out
        </button>
      </aside>

      <main style={{ flex: 1, backgroundColor: '#f8fafc', padding: '2rem', overflowY: 'auto' }}>
        <div style={{ maxWidth: '1200px', margin: '0 auto' }}>
          <Outlet />
        </div>
      </main>
    </div>
  );
};
