package aigen

// (intentionally empty)
//
// Earlier scaffolding used an init() hop to feed schema-registry enum values
// into provider_openai.go's literal builders. The provider now imports the
// schema packages directly so this file is no longer needed; it remains as
// a placeholder to make the deletion obvious in the diff and to reserve the
// filename for future provider-specific enum helpers.
