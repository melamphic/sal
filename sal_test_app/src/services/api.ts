import axios from 'axios';
import type { TokenPair } from '../types';

const BASE_URL = 'http://localhost:8080/api/v1';

const api = axios.create({
  baseURL: BASE_URL,
});

api.interceptors.request.use((config) => {
  const token = localStorage.getItem('access_token');
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

api.interceptors.response.use(
  (response) => response,
  async (error) => {
    const originalRequest = error.config;
    if (error.response?.status === 401 && !originalRequest._retry) {
      originalRequest._retry = true;
      const refreshToken = localStorage.getItem('refresh_token');
      if (refreshToken) {
        try {
          const { data } = await axios.post<TokenPair>(`${BASE_URL}/auth/refresh`, {
            refresh_token: refreshToken,
          });
          localStorage.setItem('access_token', data.access_token);
          localStorage.setItem('refresh_token', data.refresh_token);
          api.defaults.headers.common['Authorization'] = `Bearer ${data.access_token}`;
          return api(originalRequest);
        } catch (refreshError) {
          localStorage.removeItem('access_token');
          localStorage.removeItem('refresh_token');
          window.location.href = '/login';
        }
      }
    }
    return Promise.reject(error);
  }
);

export const auth = {
  requestMagicLink: (email: string) => api.post('/auth/magic-link', { email }),
  verifyToken: (token: string) => api.get<TokenPair>(`/auth/verify?token=${token}`),
  logout: () => api.post('/auth/logout'),
};

export const forms = {
  list: () => api.get('/forms'),
  get: (id: string) => api.get(`/forms/${id}`),
};

export const recordings = {
  create: (data: { subject_id?: string; content_type: string }) =>
    api.post('/recordings', data),
  confirm: (id: string) => api.post(`/recordings/${id}/confirm-upload`),
  get: (id: string) => api.get(`/recordings/${id}`),
};

export const notes = {
  create: (data: {
    recording_id?: string;
    form_version_id: string;
    subject_id?: string;
    skip_extraction?: boolean;
  }) => api.post('/notes', data),
  get: (id: string) => api.get(`/notes/${id}`),
  list: (params?: any) => api.get('/notes', { params }),
  updateField: (noteId: string, fieldId: string, value: string | null) =>
    api.patch(`/notes/${noteId}/fields/${fieldId}`, { value }),
};

export const clinic = {
  register: (data: {
    name: string;
    email: string;
    vertical: 'veterinary' | 'dental' | 'aged_care';
    data_region?: string;
    admin_email: string;
    admin_name: string;
  }) => api.post('/clinic/register', data),
  get: () => api.get('/clinic'),
};

export const staff = {
  invite: (data: {
    email: string;
    full_name: string;
    role: string;
    note_tier: string;
    permissions: any;
  }) => api.post('/staff/invite', data),
  list: () => api.get('/staff'),
};

export const subjects = {
  list: () => api.get('/patients'),
  create: (data: { name: string; species?: string; breed?: string; sex?: string }) => 
    api.post('/patients', data),
};

export default api;
