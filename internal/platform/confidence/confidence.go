// Package confidence provides deterministic confidence scoring for AI-extracted
// form fields by grounding the LLM's source_quote back to ASR word-level data.
//
// Pipeline:
//
//	Deepgram words[] → BuildWordIndex → []WordConfidence
//	LLM source_quote + []WordConfidence → ComputeFieldConfidence → FieldConfidenceResult
//
// References: docs/deterministic_confidence.md
package confidence

import (
	"strings"
	"unicode"
)

// WordConfidence is a single ASR word with its acoustic confidence score and
// character offset within the assembled transcript string.
type WordConfidence struct {
	Word           string  // lowercased, stripped form returned by ASR
	PunctuatedWord string  // original form with punctuation and casing
	Start          float64 // audio timestamp seconds
	End            float64 // audio timestamp seconds
	Confidence     float64 // acoustic model score 0.0–1.0
	CharStart      int     // byte offset in assembled transcript (PunctuatedWord-based)
	CharEnd        int     // byte offset in assembled transcript
	Speaker        *int    // diarization speaker index; nil if not diarized
}

// RawWord is the provider-agnostic input for BuildWordIndex.
// Map the ASR provider's word type to this before calling BuildWordIndex.
type RawWord struct {
	Word           string
	PunctuatedWord string
	Start          float64
	End            float64
	Confidence     float64
	Speaker        *int
}

// FieldConfidenceResult is the output of ComputeFieldConfidence for one field.
type FieldConfidenceResult struct {
	ASRConfidence     float64 // mean word confidence for matched span (0.0 when ungrounded)
	MinWordConfidence float64 // minimum word confidence in span (review trigger)
	AlignmentScore    float64 // 1.0=exact, <1.0=fuzzy, 0.0=no match
	GroundingSource   string  // "exact" | "fuzzy" | "ungrounded" | "no_asr_data"
}

// BuildWordIndex assembles the []WordConfidence index from ASR words.
// Uses PunctuatedWord when non-empty, falling back to Word.
// The CharStart/CharEnd fields are byte offsets in the assembled PunctuatedWord transcript.
func BuildWordIndex(words []RawWord) []WordConfidence {
	index := make([]WordConfidence, 0, len(words))
	pos := 0
	for i, w := range words {
		text := w.PunctuatedWord
		if text == "" {
			text = w.Word
		}
		index = append(index, WordConfidence{
			Word:           w.Word,
			PunctuatedWord: text,
			Start:          w.Start,
			End:            w.End,
			Confidence:     w.Confidence,
			CharStart:      pos,
			CharEnd:        pos + len(text),
			Speaker:        w.Speaker,
		})
		pos += len(text)
		if i < len(words)-1 {
			pos++ // space separator between words
		}
	}
	return index
}

// ComputeFieldConfidence runs the full alignment + aggregation pipeline for one field.
//
// sourceQuote is the verbatim text the LLM cited from the transcript.
// transformationType is "direct" or "inference" — "inference" applies a 0.85 penalty
// because the LLM derived the value rather than quoting it verbatim.
//
// Pass nil index (Gemini transcriber, no ASR data) to get GroundingSource="no_asr_data".
func ComputeFieldConfidence(sourceQuote, transformationType string, index []WordConfidence) FieldConfidenceResult {
	if len(index) == 0 {
		return FieldConfidenceResult{GroundingSource: "no_asr_data"}
	}
	if sourceQuote == "" {
		return FieldConfidenceResult{GroundingSource: "ungrounded"}
	}

	matched, score, source := alignQuote(sourceQuote, index)
	if source == "ungrounded" || len(matched) == 0 {
		return FieldConfidenceResult{
			AlignmentScore:  score,
			GroundingSource: "ungrounded",
		}
	}

	mean, minVal := aggregateConfidences(matched)

	// Fuzzy penalty: scale linearly from 0 at ungroundedThreshold to 1 at fuzzyThreshold.
	const (
		ungroundedThreshold = 0.60
		fuzzyThreshold      = 0.85
	)
	if score < fuzzyThreshold {
		penalty := (score - ungroundedThreshold) / (fuzzyThreshold - ungroundedThreshold)
		mean *= penalty
		minVal *= penalty
	}

	// Inference penalty: two error sources (ASR mis-transcription + AI reasoning).
	if transformationType == "inference" {
		const inferencePenalty = 0.85
		mean *= inferencePenalty
		minVal *= inferencePenalty
	}

	return FieldConfidenceResult{
		ASRConfidence:     mean,
		MinWordConfidence: minVal,
		AlignmentScore:    score,
		GroundingSource:   source,
	}
}

