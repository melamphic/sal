# Deterministic Confidence Scoring — Design Research

How to replace AI-estimated confidence floats with scores grounded in Deepgram word-level ASR data.

---

## The Problem

The LLM returns a `source_quote` (verbatim text from the transcript) plus a `confidence` float (0.0–1.0) for each extracted field. That LLM-estimated confidence is unreliable — it is a self-report from the model, not a measurement. Token log-probability–based scores have AUROC 0.71–0.87 vs 0.51–0.70 for self-reported confidence on medical questions (PMC12396779).

Deepgram's word-level response gives us a real signal: acoustic model confidence per word, grounded in the audio. The goal is to align the `source_quote` back to those words and aggregate their scores.

---

## Pipeline

```
Deepgram words[] → assemble transcript string + char-offset index
                                      ↓
LLM returns {field_id, value, source_quote, llm_confidence (discard)}
                                      ↓
QuoteAligner:
  1. Normalize both strings (lowercase, strip punct, collapse whitespace)
  2. Exact match via str.find() → char interval          [score = 1.0]
  3. Fallback: rapidfuzz.partial_ratio_alignment() ≥ 0.85 [score = ratio]
  4. If best score < 0.60 → mark as ungrounded           [score = 0.0]
                                      ↓
WordConfidenceExtractor:
  Given char interval → collect words[] entries whose spans overlap
                                      ↓
ConfidenceAggregator:
  - mean_confidence     (primary display score)
  - min_word_confidence (review trigger — one bad word tanks a drug name)
  - alignment_score     (how well the quote matched the transcript)
  - grounding_source:   "exact" | "fuzzy" | "ungrounded"
  - requires_review:    bool
```

---

## 1. Quote-to-Words Alignment

### Algorithm

**Phase 1 — exact match (fast path):**
Normalize transcript + quote → `str.find()`. If found, char interval is exact. Alignment score = 1.0.

**Phase 2 — fuzzy fallback:**
`rapidfuzz.partial_ratio_alignment(norm_quote, norm_transcript)` returns `ScoreAlignment` with `.score` (0–100), `.dest_start`, `.dest_end` (char offsets in transcript).
- Accept if `score / 100 ≥ 0.85` — threshold from Google LangExtract production usage.
- `score / 100 < 0.60` → ungrounded; confidence = 0.0.
- Between 0.60–0.85 → partial match; apply paraphrase penalty (see Section 3).

