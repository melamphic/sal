import React, { useRef, useState } from 'react';
import { Upload, FileAudio, X } from 'lucide-react';

interface AudioUploaderProps {
  onFileSelected: (file: File | null) => void;
}

export const AudioUploader: React.FC<AudioUploaderProps> = ({ onFileSelected }) => {
  const [file, setFile] = useState<File | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const selectedFile = e.target.files?.[0] || null;
    setFile(selectedFile);
    onFileSelected(selectedFile);
  };

  const clearFile = () => {
    setFile(null);
    onFileSelected(null);
    if (inputRef.current) inputRef.current.value = '';
  };

  return (
    <div className="form-group">
      <label>Audio Recording</label>
      {!file ? (
        <div 
          className="upload-dropzone"
          onClick={() => inputRef.current?.click()}
          style={{
            border: '2px dashed var(--border)',
            borderRadius: '0.5rem',
            padding: '2rem',
            textAlign: 'center',
            cursor: 'pointer',
            backgroundColor: '#f8fafc'
          }}
        >
          <Upload size={32} style={{ color: 'var(--text-muted)', marginBottom: '0.5rem' }} />
          <p style={{ margin: 0, fontSize: '0.875rem' }}>Click to upload audio (MP3, M4A, WAV)</p>
          <input 
            type="file" 
            ref={inputRef} 
            onChange={handleFileChange} 
            accept="audio/*" 
            style={{ display: 'none' }} 
          />
        </div>
      ) : (
        <div 
          style={{
            display: 'flex',
            alignItems: 'center',
            padding: '0.75rem',
            border: '1px solid var(--primary)',
            borderRadius: '0.5rem',
            backgroundColor: '#f5f3ff'
          }}
        >
          <FileAudio size={20} style={{ color: 'var(--primary)', marginRight: '0.75rem' }} />
          <div style={{ flex: 1, overflow: 'hidden' }}>
            <div style={{ fontSize: '0.875rem', fontWeight: 500, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
              {file.name}
            </div>
            <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
              {(file.size / 1024 / 1024).toFixed(2)} MB
            </div>
          </div>
          <button 
            onClick={clearFile}
            style={{ width: 'auto', padding: '0.25rem', background: 'transparent', color: 'var(--text-muted)' }}
          >
            <X size={20} />
          </button>
        </div>
      )}
    </div>
  );
};
