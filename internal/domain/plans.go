package domain

// Plans — billing plan registry.
//
// Source of truth: pricing-model-v3.md (locked 2026-04-18). Numbers below
// must stay in sync with that doc. Public marketing says "Unlimited
// notes"; NoteCap here is the internal soft-cap used for ops alerts only
// (see pricing v3 §7) — never surface to customers.
//
// Plan codes are stable strings of the form
// `{product}_{tier}_{cycle}` e.g. `paws_practice_monthly`. Stored on
// clinics.plan_code. Stripe price ids are looked up from PlanCode at
// checkout time — we do not store them here so they can rotate without
// a code release.

// PlanProduct identifies which Salvia product a plan belongs to.
type PlanProduct string

const (
	PlanProductPaws   PlanProduct = "paws"   // Veterinary
	PlanProductSmile  PlanProduct = "smile"  // Dental
	PlanProductClinic PlanProduct = "clinic" // General primary care
)

// PlanTier identifies the seat-band a plan covers.
type PlanTier string

const (
	PlanTierPractice   PlanTier = "practice"   // 1–3 standard clinicians
	PlanTierPro        PlanTier = "pro"        // 4–7 standard clinicians
	PlanTierEnterprise PlanTier = "enterprise" // 8+ — Contact Sales, no self-serve
)

// PlanCycle identifies the billing cadence.
type PlanCycle string

const (
	PlanCycleMonthly PlanCycle = "monthly"
	PlanCycleAnnual  PlanCycle = "annual"
)

// PlanCode is the stable string identifier persisted on clinics.plan_code.
type PlanCode string

const (
	PlanPawsPracticeMonthly PlanCode = "paws_practice_monthly"
	PlanPawsPracticeAnnual  PlanCode = "paws_practice_annual"
	PlanPawsProMonthly      PlanCode = "paws_pro_monthly"
	PlanPawsProAnnual       PlanCode = "paws_pro_annual"

	PlanSmilePracticeMonthly PlanCode = "smile_practice_monthly"
	PlanSmilePracticeAnnual  PlanCode = "smile_practice_annual"
	PlanSmileProMonthly      PlanCode = "smile_pro_monthly"
	PlanSmileProAnnual       PlanCode = "smile_pro_annual"

	PlanClinicPracticeMonthly PlanCode = "clinic_practice_monthly"
	PlanClinicPracticeAnnual  PlanCode = "clinic_practice_annual"
	PlanClinicProMonthly      PlanCode = "clinic_pro_monthly"
	PlanClinicProAnnual       PlanCode = "clinic_pro_annual"
)

// Plan describes a single billable plan.
type Plan struct {
	Code    PlanCode
	Product PlanProduct
	Tier    PlanTier
	Cycle   PlanCycle
	// Vertical the product binds to. Drives clinic.vertical at signup.
	Vertical Vertical
	// PriceUSDCents is the monthly-equivalent USD price displayed to
	// customers in the US locale. Annual plans show the annual amount
	// (= PriceUSDCents * 12 * 0.83) at checkout — Stripe holds the
	// authoritative price, this is only for display fallback and
	// margin/ops calcs.
	PriceUSDCents int
	// NoteCap is the internal monthly note threshold. Used for
	// upgrade-conversation alerts at 80%/110%/150% (pricing v3 §7).
	// Public marketing says "Unlimited" — never expose this number.
	NoteCap int
	// AISeatCap is the maximum number of staff that may have
	// note_tier=standard (i.e. AI / audio recording / extraction
	// access). Practice = 3, Pro = 7. Enforced server-side when the
	// owner promotes a staff member; the UI surfaces the cap and the
	// upgrade CTA when they hit the ceiling. Marketing positioning
	// (mel) calls these "AI seats". Distinct from NoteCap, which is
	// per-month and soft.
	AISeatCap int
}

const annualDiscount = 0.83 // 17% off — pricing v3 §8.

