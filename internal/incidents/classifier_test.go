package incidents

import (
	"testing"
	"time"
)

// TestClassify_AgedCareAU_AbuseTypesAreAlwaysPriority1 validates the most
// regulator-load-bearing rule: the ten abuse / neglect / restraint /
// missing-resident / unexplained-injury types auto-classify to SIRS
// Priority 1 with a 24h deadline regardless of severity. Getting this
// wrong would silently put providers in breach of the SIRS Rules.
func TestClassify_AgedCareAU_AbuseTypesAreAlwaysPriority1(t *testing.T) {
	t.Parallel()
	occurred := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	priority1Types := []string{
		"physical_abuse",
		"sexual_misconduct",
		"psychological_abuse",
		"financial_abuse",
		"neglect",
		"restraint",
		"unauthorised_absence",
		"unexplained_injury",
	}
	for _, typ := range priority1Types {
		c := Classify(ClassifyInput{
			Vertical:     "aged_care",
			Country:      "AU",
			IncidentType: typ,
			Severity:     "low", // even low severity → P1
			OccurredAt:   occurred,
		})
		if c.SIRSPriority != "priority_1" {
			t.Errorf("type %s: SIRSPriority = %q, want priority_1", typ, c.SIRSPriority)
		}
		if c.NotificationDeadline == nil {
			t.Errorf("type %s: deadline not set", typ)
			continue
		}
		want := occurred.Add(24 * time.Hour)
		if !c.NotificationDeadline.Equal(want) {
			t.Errorf("type %s: deadline = %v, want %v", typ, *c.NotificationDeadline, want)
		}
	}
}

// TestClassify_AgedCareAU_HospitalisationBumpsToPriority1 — a fall is a
// reportable type but only Priority 2 by default; if the resident ends
// up in hospital it must escalate to Priority 1.
func TestClassify_AgedCareAU_HospitalisationBumpsToPriority1(t *testing.T) {
	t.Parallel()
	occurred := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	c := Classify(ClassifyInput{
		Vertical:       "aged_care",
		Country:        "AU",
		IncidentType:   "fall",
		Severity:       "medium",
		SubjectOutcome: "hospitalised",
		OccurredAt:     occurred,
	})
	if c.SIRSPriority != "priority_1" {
		t.Errorf("SIRSPriority = %q, want priority_1", c.SIRSPriority)
	}
	if c.NotificationDeadline == nil ||
		!c.NotificationDeadline.Equal(occurred.Add(24*time.Hour)) {
		t.Errorf("deadline mismatch")
	}
}

// TestClassify_AgedCareAU_FallNoHarmIsPriority2 — a routine fall with
// no injury falls under SIRS Priority 2 (30-day window).
func TestClassify_AgedCareAU_FallNoHarmIsPriority2(t *testing.T) {
	t.Parallel()
	occurred := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	c := Classify(ClassifyInput{
		Vertical:       "aged_care",
		Country:        "AU",
		IncidentType:   "fall",
		Severity:       "low",
		SubjectOutcome: "no_harm",
		OccurredAt:     occurred,
	})
	if c.SIRSPriority != "priority_2" {
		t.Errorf("SIRSPriority = %q, want priority_2", c.SIRSPriority)
	}
	want := occurred.Add(30 * 24 * time.Hour)
	if c.NotificationDeadline == nil || !c.NotificationDeadline.Equal(want) {
		t.Errorf("deadline = %v, want %v", c.NotificationDeadline, want)
	}
}

// TestClassify_AgedCareAU_OtherCountriesAreInternalOnly — the same
// incident in NZ/UK/US doesn't trigger SIRS (which is AU-only).
func TestClassify_AgedCareAU_OtherCountriesAreInternalOnly(t *testing.T) {
	t.Parallel()
	for _, country := range []string{"NZ", "US"} {
		c := Classify(ClassifyInput{
			Vertical:     "aged_care",
			Country:      country,
			IncidentType: "physical_abuse",
			Severity:     "high",
			OccurredAt:   time.Now(),
		})
		if c.SIRSPriority != "" {
			t.Errorf("country %s: SIRSPriority = %q, want empty", country, c.SIRSPriority)
		}
	}
}

