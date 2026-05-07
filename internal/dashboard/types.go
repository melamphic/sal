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
	FetchedAt        time.Time         `json:"fetched_at"`
	TTLSeconds       int               `json:"ttl_seconds"`
	Vertical         domain.Vertical   `json:"vertical"`
	Hero             *HeroMetric       `json:"hero,omitempty"`
	KPIStrip         []KPI             `json:"kpi_strip"`
	VerticalCard     *VerticalCard     `json:"vertical_card,omitempty"`
	Watchcards       []Watchcard       `json:"watchcards"`
	DraftsCount      int               `json:"drafts_count"`
	SeatUsage        SeatUsage         `json:"seat_usage"`
	Activity         []ActivityEvent   `json:"activity"`
	Attention        *AttentionPanel   `json:"attention,omitempty"`
	ComplianceHealth *ComplianceHealth `json:"compliance_health,omitempty"`
	Billing          *BillingStrip     `json:"billing,omitempty"`
}

// AttentionPanel is the "what needs me right now?" section at the top
// of the dashboard. Each item has a count, a label, a tone, and an
// optional CTA href the FE chip uses for click-through. Items with
// count=0 are still emitted so the FE can render an "all clear" tile;
// the FE decides what to hide.
type AttentionPanel struct {
	Items []AttentionItem `json:"items"`
}

// AttentionItem is one chip in the attention panel.
type AttentionItem struct {
	ID      string `json:"id"`           // dotted slug ("approvals.pending")
	Title   string `json:"title"`        // "Witness queue"
	Detail  string `json:"detail"`       // "Oldest waiting 3h" or "All clear"
	Count   int    `json:"count"`
	Tone    string `json:"tone"`         // "danger" | "warn" | "info" | "ok"
	CTAHref string `json:"cta_href,omitempty"`
	Icon    string `json:"icon,omitempty"` // FE-side icon-name slug
}

// ComplianceHealth is the regulator-readiness row. Each metric has a
// label, a numeric value (0..100 percent unless `value_kind=count`),
// a tone, and optional supporting text.
type ComplianceHealth struct {
	Metrics []HealthMetric `json:"metrics"`
}

// HealthMetric is one tile in the compliance-health row.
type HealthMetric struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Value     string `json:"value"`             // pre-formatted ("98%", "12 / 14", "3h")
	Detail    string `json:"detail,omitempty"`  // small hint under the value
	ValueKind string `json:"value_kind"`        // "percent" | "count" | "duration" | "ratio"
	Tone      string `json:"tone"`              // "ok" | "warn" | "danger" | "info"
	// Pct is the 0..100 percentage when ValueKind=percent — the FE
	// uses it to fill a horizontal progress bar without re-parsing
	// the formatted string.
	Pct *float64 `json:"pct,omitempty"`
}

// BillingStrip is the small plan-and-billing summary at the dashboard
// top. Drives a horizontal strip showing trial countdown / plan
// tier / seat utilization.
type BillingStrip struct {
	PlanLabel       string  `json:"plan_label"`               // "Practice trial" | "Pro"
	TrialDaysLeft   *int    `json:"trial_days_left,omitempty"` // nil when not on trial
	SeatsUsed       int     `json:"seats_used"`
	SeatsCap        int     `json:"seats_cap"`
	SeatPct         float64 `json:"seat_pct"`                  // 0..100
	NextInvoiceHint string  `json:"next_invoice_hint,omitempty"` // "$49 on 12 May"; "" when unknown
	Tone            string  `json:"tone"`                       // "ok" | "warn" | "danger"
	CTAHref         string  `json:"cta_href,omitempty"`         // "/settings/billing"
}

// HeroMetric powers the headline card at the top of the dashboard —
// a big number, a one-line label, a 7-day sparkline series, and an
// optional delta-vs-last-week chip. Vertical-aware: vet headline is
// notes signed; aged-care is open incidents; etc.
//
// Series carries one integer per day, oldest first, length=7. Empty
// series renders the card without a sparkline (still readable).
type HeroMetric struct {
	Label       string  `json:"label"`         // "Notes signed this week"
	Value       int     `json:"value"`         // 142
	Suffix      string  `json:"suffix,omitempty"` // "" or "%" or "patients"
	Series      []int   `json:"series"`        // 7 daily counts, Mon..Sun
	DeltaPct    float64 `json:"delta_pct,omitempty"`
	DeltaDir    string  `json:"delta_dir,omitempty"` // up | down | flat
	SubLabel    string  `json:"sub_label,omitempty"` // "vs last 7 days"
	Tone        string  `json:"tone,omitempty"`      // "ok" | "warn" | "info"
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
