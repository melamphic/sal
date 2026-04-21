package forms

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// fakeRepo is an in-memory implementation of the repo interface used in unit tests.
type fakeRepo struct {
	mu       sync.RWMutex
	groups   map[uuid.UUID]*GroupRecord
	forms    map[uuid.UUID]*FormRecord
	versions map[uuid.UUID]*FormVersionRecord
	fields   map[uuid.UUID][]*FieldRecord // keyed by version ID
	policies map[uuid.UUID][]uuid.UUID    // form_id → policy IDs
	styles   []*StyleVersionRecord        // ordered by version asc
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		groups:   make(map[uuid.UUID]*GroupRecord),
		forms:    make(map[uuid.UUID]*FormRecord),
		versions: make(map[uuid.UUID]*FormVersionRecord),
		fields:   make(map[uuid.UUID][]*FieldRecord),
		policies: make(map[uuid.UUID][]uuid.UUID),
	}
}

// ── Groups ────────────────────────────────────────────────────────────────────

func (f *fakeRepo) CreateGroup(_ context.Context, p CreateGroupParams) (*GroupRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := &GroupRecord{
		ID:          p.ID,
		ClinicID:    p.ClinicID,
		Name:        p.Name,
		Description: p.Description,
		CreatedBy:   p.CreatedBy,
		CreatedAt:   domain.TimeNow(),
		UpdatedAt:   domain.TimeNow(),
	}
	f.groups[g.ID] = g
	return cloneGroup(g), nil
}

func (f *fakeRepo) GetGroupByID(_ context.Context, id, clinicID uuid.UUID) (*GroupRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	g, ok := f.groups[id]
	if !ok || g.ClinicID != clinicID {
		return nil, domain.ErrNotFound
	}
	return cloneGroup(g), nil
}

func (f *fakeRepo) ListGroups(_ context.Context, clinicID uuid.UUID) ([]*GroupRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []*GroupRecord
	for _, g := range f.groups {
		if g.ClinicID == clinicID {
			out = append(out, cloneGroup(g))
		}
	}
	return out, nil
}

func (f *fakeRepo) UpdateGroup(_ context.Context, p UpdateGroupParams) (*GroupRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.groups[p.ID]
	if !ok || g.ClinicID != p.ClinicID {
		return nil, domain.ErrNotFound
	}
	g.Name = p.Name
	g.Description = p.Description
	g.UpdatedAt = domain.TimeNow()
	return cloneGroup(g), nil
}

// ── Forms ─────────────────────────────────────────────────────────────────────

func (f *fakeRepo) CreateForm(_ context.Context, p CreateFormParams) (*FormRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}
	form := &FormRecord{
		ID:            p.ID,
		ClinicID:      p.ClinicID,
		GroupID:       p.GroupID,
		Name:          p.Name,
		Description:   p.Description,
		OverallPrompt: p.OverallPrompt,
		Tags:          tags,
		CreatedBy:     p.CreatedBy,
		CreatedAt:     domain.TimeNow(),
		UpdatedAt:     domain.TimeNow(),
	}
	f.forms[form.ID] = form
	return cloneForm(form), nil
}

func (f *fakeRepo) GetFormByID(_ context.Context, id, clinicID uuid.UUID) (*FormRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	form, ok := f.forms[id]
	if !ok || form.ClinicID != clinicID {
		return nil, domain.ErrNotFound
	}
	return cloneForm(form), nil
}

