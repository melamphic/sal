package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanFor_KnownCode_ReturnsPlan(t *testing.T) {
	t.Parallel()
	p, ok := PlanFor(PlanPawsPracticeMonthly)
	require.True(t, ok)
	assert.Equal(t, PlanProductPaws, p.Product)
	assert.Equal(t, PlanTierPractice, p.Tier)
	assert.Equal(t, PlanCycleMonthly, p.Cycle)
	assert.Equal(t, VerticalVeterinary, p.Vertical)
	assert.Equal(t, 22900, p.PriceUSDCents)
	assert.Equal(t, 1500, p.NoteCap)
}

func TestPlanFor_UnknownCode_ReturnsFalse(t *testing.T) {
	t.Parallel()
	_, ok := PlanFor("not-a-plan")
	assert.False(t, ok)
}

func TestPlans_RegistryShape_LocksPricingV3(t *testing.T) {
	t.Parallel()
	// Pricing v3 (locked 2026-04-18): 3 products × 2 tiers × 2 cycles = 12
	// SKUs. Enterprise is contact-sales only and intentionally absent.
	require.Len(t, Plans, 12)

	for code, p := range Plans {
		assert.Equal(t, code, p.Code, "registry key must equal Plan.Code")
		assert.NotEmpty(t, p.Vertical)
		assert.NotZero(t, p.PriceUSDCents)
		assert.NotZero(t, p.NoteCap)
		assert.True(t,
			strings.HasSuffix(string(code), "_monthly") ||
				strings.HasSuffix(string(code), "_annual"),
			"code %q must end with cycle suffix", code)
	}
}

func TestPlans_PriceParityAcrossCycles(t *testing.T) {
	t.Parallel()
	// Monthly and annual SKUs share a display price (annual discount is
	// applied at checkout time; the per-month rate is the same number).
	pairs := [][2]PlanCode{
		{PlanPawsPracticeMonthly, PlanPawsPracticeAnnual},
		{PlanPawsProMonthly, PlanPawsProAnnual},
		{PlanSmilePracticeMonthly, PlanSmilePracticeAnnual},
		{PlanSmileProMonthly, PlanSmileProAnnual},
		{PlanClinicPracticeMonthly, PlanClinicPracticeAnnual},
		{PlanClinicProMonthly, PlanClinicProAnnual},
	}
	for _, pair := range pairs {
		m := Plans[pair[0]]
		a := Plans[pair[1]]
		assert.Equal(t, m.PriceUSDCents, a.PriceUSDCents,
			"%s and %s must share base price", pair[0], pair[1])
		assert.Equal(t, m.NoteCap, a.NoteCap,
			"%s and %s must share note cap", pair[0], pair[1])
	}
}

func TestPlan_AnnualPriceUSDCents_Applies17PercentDiscount(t *testing.T) {
	t.Parallel()
	p := Plans[PlanPawsPracticeAnnual]
	// 22900 * 12 * 0.83 = 228084
	assert.Equal(t, 228084, p.AnnualPriceUSDCents())

	// Monthly plans return 0 — annual price is undefined for them.
	assert.Equal(t, 0, Plans[PlanPawsPracticeMonthly].AnnualPriceUSDCents())
}

func TestClinicStatusPastDue_Defined(t *testing.T) {
	t.Parallel()
	// Lock the past_due status string so the migration CHECK constraint
	// stays in sync with the Go constant.
	assert.Equal(t, ClinicStatus("past_due"), ClinicStatusPastDue)
}
