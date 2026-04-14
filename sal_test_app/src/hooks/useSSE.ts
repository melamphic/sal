import { useEffect, useState } from 'react';
import type { SSEEvent } from '../types';

export const useSSE = () => {
  const [lastEvent, setLastEvent] = useState<SSEEvent | null>(null);

  useEffect(() => {
    const token = localStorage.getItem('access_token');
    if (!token) return;

    const url = `http://localhost:8080/api/v1/events?token=${token}`;
    const es = new EventSource(url);

    es.onmessage = (e) => {
      try {
        const data: SSEEvent = JSON.parse(e.data);
        setLastEvent(data);
      } catch (err) {
        console.error('SSE parse error:', err);
      }
    };

    es.onerror = (e) => {
      console.error('SSE connection error:', e);
    };

    return () => es.close();
  }, []);

  return lastEvent;
};