func (f *fakeRepo) ListForms(_ context.Context, clinicID uuid.UUID, p ListFormsParams) ([]*FormRecord, int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var all []*FormRecord
	for _, form := range f.forms {
		if form.ClinicID != clinicID {
			continue
		}
		if !p.IncludeArchived && form.ArchivedAt != nil {
			continue
		}
		if p.GroupID != nil && (form.GroupID == nil || *form.GroupID != *p.GroupID) {
			continue
		}
		all = append(all, cloneForm(form))
	}
	total := len(all)
	start := p.Offset
	if start > total {
		return []*FormRecord{}, total, nil
	}
	end := start + p.Limit
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func (f *fakeRepo) UpdateFormMeta(_ context.Context, p UpdateFormMetaParams) (*FormRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	form, ok := f.forms[p.ID]
	if !ok || form.ClinicID != p.ClinicID {
		return nil, domain.ErrNotFound
	}
	if form.ArchivedAt != nil {
		return nil, domain.ErrConflict
	}
	form.GroupID = p.GroupID
	form.Name = p.Name
	form.Description = p.Description
	form.OverallPrompt = p.OverallPrompt
	form.Tags = p.Tags
	form.UpdatedAt = domain.TimeNow()
	return cloneForm(form), nil
}

func (f *fakeRepo) RetireForm(_ context.Context, p RetireFormParams) (*FormRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	form, ok := f.forms[p.ID]
	if !ok || form.ClinicID != p.ClinicID {
		return nil, domain.ErrNotFound
	}
	if form.ArchivedAt != nil {
		return nil, domain.ErrConflict
	}
	t := p.ArchivedAt
	form.ArchivedAt = &t
	form.RetireReason = p.RetireReason
	return cloneForm(form), nil
}

// ── Versions ──────────────────────────────────────────────────────────────────

func (f *fakeRepo) GetDraftVersion(_ context.Context, formID uuid.UUID) (*FormVersionRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, v := range f.versions {
		if v.FormID == formID && v.Status == domain.FormVersionStatusDraft {
			return cloneVersion(v), nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeRepo) GetVersionByID(_ context.Context, id uuid.UUID) (*FormVersionRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	v, ok := f.versions[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return cloneVersion(v), nil
}

func (f *fakeRepo) ListPublishedVersions(_ context.Context, formID uuid.UUID) ([]*FormVersionRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []*FormVersionRecord
	for _, v := range f.versions {
		if v.FormID == formID && v.Status == domain.FormVersionStatusPublished {
			out = append(out, cloneVersion(v))
		}
	}
	return out, nil
}

func (f *fakeRepo) GetLatestPublishedVersion(_ context.Context, formID uuid.UUID) (*FormVersionRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var latest *FormVersionRecord
	for _, v := range f.versions {
		if v.FormID != formID || v.Status != domain.FormVersionStatusPublished {
			continue
		}
		if latest == nil || v.PublishedAt.After(*latest.PublishedAt) {
			vv := v
			latest = vv
		}
	}
	if latest == nil {
		return nil, domain.ErrNotFound
	}
	return cloneVersion(latest), nil
}

func (f *fakeRepo) CreateDraftVersion(_ context.Context, p CreateDraftVersionParams) (*FormVersionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Enforce one draft per form.
	for _, v := range f.versions {
		if v.FormID == p.FormID && v.Status == domain.FormVersionStatusDraft {
			return nil, domain.ErrConflict
		}
	}
	v := &FormVersionRecord{
		ID:         p.ID,
		FormID:     p.FormID,
		Status:     domain.FormVersionStatusDraft,
		RollbackOf: p.RollbackOf,
		CreatedBy:  p.CreatedBy,
		CreatedAt:  domain.TimeNow(),
	}
	f.versions[v.ID] = v
	return cloneVersion(v), nil
}

func (f *fakeRepo) CreatePublishedVersion(_ context.Context, p CreatePublishedVersionParams) (*FormVersionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ct := p.ChangeType
	pubAt := p.PublishedAt
	pubBy := p.PublishedBy
	major := p.VersionMajor
	minor := p.VersionMinor
	v := &FormVersionRecord{
		ID:            p.ID,
		FormID:        p.FormID,
		Status:        domain.FormVersionStatusPublished,
		VersionMajor:  &major,
		VersionMinor:  &minor,
		ChangeType:    &ct,
		ChangeSummary: p.ChangeSummary,
		Changes:       p.Changes,
		RollbackOf:    p.RollbackOf,
		PublishedBy:   &pubBy,
		PublishedAt:   &pubAt,
		CreatedBy:     p.PublishedBy,
		CreatedAt:     domain.TimeNow(),
	}
	f.versions[v.ID] = v
	return cloneVersion(v), nil
}

func (f *fakeRepo) PublishDraftVersion(_ context.Context, p PublishDraftVersionParams) (*FormVersionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.versions[p.ID]
	if !ok || v.Status != domain.FormVersionStatusDraft {
		return nil, domain.ErrNotFound
	}
	v.Status = domain.FormVersionStatusPublished
	v.VersionMajor = &p.VersionMajor
	v.VersionMinor = &p.VersionMinor
	ct := p.ChangeType
	v.ChangeType = &ct
	v.ChangeSummary = p.ChangeSummary
	v.PublishedBy = &p.PublishedBy
	v.PublishedAt = &p.PublishedAt
	return cloneVersion(v), nil
}

func (f *fakeRepo) SavePolicyCheckResult(_ context.Context, p SavePolicyCheckParams) (*FormVersionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.versions[p.VersionID]
	if !ok || v.Status != domain.FormVersionStatusDraft {
		return nil, domain.ErrNotFound
	}
	v.PolicyCheckResult = &p.Result
	v.PolicyCheckBy = &p.CheckedBy
	v.PolicyCheckAt = &p.CheckedAt
	return cloneVersion(v), nil
}

// ── Fields ────────────────────────────────────────────────────────────────────

func (f *fakeRepo) GetFieldsByVersionID(_ context.Context, versionID uuid.UUID) ([]*FieldRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	fields := f.fields[versionID]
	out := make([]*FieldRecord, len(fields))
	for i, fld := range fields {
		cp := *fld
		out[i] = &cp
	}
	return out, nil
}

func (f *fakeRepo) ReplaceFields(_ context.Context, versionID uuid.UUID, params []CreateFieldParams) ([]*FieldRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := domain.TimeNow()
	newFields := make([]*FieldRecord, len(params))
	for i, p := range params {
		newFields[i] = &FieldRecord{
			ID:            p.ID,
			FormVersionID: versionID,
			Position:      p.Position,
			Title:         p.Title,
			Type:          p.Type,
			Config:        p.Config,
			AIPrompt:      p.AIPrompt,
			Required:      p.Required,
			Skippable:     p.Skippable,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
	}
	f.fields[versionID] = newFields
	out := make([]*FieldRecord, len(newFields))
	for i, fld := range newFields {
		cp := *fld
		out[i] = &cp
	}
	return out, nil
}

// ── Policies ──────────────────────────────────────────────────────────────────

func (f *fakeRepo) LinkPolicy(_ context.Context, formID, policyID, _ uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.policies[formID] {
		if id == policyID {
			return nil // idempotent
		}
	}
	f.policies[formID] = append(f.policies[formID], policyID)
	return nil
}

func (f *fakeRepo) UnlinkPolicy(_ context.Context, formID, policyID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := f.policies[formID]
	for i, id := range ids {
		if id == policyID {
			f.policies[formID] = append(ids[:i], ids[i+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakeRepo) ListLinkedPolicies(_ context.Context, formID uuid.UUID) ([]uuid.UUID, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]uuid.UUID{}, f.policies[formID]...), nil
}

func (f *fakeRepo) ListFormIDsByPolicyID(_ context.Context, policyID uuid.UUID) ([]uuid.UUID, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []uuid.UUID
	for formID, pids := range f.policies {
		for _, pid := range pids {
			if pid == policyID {
				out = append(out, formID)
				break
			}
		}
	}
	return out, nil
}

// ── Style ─────────────────────────────────────────────────────────────────────

func (f *fakeRepo) GetCurrentStyle(_ context.Context, clinicID uuid.UUID) (*StyleVersionRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var latest *StyleVersionRecord
	for _, s := range f.styles {
		if s.ClinicID == clinicID {
			if latest == nil || s.Version > latest.Version {
				latest = s
			}
		}
	}
	if latest == nil {
		return nil, domain.ErrNotFound
	}
	cp := *latest
	return &cp, nil
}

func (f *fakeRepo) ListStyleVersions(_ context.Context, clinicID uuid.UUID) ([]*StyleVersionRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []*StyleVersionRecord
	for _, s := range f.styles {
		if s.ClinicID == clinicID {
			cp := *s
			out = append(out, &cp)
		}
	}
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Version > out[i].Version {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) CreateStyleVersion(_ context.Context, p CreateStyleVersionParams) (*StyleVersionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.styles {
		if s.ClinicID == p.ClinicID {
			s.IsActive = false
		}
	}
	s := &StyleVersionRecord{
		ID:           p.ID,
		ClinicID:     p.ClinicID,
		Version:      p.Version,
		LogoKey:      p.LogoKey,
		PrimaryColor: p.PrimaryColor,
		FontFamily:   p.FontFamily,
		HeaderExtra:  p.HeaderExtra,
		FooterText:   p.FooterText,
		Config:       p.Config,
		PresetID:     p.PresetID,
		IsActive:     true,
		CreatedBy:    p.CreatedBy,
		CreatedAt:    domain.TimeNow(),
	}
	f.styles = append(f.styles, s)
	cp := *s
	return &cp, nil
}

// ── Clone helpers ─────────────────────────────────────────────────────────────

func cloneGroup(g *GroupRecord) *GroupRecord { cp := *g; return &cp }
func cloneForm(f *FormRecord) *FormRecord    { cp := *f; return &cp }
func cloneVersion(v *FormVersionRecord) *FormVersionRecord {
	cp := *v
	return &cp
}
