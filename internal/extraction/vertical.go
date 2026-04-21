package extraction

// verticalContextLine returns a short natural-language sentence the AI can read
// to ground its analysis in the correct clinical discipline. Empty or unknown
// values fall back to a generic "clinical" framing so prompts stay safe to
// send even when a clinic's vertical is missing from the DB.
func verticalContextLine(vertical string) string {
	switch vertical {
	case "veterinary":
		return "This note is from a veterinary clinic — patients are animals; owners are the human contacts."
	case "dental":
		return "This note is from a dental clinic — patients are people receiving dental care."
	case "aged_care":
		return "This note is from an aged care facility — the subject of care is referred to as a resident."
	case "general_clinic":
		return "This note is from a general medical clinic — patients are people receiving general medical care."
	default:
		return "Clinic type is not specified — infer the discipline from the transcript and form context."
	}
}

// verticalContextLineForm is the form-design variant: talks about the form
// rather than the note.
func verticalContextLineForm(vertical string) string {
	switch vertical {
	case "veterinary":
		return "This form belongs to a veterinary clinic — patients are animals."
	case "dental":
		return "This form belongs to a dental clinic — patients are people receiving dental care."
	case "aged_care":
		return "This form belongs to an aged care facility — the subject of care is referred to as a resident."
	case "general_clinic":
		return "This form belongs to a general medical clinic — patients are people receiving general medical care."
	default:
		return "Clinic type is not specified — infer the discipline from the form purpose and clause wording."
	}
}