**Why rapidfuzz over difflib:** 35–50× faster (C++ backend). MIT license. `partial_ratio_alignment` gives char offsets directly — no post-processing. LangExtract is actively migrating from difflib to rapidfuzz (Issue #386).

### Normalization (apply to both sides before any comparison)

```
1. unicodedata.normalize("NFKD", text)
2. .lower()
3. strip punctuation: re.sub(r"[^\w\s]", "", text)
4. collapse whitespace: re.sub(r"\s+", " ", text).strip()
```

**Key Deepgram pitfall:** `word` field is always lowercased + no punctuation. `punctuated_word` has capitalization + commas/periods. Assemble the reference transcript from `punctuated_word` (matches what the LLM sees) but normalize before comparison. This resolves the most common mismatch source.

### Character-Offset Index

Build once when Deepgram response arrives:

```go
type IndexedWord struct {
    Word            string
    PunctuatedWord  string
    Start, End      float64  // audio timestamps (seconds)
    Confidence      float64
    CharStart, CharEnd int   // byte offsets in assembled transcript
    Speaker         *int
}

func BuildWordIndex(dgWords []DeepgramWord) (transcript string, index []IndexedWord) {
    var sb strings.Builder
    for i, w := range dgWords {
        text := w.PunctuatedWord
        if text == "" { text = w.Word }
        charStart := sb.Len()
        sb.WriteString(text)
        charEnd := sb.Len()
        index = append(index, IndexedWord{
            Word: w.Word, PunctuatedWord: text,
            Start: w.Start, End: w.End, Confidence: w.Confidence,
            CharStart: charStart, CharEnd: charEnd, Speaker: w.Speaker,
        })
        if i < len(dgWords)-1 { sb.WriteByte(' ') }
    }
    return sb.String(), index
}
```

---

## 2. Deepgram Word Object Schema

Each word in `results.channels[0].alternatives[0].words[]`:

```json
{
  "word": "amoxicillin",
  "start": 12.34,
  "end": 12.89,
  "confidence": 0.8821,
  "punctuated_word": "amoxicillin,",
  "speaker": 0,
  "speaker_confidence": 0.9412
}
```

Field availability:
- `word`, `start`, `end`, `confidence` — always present
- `punctuated_word` — requires `smart_format=true` (we use this)
- `speaker`, `speaker_confidence` — requires `diarize=true` (we use this)

**Overconfidence warning:** End-to-end ASR models (Nova-2, Nova-3) are systemically overconfident. Word confidence correlates with correctness with ROC AUC 0.68–0.87 — useful as a relative signal, not a calibrated probability. A score of 0.95 does not mean 95% chance correct. Treat thresholds as conservative relative cutoffs (arxiv 2503.15124).

**Nova-3 Medical:** 3.44% WER, 6.79% Keyword Error Rate — state of the art. Keyterm Prompting can boost low-frequency veterinary terms (drug names, procedures). No documented effect on confidence score calibration for keyterm-boosted words.

---

## 3. Confidence Aggregation

Once you have `[]float64` word confidences for the matched span:

| Method | Formula | Use for |
|---|---|---|
| Arithmetic mean | `sum(c)/n` | Primary display score |
| Minimum | `min(c)` | Review trigger — one uncertain word in "metronidazole 250mg bid" should flag the whole span |
| Geometric mean | `exp(mean(log(c)))` | Length-invariant; used in conformal prediction work on medical NLP |

**Recommendation (from NVIDIA entropy research + conformal prediction paper):** Use **mean** as the primary `asr_confidence` score. Use **min** as the `requires_review` trigger. Store both.

**Paraphrase penalty** for fuzzy matches (alignment_score 0.60–0.85):
```
adjusted_score = mean_confidence × ((alignment_score - 0.60) / (0.85 - 0.60))
```
This reduces the score proportionally when the LLM didn't quote verbatim.

**Ungrounded** (alignment_score < 0.60):
```
asr_confidence = 0.0, requires_review = true
```
The LLM either hallucinated the quote or paraphrased beyond recognition. The conformal prediction study on medical entity extraction (arxiv 2603.00924) treats ungrounded extractions as requiring mandatory human review.

---

## 4. Thresholds

No universal value is correct — optimal thresholds range 0.55–0.94 across models and domains (arxiv 2503.15124). These are validated starting points:

| Field category | min trigger | mean reject |
|---|---|---|
| Safety-critical (drug name, dose, controlled substance) | 0.80 | 0.85 |
| Clinical (diagnosis, chief complaint, procedure) | 0.72 | 0.78 |
| Physical measurements (weight, temp, HR) | 0.70 | 0.75 |
| Administrative (owner name, pet name, date) | 0.60 | 0.68 |
| Default fallback | 0.70 | 0.75 |

**Architecture:** Per-field thresholds stored on `FieldSpec` (add `MinConfidence float64`). The conformal prediction research explicitly confirms that global thresholds are unreliable; per-category thresholds are necessary for controlled false-discovery rates.

Expose a clinic-level sensitivity dial that shifts all thresholds ±0.05–0.10 rather than exposing raw threshold values to clinic admins.

---

## 5. What Goes in the Database

### Recording table addition (migration required)

```sql
ALTER TABLE recordings ADD COLUMN word_confidences JSONB;
-- [{word, start, end, confidence, punctuated_word, char_start, char_end, speaker}]
-- Populated by TranscribeAudioWorker from Deepgram words[] array.
-- NULL when using GeminiTranscriber (dev/staging — no word-level data).
```

### Note fields (already in domain — verify these columns exist)

```sql
-- Per-extraction field result — already has:
confidence FLOAT     -- currently stores LLM-estimated value
source_quote TEXT    -- already stored

-- Add:
asr_confidence       FLOAT     -- mean of matched word confidences
min_word_confidence  FLOAT     -- minimum word confidence in span
alignment_score      FLOAT     -- quote-to-transcript match quality
grounding_source     TEXT      -- 'exact' | 'fuzzy' | 'ungrounded' | 'no_asr_data'
requires_review      BOOLEAN   -- true if below threshold or ungrounded
```

---

## 6. Prior Art

| Project / Paper | What it does | Relevance |
|---|---|---|
| [Google LangExtract](https://github.com/google/langextract) (Aug 2025) | LLM extraction → char-offset grounding via difflib (migrating to rapidfuzz). `char_interval=None` for ungrounded. | Direct template for alignment layer |
| [NVIDIA NeMo ASR Confidence](https://github.com/NVIDIA-NeMo/NeMo) | Entropy-based word confidence; min aggregation recommended for error detection | Aggregation method justification |
| [arxiv 2503.15124](https://arxiv.org/html/2503.15124v1) | Empirical eval of ASR confidence across providers incl Deepgram; optimal thresholds 0.55–0.94 | Threshold calibration baseline |
| [arxiv 2603.00924](https://arxiv.org/html/2603.00924) | Conformal prediction for medical entity extraction; per-category thresholds; geometric mean of token log-probs | Architecture for threshold design |
| [PMC12396779](https://pmc.ncbi.nlm.nih.gov/articles/PMC12396779/) | Token log-probs AUROC 0.71–0.87 vs self-reported 0.51–0.70 for medical questions | Why we discard LLM self-confidence |
| [VeriFact](https://github.com/philipchung/verifact) | Clinical text factuality verification against EHRs | Pattern for human-review flagging |

---

## 7. Implementation — Status

**Completed (2026-04-15, migration 00017)**

| Step | File | Status |
|---|---|---|
| `word_confidences JSONB` on `recordings` | `migrations/00017_add_deterministic_confidence.sql` | ✅ |
| 5 new columns on `note_fields` | `migrations/00017_add_deterministic_confidence.sql` | ✅ |
| `platform/confidence` package (`BuildWordIndex`, `AlignQuote`, `ComputeFieldConfidence`) | `internal/platform/confidence/confidence.go` | ✅ |
| Deepgram word array → `WordConfidence` index in `TranscriptResult` | `internal/audio/transcriber.go` | ✅ |
| Persist word confidences in `UpdateRecordingTranscript` | `internal/audio/repository.go` | ✅ |
| `GetWordConfidences` repo method | `internal/audio/repository.go` | ✅ |
| `RecordingProvider.GetWordConfidences` interface + adapter | `internal/notes/jobs.go`, `internal/app/app.go` | ✅ |
| `ComputeFieldConfidence` called per field in `ExtractNoteWorker` | `internal/notes/jobs.go` | ✅ |
| New columns persisted via `UpsertNoteFields` | `internal/notes/repository.go` | ✅ |

**Implementation notes vs. original plan:**
- `rapidfuzz` not used — Go implementation with LCS-ratio sliding window is sufficient and adds no dependency.
- `MinConfidence` per-field threshold not added to `FieldSpec` yet — the `requires_review` flag exposes the signal to the frontend; threshold enforcement can be added per-field when the frontend review UI is built.
- `no_asr_data` grounding source (Gemini transcriber path) leaves all ASR columns NULL and keeps the LLM `confidence` value as-is.