// Plans is the canonical registry. Indexed by PlanCode.
var Plans = map[PlanCode]Plan{
	// ── Salvia Paws (Veterinary) ───────────────────────────────────────────
	PlanPawsPracticeMonthly: {
		Code: PlanPawsPracticeMonthly, Product: PlanProductPaws,
		Tier: PlanTierPractice, Cycle: PlanCycleMonthly,
		Vertical: VerticalVeterinary, PriceUSDCents: 22900, NoteCap: 1500, AISeatCap: 3,
	},
	PlanPawsPracticeAnnual: {
		Code: PlanPawsPracticeAnnual, Product: PlanProductPaws,
		Tier: PlanTierPractice, Cycle: PlanCycleAnnual,
		Vertical: VerticalVeterinary, PriceUSDCents: 22900, NoteCap: 1500, AISeatCap: 3,
	},
	PlanPawsProMonthly: {
		Code: PlanPawsProMonthly, Product: PlanProductPaws,
		Tier: PlanTierPro, Cycle: PlanCycleMonthly,
		Vertical: VerticalVeterinary, PriceUSDCents: 49900, NoteCap: 4000, AISeatCap: 7,
	},
	PlanPawsProAnnual: {
		Code: PlanPawsProAnnual, Product: PlanProductPaws,
		Tier: PlanTierPro, Cycle: PlanCycleAnnual,
		Vertical: VerticalVeterinary, PriceUSDCents: 49900, NoteCap: 4000, AISeatCap: 7,
	},

	// ── Salvia Smile (Dental) ──────────────────────────────────────────────
	PlanSmilePracticeMonthly: {
		Code: PlanSmilePracticeMonthly, Product: PlanProductSmile,
		Tier: PlanTierPractice, Cycle: PlanCycleMonthly,
		Vertical: VerticalDental, PriceUSDCents: 22900, NoteCap: 1200, AISeatCap: 3,
	},
	PlanSmilePracticeAnnual: {
		Code: PlanSmilePracticeAnnual, Product: PlanProductSmile,
		Tier: PlanTierPractice, Cycle: PlanCycleAnnual,
		Vertical: VerticalDental, PriceUSDCents: 22900, NoteCap: 1200, AISeatCap: 3,
	},
	PlanSmileProMonthly: {
		Code: PlanSmileProMonthly, Product: PlanProductSmile,
		Tier: PlanTierPro, Cycle: PlanCycleMonthly,
		Vertical: VerticalDental, PriceUSDCents: 49900, NoteCap: 3000, AISeatCap: 7,
	},
	PlanSmileProAnnual: {
		Code: PlanSmileProAnnual, Product: PlanProductSmile,
		Tier: PlanTierPro, Cycle: PlanCycleAnnual,
		Vertical: VerticalDental, PriceUSDCents: 49900, NoteCap: 3000, AISeatCap: 7,
	},

	// ── Salvia Clinic (General primary care) ───────────────────────────────
	PlanClinicPracticeMonthly: {
		Code: PlanClinicPracticeMonthly, Product: PlanProductClinic,
		Tier: PlanTierPractice, Cycle: PlanCycleMonthly,
		Vertical: VerticalGeneralClinic, PriceUSDCents: 24900, NoteCap: 2000, AISeatCap: 3,
	},
	PlanClinicPracticeAnnual: {
		Code: PlanClinicPracticeAnnual, Product: PlanProductClinic,
		Tier: PlanTierPractice, Cycle: PlanCycleAnnual,
		Vertical: VerticalGeneralClinic, PriceUSDCents: 24900, NoteCap: 2000, AISeatCap: 3,
	},
	PlanClinicProMonthly: {
		Code: PlanClinicProMonthly, Product: PlanProductClinic,
		Tier: PlanTierPro, Cycle: PlanCycleMonthly,
		Vertical: VerticalGeneralClinic, PriceUSDCents: 59900, NoteCap: 5000, AISeatCap: 7,
	},
	PlanClinicProAnnual: {
		Code: PlanClinicProAnnual, Product: PlanProductClinic,
		Tier: PlanTierPro, Cycle: PlanCycleAnnual,
		Vertical: VerticalGeneralClinic, PriceUSDCents: 59900, NoteCap: 5000, AISeatCap: 7,
	},
}

// AISeatCapForTier returns the per-tier ceiling on note_tier=standard
// staff. Mirrors the registry but exposed as a function so callers
// without a PlanCode (e.g. trial clinics) can still resolve a cap from
// just the tier band. Defaults to 3 (Practice) for an unrecognised tier
// — fail closed rather than silently grant unlimited AI seats.
func AISeatCapForTier(tier PlanTier) int {
	switch tier {
	case PlanTierPro:
		return 7
	case PlanTierEnterprise:
		// Enterprise customers negotiate caps in their contract; the
		// app-level cap is high enough to never bind in practice.
		// Sales/CS adjusts via direct DB / admin tool when needed.
		return 999
	default:
		return 3
	}
}

// PlanFor returns the plan registered for the given code. The bool is
// false if the code is unknown. Callers must always check it — feeding
// an unknown plan_code through to billing logic would silently mis-bill.
func PlanFor(code PlanCode) (Plan, bool) {
	p, ok := Plans[code]
	return p, ok
}

// AnnualPriceUSDCents returns the annualised price for an annual-cycle
// plan after the 17% discount, rounded to the nearest cent. Used for
// display only — Stripe holds the authoritative number.
func (p Plan) AnnualPriceUSDCents() int {
	if p.Cycle != PlanCycleAnnual {
		return 0
	}
	return int(float64(p.PriceUSDCents*12) * annualDiscount)
}

// DeriveTierFromHeadcount maps a count of `note_tier=standard` staff to
// the expected billing tier per pricing-model-v3 §6:
//
//	1–3 standard clinicians → Practice
//	4+ standard clinicians  → Pro
//
// (Pro caps at 7 in marketing copy but >7 still falls into Pro on the
// auto-derivation path — sales/CS handle the enterprise upgrade
// conversation manually.) Returns ("", false) for non-positive counts
// so callers can distinguish "no clinicians yet, leave plan alone" from
// a legitimate downgrade.
func DeriveTierFromHeadcount(count int) (PlanTier, bool) {
	if count <= 0 {
		return "", false
	}
	if count <= 3 {
		return PlanTierPractice, true
	}
	return PlanTierPro, true
}

// PlanCodeFor resolves the canonical PlanCode for a (product, tier,
// cycle) triple by scanning the registry. Returns ("", false) when no
// plan exists for the combination — should never happen for known
// products + tiers + cycles, but the bool keeps the call site honest.
func PlanCodeFor(product PlanProduct, tier PlanTier, cycle PlanCycle) (PlanCode, bool) {
	for code, p := range Plans {
		if p.Product == product && p.Tier == tier && p.Cycle == cycle {
			return code, true
		}
	}
	return "", false
}
