import React, { createContext, useContext, useState, useEffect } from 'react';
import { jwtDecode } from 'jwt-decode';
import type { TokenPair, User } from '../types';
import { auth as authApi } from '../services/api';

interface AuthContextType {
  user: User | null;
  loading: boolean;
  login: (email: string) => Promise<void>;
  verify: (token: string) => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export const AuthProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const token = localStorage.getItem('access_token');
    if (token) {
      try {
        const decoded: any = jwtDecode(token);
        setUser({
          id: decoded.staff_id || decoded.sub,
          email: '', // Not in JWT by default, can be added to state if needed
          full_name: '',
          role: decoded.role,
          clinic_id: decoded.clinic_id,
        });
      } catch (e) {
        localStorage.removeItem('access_token');
      }
    }
    setLoading(false);
  }, []);

  const login = async (email: string) => {
    await authApi.requestMagicLink(email);
  };

  const verify = async (token: string) => {
    const { data } = await authApi.verifyToken(token);
    const tokenPair: TokenPair = data;
    localStorage.setItem('access_token', tokenPair.access_token);
    localStorage.setItem('refresh_token', tokenPair.refresh_token);
    const decoded: any = jwtDecode(tokenPair.access_token);
    setUser({
      id: decoded.staff_id || decoded.sub,
      email: '',
      full_name: '',
      role: decoded.role,
      clinic_id: decoded.clinic_id,
    });
  };

  const logout = () => {
    authApi.logout().finally(() => {
      localStorage.removeItem('access_token');
      localStorage.removeItem('refresh_token');
      setUser(null);
    });
  };

  return (
    <AuthContext.Provider value={{ user, loading, login, verify, logout }}>
      {children}
    </AuthContext.Provider>
  );
};

export const useAuth = () => {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
};
