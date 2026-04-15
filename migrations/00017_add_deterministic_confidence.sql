-- +goose Up
-- +goose StatementBegin

-- Store Deepgram word-level confidence data alongside the transcript.
-- NULL when using GeminiTranscriber (dev/staging) — no word-level data available.
ALTER TABLE recordings
    ADD COLUMN word_confidences JSONB;

-- Deterministic confidence scoring columns on note_fields.
-- asr_confidence:      mean ASR word confidence for the matched source_quote span (0.0–1.0).
-- min_word_confidence: minimum word confidence in span — review trigger.
-- alignment_score:     how well source_quote matched transcript (1.0=exact, 0.0=no match).
-- grounding_source:    'exact' | 'fuzzy' | 'ungrounded' | 'no_asr_data'
-- requires_review:     true when grounding_source='ungrounded' or score below field threshold.
ALTER TABLE note_fields
    ADD COLUMN asr_confidence      DECIMAL(5,4) CHECK (asr_confidence >= 0 AND asr_confidence <= 1),
    ADD COLUMN min_word_confidence DECIMAL(5,4) CHECK (min_word_confidence >= 0 AND min_word_confidence <= 1),
    ADD COLUMN alignment_score     DECIMAL(5,4) CHECK (alignment_score >= 0 AND alignment_score <= 1),
    ADD COLUMN grounding_source    VARCHAR(20)  CHECK (grounding_source IN ('exact','fuzzy','ungrounded','no_asr_data')),
    ADD COLUMN requires_review     BOOLEAN      NOT NULL DEFAULT FALSE;

-- Fast lookup for the review queue.
CREATE INDEX idx_note_fields_review ON note_fields(note_id) WHERE requires_review = TRUE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_note_fields_review;

ALTER TABLE note_fields
    DROP COLUMN IF EXISTS requires_review,
    DROP COLUMN IF EXISTS grounding_source,
    DROP COLUMN IF EXISTS alignment_score,
    DROP COLUMN IF EXISTS min_word_confidence,
    DROP COLUMN IF EXISTS asr_confidence;

ALTER TABLE recordings
    DROP COLUMN IF EXISTS word_confidences;

-- +goose StatementEnd