// ── Internal alignment ────────────────────────────────────────────────────────

// alignQuote locates sourceQuote in the word index using exact string match first,
// then a sliding-window LCS-ratio fuzzy fallback.
// Returns (matched words, alignment score, grounding source).
func alignQuote(sourceQuote string, index []WordConfidence) ([]WordConfidence, float64, string) {
	// Build normalized transcript from word index — consistent char positions.
	var sb strings.Builder
	normStarts := make([]int, len(index))
	normEnds := make([]int, len(index))
	for i, w := range index {
		norm := normalizeToken(w.Word)
		normStarts[i] = sb.Len()
		sb.WriteString(norm)
		normEnds[i] = sb.Len()
		if i < len(index)-1 {
			sb.WriteByte(' ')
		}
	}
	normTranscript := sb.String()
	normQuote := normalizeText(sourceQuote)

	if normQuote == "" {
		return nil, 0.0, "ungrounded"
	}

	// Phase 1: exact match on normalized text.
	if idx := strings.Index(normTranscript, normQuote); idx != -1 {
		matched := wordsInNormRange(index, normStarts, normEnds, idx, idx+len(normQuote))
		return matched, 1.0, "exact"
	}

	// Phase 2: sliding-window fuzzy match via LCS ratio.
	qLen := len(normQuote)
	windowSize := qLen + 40 // 40-char padding handles minor punctuation/filler differences
	step := qLen / 4
	if step < 6 {
		step = 6
	}

	bestScore := 0.0
	bestStart, bestEnd := 0, 0
	for start := 0; start < len(normTranscript); start += step {
		end := start + windowSize
		if end > len(normTranscript) {
			end = len(normTranscript)
		}
		score := lcsRatio(normQuote, normTranscript[start:end])
		if score > bestScore {
			bestScore = score
			bestStart = start
			bestEnd = end
		}
		if end == len(normTranscript) {
			break
		}
	}

	const ungroundedThreshold = 0.60
	if bestScore < ungroundedThreshold {
		return nil, bestScore, "ungrounded"
	}

	matched := wordsInNormRange(index, normStarts, normEnds, bestStart, bestEnd)
	return matched, bestScore, "fuzzy"
}

// wordsInNormRange returns words whose normalized char spans overlap [start, end).
func wordsInNormRange(index []WordConfidence, normStarts, normEnds []int, start, end int) []WordConfidence {
	var result []WordConfidence
	for i, w := range index {
		if normStarts[i] < end && normEnds[i] > start {
			result = append(result, w)
		}
	}
	return result
}

// aggregateConfidences returns mean and minimum confidence for a word slice.
func aggregateConfidences(words []WordConfidence) (mean, min float64) {
	if len(words) == 0 {
		return 0.0, 0.0
	}
	min = 1.0
	sum := 0.0
	for _, w := range words {
		sum += w.Confidence
		if w.Confidence < min {
			min = w.Confidence
		}
	}
	return sum / float64(len(words)), min
}

// normalizeText lowercases and strips non-alphanumeric characters, collapsing whitespace.
// Applied to source_quote before comparison.
func normalizeText(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	prevSpace := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
			prevSpace = false
		} else if !prevSpace && sb.Len() > 0 {
			sb.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimRight(sb.String(), " ")
}

// normalizeToken normalizes a single ASR word (Deepgram `word` field is already
// lowercased and stripped, but this handles edge cases).
func normalizeToken(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// lcsRatio computes 2*LCS_length / (|a|+|b|) — a character-level Dice coefficient.
// Used for fuzzy quote-to-transcript alignment. O(|a| * |b|) with 1-row rolling DP.
func lcsRatio(a, b string) float64 {
	la, lb := len(a), len(b)
	if la == 0 || lb == 0 {
		return 0.0
	}
	dp := make([]int, lb+1)
	best := 0
	for i := 1; i <= la; i++ {
		prev := 0
		for j := 1; j <= lb; j++ {
			tmp := dp[j]
			if a[i-1] == b[j-1] {
				dp[j] = prev + 1
				if dp[j] > best {
					best = dp[j]
				}
			} else {
				dp[j] = 0
			}
			prev = tmp
		}
	}
	return 2.0 * float64(best) / float64(la+lb)
}
