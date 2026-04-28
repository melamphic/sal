package aigen

import (
	"context"
	"log/slog"
	"time"
)

// GenerationLog is the structured record emitted per generation request.
// Logged via slog so existing log pipelines / OTel exporters pick it up
// without bespoke instrumentation. PII is NEVER logged — only IDs and
// non-content metrics.
type GenerationLog struct {
	ClinicID         string        `json:"clinic_id"`
	StaffID          string        `json:"staff_id"`
	Vertical         string        `json:"vertical"`
	Country          string        `json:"country"`
	Kind             string        `json:"kind"` // "form" | "policy"
	Provider         string        `json:"provider"`
	Model            string        `json:"model"`
	PromptHash       string        `json:"prompt_hash"`
	LatencyMS        int64         `json:"latency_ms"`
	ValidationErrors []string      `json:"validation_errors,omitempty"` // codes only, never messages
	Repairs          []string      `json:"repairs,omitempty"`           // actions only, never details with content
	RetryCount       int           `json:"retry_count"`
	Outcome          string        `json:"outcome"` // "success" | "validation_failed" | "provider_error" | "cancelled"
	OutcomeReason    string        `json:"outcome_reason,omitempty"`
	StartedAt        time.Time     `json:"started_at"`
	Duration         time.Duration `json:"-"` // computed from LatencyMS in handlers
}

// Emit writes the GenerationLog to the provided logger. The default logger is
// used if l is nil.
//
// Use slog.Group + structured attrs so the JSON logger renders cleanly without
// nested escaping. Numeric/string values only — no nested objects beyond
// strings — to make this trivial to ingest into Loki / BigQuery / Athena.
func (g GenerationLog) Emit(l *slog.Logger) {
	if l == nil {
		l = slog.Default()
	}
	level := slog.LevelInfo
	if g.Outcome != "success" {
		level = slog.LevelWarn
	}
	l.LogAttrs(context.Background(), level, "aigen.generation",
		slog.String("clinic_id", g.ClinicID),
		slog.String("staff_id", g.StaffID),
		slog.String("vertical", g.Vertical),
		slog.String("country", g.Country),
		slog.String("kind", g.Kind),
		slog.String("provider", g.Provider),
		slog.String("model", g.Model),
		slog.String("prompt_hash", g.PromptHash),
		slog.Int64("latency_ms", g.LatencyMS),
		slog.Any("validation_errors", g.ValidationErrors),
		slog.Any("repairs", g.Repairs),
		slog.Int("retry_count", g.RetryCount),
		slog.String("outcome", g.Outcome),
		slog.String("outcome_reason", g.OutcomeReason),
	)
}

// repairActions returns the action names from a slice of RepairLogEntry,
// stripping the Details field (which may include AI-output snippets).
func repairActions(entries []RepairLogEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Action
	}
	return out
}
