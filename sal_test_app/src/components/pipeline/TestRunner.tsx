import React, { useState } from 'react';
import axios from 'axios';
import { useNavigate } from 'react-router-dom';
import { Play } from 'lucide-react';
import { FormSelector } from './FormSelector';
import { AudioUploader } from './AudioUploader';
import { SubjectCreator } from './SubjectCreator';
import { recordings, notes } from '../../services/api';

export const TestRunner: React.FC = () => {
  const [formVersionId, setFormVersionId] = useState('');
  const [subjectId, setSubjectId] = useState('');
  const [audioFile, setAudioFile] = useState<File | null>(null);
  const [loading, setLoading] = useState(false);
  const [progress, setProgress] = useState('');
  const navigate = useNavigate();

  const handleRunTest = async () => {
    if (!formVersionId || !audioFile) {
      alert('Please select a form and upload an audio file.');
      return;
    }

    setLoading(true);
    try {
      // 1. Create recording record
      setProgress('Initializing recording...');
      const { data: recData } = await recordings.create({
        content_type: audioFile.type,
        subject_id: subjectId || undefined,
      });

      // 2. Upload to storage (MinIO/S3)
      setProgress('Uploading audio to storage...');
      await axios.put(recData.upload_url, audioFile, {
        headers: { 'Content-Type': audioFile.type },
      });

      // 3. Confirm upload
      setProgress('Confirming upload...');
      await recordings.confirm(recData.recording.id);

      // 4. Create note (triggers AI extraction)
      setProgress('Triggering AI extraction...');
      const { data: noteData } = await notes.create({
        recording_id: recData.recording.id,
        form_version_id: formVersionId,
        subject_id: subjectId || undefined,
      });

      // Navigate to analysis/results page
      navigate(`/test/results/${noteData.id}`);
    } catch (err) {
      console.error(err);
      alert('Test failed: ' + (err as any).message);
      setLoading(false);
    }
  };

  return (
    <div style={{ maxWidth: '600px', margin: '0 auto' }}>
      <header style={{ marginBottom: '2rem' }}>
        <h1 style={{ fontSize: '1.875rem', fontWeight: 700, marginBottom: '0.5rem' }}>Run AI Extraction Test</h1>
        <p style={{ color: 'var(--text-muted)' }}>Upload a sample recording to measure Salvia's accuracy and performance.</p>
      </header>

      <div className="login-card" style={{ maxWidth: 'none' }}>
        <SubjectCreator value={subjectId} onChange={setSubjectId} />
        <FormSelector value={formVersionId} onChange={setFormVersionId} />
        
        <AudioUploader onFileSelected={setAudioFile} />

        <div style={{ marginTop: '2rem' }}>
          <button 
            onClick={handleRunTest} 
            disabled={loading || !formVersionId || !audioFile}
            style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '0.5rem' }}
          >
            {loading ? (
              <span>{progress}</span>
            ) : (
              <>
                <Play size={20} />
                Start AI Pipeline
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
};
