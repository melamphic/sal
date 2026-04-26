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
}

const annualDiscount = 0.83 // 17% off — pricing v3 §8.

// Plans is the canonical registry. Indexed by PlanCode.
var Plans = map[PlanCode]Plan{
	// ── Salvia Paws (Veterinary) ───────────────────────────────────────────
	PlanPawsPracticeMonthly: {
		Code: PlanPawsPracticeMonthly, Product: PlanProductPaws,
		Tier: PlanTierPractice, Cycle: PlanCycleMonthly,
		Vertical: VerticalVeterinary, PriceUSDCents: 22900, NoteCap: 1500,
	},
	PlanPawsPracticeAnnual: {
		Code: PlanPawsPracticeAnnual, Product: PlanProductPaws,
		Tier: PlanTierPractice, Cycle: PlanCycleAnnual,
		Vertical: VerticalVeterinary, PriceUSDCents: 22900, NoteCap: 1500,
	},
	PlanPawsProMonthly: {
		Code: PlanPawsProMonthly, Product: PlanProductPaws,
		Tier: PlanTierPro, Cycle: PlanCycleMonthly,
		Vertical: VerticalVeterinary, PriceUSDCents: 49900, NoteCap: 4000,
	},
	PlanPawsProAnnual: {
		Code: PlanPawsProAnnual, Product: PlanProductPaws,
		Tier: PlanTierPro, Cycle: PlanCycleAnnual,
		Vertical: VerticalVeterinary, PriceUSDCents: 49900, NoteCap: 4000,
	},

	// ── Salvia Smile (Dental) ──────────────────────────────────────────────
	PlanSmilePracticeMonthly: {
		Code: PlanSmilePracticeMonthly, Product: PlanProductSmile,
		Tier: PlanTierPractice, Cycle: PlanCycleMonthly,
		Vertical: VerticalDental, PriceUSDCents: 22900, NoteCap: 1200,
	},
	PlanSmilePracticeAnnual: {
		Code: PlanSmilePracticeAnnual, Product: PlanProductSmile,
		Tier: PlanTierPractice, Cycle: PlanCycleAnnual,
		Vertical: VerticalDental, PriceUSDCents: 22900, NoteCap: 1200,
	},
	PlanSmileProMonthly: {
		Code: PlanSmileProMonthly, Product: PlanProductSmile,
		Tier: PlanTierPro, Cycle: PlanCycleMonthly,
		Vertical: VerticalDental, PriceUSDCents: 49900, NoteCap: 3000,
	},
	PlanSmileProAnnual: {
		Code: PlanSmileProAnnual, Product: PlanProductSmile,
		Tier: PlanTierPro, Cycle: PlanCycleAnnual,
		Vertical: VerticalDental, PriceUSDCents: 49900, NoteCap: 3000,
	},

	// ── Salvia Clinic (General primary care) ───────────────────────────────
	PlanClinicPracticeMonthly: {
		Code: PlanClinicPracticeMonthly, Product: PlanProductClinic,
		Tier: PlanTierPractice, Cycle: PlanCycleMonthly,
		Vertical: VerticalGeneralClinic, PriceUSDCents: 24900, NoteCap: 2000,
	},
	PlanClinicPracticeAnnual: {
		Code: PlanClinicPracticeAnnual, Product: PlanProductClinic,
		Tier: PlanTierPractice, Cycle: PlanCycleAnnual,
		Vertical: VerticalGeneralClinic, PriceUSDCents: 24900, NoteCap: 2000,
	},
	PlanClinicProMonthly: {
		Code: PlanClinicProMonthly, Product: PlanProductClinic,
		Tier: PlanTierPro, Cycle: PlanCycleMonthly,
		Vertical: VerticalGeneralClinic, PriceUSDCents: 59900, NoteCap: 5000,
	},
	PlanClinicProAnnual: {
		Code: PlanClinicProAnnual, Product: PlanProductClinic,
		Tier: PlanTierPro, Cycle: PlanCycleAnnual,
		Vertical: VerticalGeneralClinic, PriceUSDCents: 59900, NoteCap: 5000,
	},
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
