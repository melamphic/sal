package forms

import "encoding/json"

// Vertical-specific starter themes for the doc-theme designer. Keyed by
// vertical (matches clinics.vertical: aged_care, veterinary, dental, general_clinic). Each
// vertical ships with three presets — a "clean clinical" default, a more
// expressive / branded option, and a compliance-leaning "formal" option.
//
// Presets are hand-curated JSON so the Flutter designer can load them with
// zero further shaping. The schema mirrors the DocThemeConfig Flutter model:
//
//	{
//	  "header":    { shape, fill, content slots ... },
//	  "theme":     { primary_color, body_font, heading_font ... },
//	  "body":      { label_style, value_style, separator ... },
//	  "watermark": { kind, opacity, position ... },
//	  "footer":    { shape, text, left/center/right slots ... },
//	  "signature": { show, label ... },
//	  "page":      { size, margins ... }
//	}
//
// Any field may be omitted — the Flutter side fills missing values from
// sensible defaults.

// preset is the in-memory form of a starter template before serialisation.
type preset struct {
	ID          string
	Vertical    string
	Name        string
	Description string
	// Config is declared as a map so we compile-check shape here; it is
	// serialised to json.RawMessage for the API.
	Config map[string]any
}

// presetsFor returns the preset set for a vertical. Falls back to "general_clinic".
func presetsFor(vertical string) []*FormStylePresetResponse {
	set, ok := presetTable[vertical]
	if !ok {
		set = presetTable["general_clinic"]
	}
	out := make([]*FormStylePresetResponse, len(set))
	for i, p := range set {
		cfg, _ := json.Marshal(p.Config)
		out[i] = &FormStylePresetResponse{
			ID:          p.ID,
			Vertical:    p.Vertical,
			Name:        p.Name,
			Description: p.Description,
			Config:      cfg,
		}
	}
	return out
}

// Shared building blocks to keep presets terse and consistent.
func headerFlat(color, text string) map[string]any {
	return map[string]any{
		"shape":      "flat",
		"fill":       map[string]any{"kind": "solid", "color": color},
		"height":     "medium",
		"extra_text": text,
		"slots": map[string]any{
			"clinic_name":  true,
			"logo":         "left",
			"contact_line": true,
		},
	}
}

func headerCurve(from, to, text string) map[string]any {
	return map[string]any{
		"shape":      "single_curve",
		"fill":       map[string]any{"kind": "gradient", "from": from, "to": to},
		"height":     "tall",
		"extra_text": text,
		"slots": map[string]any{
			"clinic_name":  true,
			"logo":         "left",
			"contact_line": true,
			"tagline":      true,
		},
	}
}

func headerWave(from, to string) map[string]any {
	return map[string]any{
		"shape":  "double_wave",
		"fill":   map[string]any{"kind": "gradient", "from": from, "to": to},
		"height": "tall",
		"slots": map[string]any{
			"clinic_name":  true,
			"logo":         "center",
			"contact_line": true,
			"badges":       true, // accreditation logos row
		},
	}
}

func themeBlock(primary, accent, heading, body string) map[string]any {
	return map[string]any{
		"primary_color":    primary,
		"secondary_color":  accent,
		"accent_color":     accent,
		"text_color":       "#1A1A1A",
		"muted_text_color": "#6B7280",
		"heading_font":     heading,
		"body_font":        body,
		"base_size":        11,
		"line_height":      1.4,
		"corner_radius":    6,
	}
}

func bodyBlock(density, sep string) map[string]any {
	return map[string]any{
		"label_style":     "bold",
		"value_style":     "regular",
		"field_separator": sep, // "dotted" | "solid" | "none"
		"density":         density, // "compact" | "comfortable" | "airy"
		"section_heading": "caps_underline",
	}
}

func watermark(kind, asset string, opacity float64) map[string]any {
	return map[string]any{
		"kind":     kind, // "image" | "text" | "none"
		"asset":   asset,
		"opacity": opacity,
		"size":    "large",
		"position": "center",
	}
}

func footerFlat(color, text string) map[string]any {
	return map[string]any{
		"shape": "flat",
		"fill":  map[string]any{"kind": "solid", "color": color},
		"text":  text,
		"slots": map[string]any{
			"left":   "address",
			"center": "form_meta", // name + version + approver
			"right":  "page_number",
		},
		"fine_print": true,
	}
}

func footerCurve(from, to, text string) map[string]any {
	return map[string]any{
		"shape": "single_curve",
		"fill":  map[string]any{"kind": "gradient", "from": from, "to": to},
		"text":  text,
		"slots": map[string]any{
			"left":   "address",
			"center": "form_meta",
			"right":  "contact",
		},
		"fine_print": true,
	}
}

func signatureBlock(label string) map[string]any {
	return map[string]any{
		"show":                true,
		"label":               label,
		"include_printed_name": true,
		"include_role":         true,
		"include_reg_no":       true,
		"line_style":           "solid",
	}
}