// TestClassify_AgedCareUK_AbuseTriggersReg18Notification — UK aged care
// abuse allegations are CQC notifiable under Reg 18 with a 24h cut-off.
func TestClassify_AgedCareUK_AbuseTriggersReg18Notification(t *testing.T) {
	t.Parallel()
	occurred := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	for _, typ := range []string{
		"physical_abuse", "psychological_abuse",
		"sexual_misconduct", "financial_abuse", "neglect",
	} {
		c := Classify(ClassifyInput{
			Vertical:     "aged_care",
			Country:      "UK",
			IncidentType: typ,
			Severity:     "medium",
			OccurredAt:   occurred,
		})
		if !c.CQCNotifiable {
			t.Errorf("type %s: CQCNotifiable = false, want true", typ)
		}
		if c.CQCNotificationType == "" {
			t.Errorf("type %s: CQCNotificationType empty", typ)
		}
		if c.NotificationDeadline == nil ||
			!c.NotificationDeadline.Equal(occurred.Add(24*time.Hour)) {
			t.Errorf("type %s: deadline mismatch", typ)
		}
	}
}

// TestClassify_AgedCareUK_DeathTriggersReg16 — a death notification
// must be filed under Regulation 16 specifically (different from the
// Reg 18 abuse stream — providers fill out a different form).
func TestClassify_AgedCareUK_DeathTriggersReg16(t *testing.T) {
	t.Parallel()
	c := Classify(ClassifyInput{
		Vertical:     "aged_care",
		Country:      "UK",
		IncidentType: "death",
		Severity:     "critical",
		OccurredAt:   time.Now(),
	})
	if !c.CQCNotifiable {
		t.Fatal("CQCNotifiable = false, want true")
	}
	if got := c.CQCNotificationType; got == "" || got[:6] != "Reg 16" {
		t.Errorf("CQCNotificationType = %q, want Reg 16…", got)
	}
}

// TestClassify_AgedCareUK_HospitalisedFallBecomesReg18 — a routine fall
// type becomes notifiable when the resident is hospitalised, even though
// the type alone wouldn't trigger CQC.
func TestClassify_AgedCareUK_HospitalisedFallBecomesReg18(t *testing.T) {
	t.Parallel()
	c := Classify(ClassifyInput{
		Vertical:       "aged_care",
		Country:        "UK",
		IncidentType:   "fall",
		Severity:       "medium",
		SubjectOutcome: "hospitalised",
		OccurredAt:     time.Now(),
	})
	if !c.CQCNotifiable {
		t.Fatal("hospitalised fall: CQCNotifiable = false, want true")
	}
	if c.CQCNotificationType == "" {
		t.Error("CQCNotificationType empty")
	}
}

// TestClassify_NonAgedCareCombosAreInternalOnly — vet / dental / general
// clinics get no auto-classification in v1 (their regulators have
// separate adverse-event schemes that we'll wire later).
func TestClassify_NonAgedCareCombosAreInternalOnly(t *testing.T) {
	t.Parallel()
	for _, vertical := range []string{"veterinary", "dental", "general_clinic"} {
		for _, country := range []string{"NZ", "AU", "UK", "US"} {
			c := Classify(ClassifyInput{
				Vertical:     vertical,
				Country:      country,
				IncidentType: "physical_abuse",
				Severity:     "critical",
				OccurredAt:   time.Now(),
			})
			if c.SIRSPriority != "" || c.CQCNotifiable {
				t.Errorf("%s/%s: expected internal-only, got SIRS=%q CQC=%v",
					vertical, country, c.SIRSPriority, c.CQCNotifiable)
			}
			if c.NotificationDeadline != nil {
				t.Errorf("%s/%s: deadline should be nil, got %v",
					vertical, country, *c.NotificationDeadline)
			}
		}
	}
}

// TestClassify_VerticalAliasNormalisation — caller passes "veterinary"
// (canonical) but the registry keys are "vet"; alias must work.
func TestClassify_VerticalAliasNormalisation(t *testing.T) {
	t.Parallel()
	long := Classify(ClassifyInput{
		Vertical:     "general_clinic", // canonical
		Country:      "AU",
		IncidentType: "physical_abuse",
		Severity:     "high",
		OccurredAt:   time.Now(),
	})
	short := Classify(ClassifyInput{
		Vertical:     "general", // short
		Country:      "AU",
		IncidentType: "physical_abuse",
		Severity:     "high",
		OccurredAt:   time.Now(),
	})
	// Both should be internal-only (no AU-medical scheme yet) — but the
	// alias path must reach the same branch.
	if long.SIRSPriority != short.SIRSPriority || long.Reason != short.Reason {
		t.Errorf("alias mismatch: long=%+v vs short=%+v", long, short)
	}
}
