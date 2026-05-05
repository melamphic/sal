package dashboard

import (
	"time"

	"github.com/melamphic/sal/internal/domain"
)

// Snapshot is the single payload the dashboard endpoint returns. One
// JSON round-trip drives every widget on the home page; the cache
// stores it pre-serialised so cache hits skip JSON marshalling.
//
// FetchedAt + TTLSeconds let the client schedule its next refresh
// without polling — the auto-refresh tick is `TTLSeconds` after
// `FetchedAt`, plus immediate invalidation on local actions.
type Snapshot struct {
	FetchedAt   time.Time       `json:"fetched_at"`
	TTLSeconds  int             `json:"ttl_seconds"`
	Vertical    domain.Vertical `json:"vertical"`
	KPIStrip    []KPI           `json:"kpi_strip"`
	VerticalCard *VerticalCard  `json:"vertical_card,omitempty"`
	Watchcards  []Watchcard     `json:"watchcards"`
	DraftsCount int             `json:"drafts_count"`
	SeatUsage   SeatUsage       `json:"seat_usage"`
	Activity    []ActivityEvent `json:"activity"`
}

// KPI is one tile in the four-tile strip at the top of the vertical
// section. Trend is optional — when zero we don't render the delta
// chip, when set we render "+N%" or "−N%" with green/red tone.
type KPI struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	Value      string  `json:"value"`        // pre-formatted ("24", "$1,234", "75%")
	NumericValue *int  `json:"numeric_value,omitempty"` // for animated counter, when value is plain integer
	Hint       string  `json:"hint,omitempty"` // e.g. "vs yesterday"
	TrendPct   float64 `json:"trend_pct,omitempty"`
	TrendDir   string  `json:"trend_dir,omitempty"` // "up" | "down" | "flat"
	Tone       string  `json:"tone,omitempty"`      // "ok" | "warn" | "danger" | "info" — drives tile colour
}

// VerticalCard is the bigger action card under the KPI strip — content
// shape varies per vertical. The Items slice is rendered as a list,
// optionally with an avatar / status pill per row.
type VerticalCard struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Subtitle string         `json:"subtitle,omitempty"`
	Empty    string         `json:"empty,omitempty"` // rendered when Items is empty
	Items    []VerticalItem `json:"items"`
}

// VerticalItem is one row in a VerticalCard. Loose shape so each
// vertical can repurpose the fields (e.g. vet uses Avatar+Title+Pill
// for surgical patients; aged-care uses Title+Subtitle+Pill for MAR
// rounds).
type VerticalItem struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Pill     string `json:"pill,omitempty"`
	PillTone string `json:"pill_tone,omitempty"` // "ok" | "warn" | "danger" | "info" | "muted"
	Hint     string `json:"hint,omitempty"`
}

// Watchcard mirrors the Flutter WatchcardStack's existing shape —
// reproduced here so the dashboard endpoint becomes the single source
// of truth (the frontend can keep deriving these locally too, but
// surfacing them centrally lets the cache cover them).
type Watchcard struct {
	Kind     string `json:"kind"`              // "note_cap_warning" | "trial_ending" | "compliance_incomplete" | "marketplace_updates" | "ai_seat_full"
	Severity string `json:"severity"`          // "info" | "warn" | "danger"
	Title    string `json:"title"`
	Body     string `json:"body"`
	CTA      string `json:"cta,omitempty"`     // button label
	CTAHref  string `json:"cta_href,omitempty"` // route target
}

// SeatUsage is the AI-seat counter shown in the dashboard header
// (model B). Cap=0 disables the meter (test/local).
type SeatUsage struct {
	Used int `json:"used"`
	Cap  int `json:"cap"`
}

// ActivityEvent is one row of the recent-clinic-activity feed. Generic
// kind+summary pair — frontend renders a different icon per kind.
type ActivityEvent struct {
	Kind      string    `json:"kind"`              // "note_signed" | "drug_op" | "incident_logged" | "consent_captured"
	When      time.Time `json:"when"`
	Summary   string    `json:"summary"`
	ActorName string    `json:"actor_name,omitempty"`
	Tone      string    `json:"tone,omitempty"`
}