func pageBlock() map[string]any {
	return map[string]any{
		"size":        "A4",
		"orientation": "portrait",
		"margin_mm":   18,
	}
}

// ── Preset catalogue ──────────────────────────────────────────────────────────

var presetTable = map[string][]*preset{

	// Aged care — institutional, compliance-heavy, multi-facility. Emphasis on
	// facility id + room number in patient block, regulator reference in footer.
	"aged_care": {
		{
			ID: "aged_care.warm_institutional", Vertical: "aged_care",
			Name:        "Warm Institutional",
			Description: "Muted teal header, warm cream body. Signals care without clinical sterility.",
			Config: map[string]any{
				"header":    headerCurve("#2F5E6B", "#4E8A98", "Care Plan"),
				"theme":     themeBlock("#2F5E6B", "#D97706", "Lora", "Inter"),
				"body":      bodyBlock("comfortable", "dotted"),
				"watermark": watermark("image", "shield_heart", 0.06),
				"footer":    footerCurve("#2F5E6B", "#4E8A98", "Facility ref no. · Regulator license · page {n}"),
				"signature": signatureBlock("Signed by (RN / Care Manager)"),
				"page":      pageBlock(),
			},
		},
		{
			ID: "aged_care.clinical_neutral", Vertical: "aged_care",
			Name:        "Clinical Neutral",
			Description: "Flat navy header, compact layout. Optimised for dense assessment forms.",
			Config: map[string]any{
				"header":    headerFlat("#1E3A5F", "Assessment Record"),
				"theme":     themeBlock("#1E3A5F", "#0EA5E9", "Inter", "Inter"),
				"body":      bodyBlock("compact", "solid"),
				"watermark": watermark("none", "", 0),
				"footer":    footerFlat("#1E3A5F", "Confidential · Regulator license {license} · page {n}"),
				"signature": signatureBlock("Assessed by"),
				"page":      pageBlock(),
			},
		},
		{
			ID: "aged_care.facility_branded", Vertical: "aged_care",
			Name:        "Facility Branded",
			Description: "Large wave header with badges row for accreditation logos; made for group homes.",
			Config: map[string]any{
				"header":    headerWave("#3D6B50", "#8CB9A0"),
				"theme":     themeBlock("#3D6B50", "#F59E0B", "Plus Jakarta Sans", "Plus Jakarta Sans"),
				"body":      bodyBlock("comfortable", "dotted"),
				"watermark": watermark("image", "shield_heart", 0.05),
				"footer":    footerCurve("#3D6B50", "#8CB9A0", "{facility_name} · {address} · page {n}"),
				"signature": signatureBlock("Reviewed by Care Manager"),
				"page":      pageBlock(),
			},
		},
	},

	// Vet — pet-parent facing, can afford a bit of warmth. Animal + owner slots.
	"veterinary": {
		{
			ID: "veterinary.friendly_paw", Vertical: "veterinary",
			Name:        "Friendly Paw",
			Description: "Soft curve header in warm teal, paw watermark. For everyday consults.",
			Config: map[string]any{
				"header":    headerCurve("#0E7490", "#22D3EE", "Visit Record"),
				"theme":     themeBlock("#0E7490", "#F97316", "Plus Jakarta Sans", "Inter"),
				"body":      bodyBlock("comfortable", "dotted"),
				"watermark": watermark("image", "paw", 0.07),
				"footer":    footerCurve("#0E7490", "#22D3EE", "{clinic_name} · {phone} · page {n}"),
				"signature": signatureBlock("Attending vet (DVM)"),
				"page":      pageBlock(),
			},
		},
		{
			ID: "veterinary.clinical_vet", Vertical: "veterinary",
			Name:        "Clinical Vet",
			Description: "Flat charcoal header, high density. Treatment records and dispense slips.",
			Config: map[string]any{
				"header":    headerFlat("#1F2937", "Treatment Record"),
				"theme":     themeBlock("#1F2937", "#10B981", "Inter", "Inter"),
				"body":      bodyBlock("compact", "solid"),
				"watermark": watermark("none", "", 0),
				"footer":    footerFlat("#1F2937", "DVM reg {reg_no} · {form_name} v{version} · page {n}"),
				"signature": signatureBlock("Attending DVM"),
				"page":      pageBlock(),
			},
		},
		{
			ID: "veterinary.equine_pro", Vertical: "veterinary",
			Name:        "Equine Pro",
			Description: "Double-wave forest header, airy spacing. Made for long large-animal exams.",
			Config: map[string]any{
				"header":    headerWave("#14532D", "#4ADE80"),
				"theme":     themeBlock("#14532D", "#CA8A04", "Lora", "Inter"),
				"body":      bodyBlock("airy", "dotted"),
				"watermark": watermark("image", "horseshoe", 0.05),
				"footer":    footerCurve("#14532D", "#4ADE80", "{clinic_name} · DVM {reg_no} · page {n}"),
				"signature": signatureBlock("Examining Vet"),
				"page":      pageBlock(),
			},
		},
	},

	// Dental — patient-facing, often cosmetic. Tooth watermark and Rx lanes.
	"dental": {
		{
			ID: "dental.clean_clinical", Vertical: "dental",
			Name:        "Clean Clinical",
			Description: "Sky-blue curve header, tooth watermark. Treatment notes and Rx pads.",
			Config: map[string]any{
				"header":    headerCurve("#1E88E5", "#90CAF9", "Treatment Record"),
				"theme":     themeBlock("#1E88E5", "#F43F5E", "Plus Jakarta Sans", "Inter"),
				"body":      bodyBlock("comfortable", "dotted"),
				"watermark": watermark("image", "tooth", 0.06),
				"footer":    footerCurve("#1E88E5", "#90CAF9", "Dental Council reg {reg_no} · page {n}"),
				"signature": signatureBlock("Treating Dentist"),
				"page":      pageBlock(),
			},
		},
		{
			ID: "dental.spa_cosmetic", Vertical: "dental",
			Name:        "Spa Cosmetic",
			Description: "Gold wave header, warm ivory body. Cosmetic and aesthetic clinics.",
			Config: map[string]any{
				"header":    headerWave("#B45309", "#FCD34D"),
				"theme":     themeBlock("#B45309", "#1E293B", "Lora", "Inter"),
				"body":      bodyBlock("airy", "none"),
				"watermark": watermark("image", "tooth", 0.04),
				"footer":    footerCurve("#B45309", "#FCD34D", "{clinic_name} · {web} · page {n}"),
				"signature": signatureBlock("Lead Clinician"),
				"page":      pageBlock(),
			},
		},
		{
			ID: "dental.classic_rx", Vertical: "dental",
			Name:        "Classic Rx",
			Description: "Flat header, right-side doctors strip, Rx-pad layout. For prescriptions.",
			Config: map[string]any{
				"header":    headerFlat("#0F172A", "Prescription"),
				"theme":     themeBlock("#0F172A", "#0EA5E9", "Lora", "Inter"),
				"body":      bodyBlock("compact", "solid"),
				"watermark": watermark("image", "rx", 0.05),
				"footer":    footerFlat("#0F172A", "Not for Medico-legal purpose · {reg_no} · page {n}"),
				"signature": signatureBlock("Doctor's Signature"),
				"page":      pageBlock(),
			},
		},
	},

	// General clinics — catch-all.
	"general_clinic": {
		{
			ID: "general_clinic.classic_rx", Vertical: "general_clinic",
			Name:        "Classic Rx",
			Description: "Caduceus watermark, flat teal header. Familiar prescription look.",
			Config: map[string]any{
				"header":    headerCurve("#115E59", "#5EEAD4", "Medical Record"),
				"theme":     themeBlock("#115E59", "#DB2777", "Lora", "Inter"),
				"body":      bodyBlock("comfortable", "dotted"),
				"watermark": watermark("image", "caduceus", 0.06),
				"footer":    footerCurve("#115E59", "#5EEAD4", "{clinic_name} · {address} · page {n}"),
				"signature": signatureBlock("Doctor's Signature"),
				"page":      pageBlock(),
			},
		},
		{
			ID: "general_clinic.modern_medical", Vertical: "general_clinic",
			Name:        "Modern Medical",
			Description: "Flat slate header, Plus Jakarta Sans, high-density body. For multi-specialist.",
			Config: map[string]any{
				"header":    headerFlat("#334155", "Consultation Note"),
				"theme":     themeBlock("#334155", "#0EA5E9", "Plus Jakarta Sans", "Plus Jakarta Sans"),
				"body":      bodyBlock("compact", "solid"),
				"watermark": watermark("none", "", 0),
				"footer":    footerFlat("#334155", "{clinic_name} · {form_name} v{version} · page {n}"),
				"signature": signatureBlock("Reviewed by"),
				"page":      pageBlock(),
			},
		},
		{
			ID: "general_clinic.specialist_clean", Vertical: "general_clinic",
			Name:        "Specialist Clean",
			Description: "Wave header with badges row, Lora headings. For specialist clinics and reports.",
			Config: map[string]any{
				"header":    headerWave("#1D4ED8", "#60A5FA"),
				"theme":     themeBlock("#1D4ED8", "#F59E0B", "Lora", "Inter"),
				"body":      bodyBlock("comfortable", "dotted"),
				"watermark": watermark("image", "caduceus", 0.05),
				"footer":    footerCurve("#1D4ED8", "#60A5FA", "{specialist_name} · {reg_no} · page {n}"),
				"signature": signatureBlock("Consulting Specialist"),
				"page":      pageBlock(),
			},
		},
	},
}
