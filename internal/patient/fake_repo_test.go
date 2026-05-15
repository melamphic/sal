package patient

import (
	"context"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// fakeRepo is an in-memory implementation of the repo interface used in unit tests.
// It is not safe for concurrent write access from multiple goroutines.
type fakeRepo struct {
	mu              sync.RWMutex
	contacts        map[uuid.UUID]*ContactRecord
	subjects        map[uuid.UUID]*SubjectRecord
	vetDetails      map[uuid.UUID]*VetDetailsRecord
	dentalDetails   map[uuid.UUID]*DentalDetailsRecord
	generalDetails  map[uuid.UUID]*GeneralDetailsRecord
	agedCareDetails map[uuid.UUID]*AgedCareDetailsRecord
	subjectContacts []*SubjectContactRecord
	accessLog       []*SubjectAccessLogRecord
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		contacts:        make(map[uuid.UUID]*ContactRecord),
		subjects:        make(map[uuid.UUID]*SubjectRecord),
		vetDetails:      make(map[uuid.UUID]*VetDetailsRecord),
		dentalDetails:   make(map[uuid.UUID]*DentalDetailsRecord),
		generalDetails:  make(map[uuid.UUID]*GeneralDetailsRecord),
		agedCareDetails: make(map[uuid.UUID]*AgedCareDetailsRecord),
	}
}

// ── Contacts ──────────────────────────────────────────────────────────────────

func (f *fakeRepo) CreateContact(_ context.Context, p CreateContactParams) (*ContactRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &ContactRecord{
		ID:        p.ID,
		ClinicID:  p.ClinicID,
		FullName:  p.FullName,
		Phone:     p.Phone,
		Email:     p.Email,
		EmailHash: p.EmailHash,
		Address:   p.Address,
		CreatedAt: domain.TimeNow(),
		UpdatedAt: domain.TimeNow(),
	}
	f.contacts[c.ID] = c
	return clone(c), nil
}

func (f *fakeRepo) GetContactByID(_ context.Context, id, clinicID uuid.UUID) (*ContactRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	c, ok := f.contacts[id]
	if !ok || c.ClinicID != clinicID || c.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	return clone(c), nil
}

func (f *fakeRepo) ListContacts(_ context.Context, clinicID uuid.UUID, p ListParams) ([]*ContactRecord, int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var all []*ContactRecord
	for _, c := range f.contacts {
		if c.ClinicID == clinicID && c.ArchivedAt == nil {
			all = append(all, clone(c))
		}
	}
	total := len(all)
	start := p.Offset
	if start > total {
		return []*ContactRecord{}, total, nil
	}
	end := start + p.Limit
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func (f *fakeRepo) UpdateContact(_ context.Context, id, clinicID uuid.UUID, p UpdateContactParams) (*ContactRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.contacts[id]
	if !ok || c.ClinicID != clinicID || c.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	if p.FullName != nil {
		c.FullName = *p.FullName
	}
	if p.Phone != nil {
		c.Phone = p.Phone
	}
	if p.Email != nil {
		c.Email = p.Email
	}
	if p.EmailHash != nil {
		c.EmailHash = p.EmailHash
	}
	if p.Address != nil {
		c.Address = p.Address
	}
	c.UpdatedAt = domain.TimeNow()
	return clone(c), nil
}

func (f *fakeRepo) ArchiveContact(_ context.Context, id, clinicID uuid.UUID) (*ContactRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.contacts[id]
	if !ok || c.ClinicID != clinicID || c.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	for _, s := range f.subjects {
		if s.ClinicID != clinicID || s.ArchivedAt != nil {
			continue
		}
		if s.ContactID != nil && *s.ContactID == id {
			return nil, domain.ErrConflict
		}
	}
	now := domain.TimeNow()
	c.ArchivedAt = &now
	c.UpdatedAt = now
	return clone(c), nil
}

// ── Subjects ──────────────────────────────────────────────────────────────────

func (f *fakeRepo) CreateSubject(_ context.Context, p CreateSubjectParams) (*SubjectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &SubjectRecord{
		ID:          p.ID,
		ClinicID:    p.ClinicID,
		ContactID:   p.ContactID,
		DisplayName: p.DisplayName,
		Status:      p.Status,
		Vertical:    p.Vertical,
		PhotoURL:    p.PhotoURL,
		CreatedBy:   p.CreatedBy,
		CreatedAt:   domain.TimeNow(),
		UpdatedAt:   domain.TimeNow(),
	}
	f.subjects[s.ID] = s
	return cloneSubject(s), nil
}

func (f *fakeRepo) CreateVetDetails(_ context.Context, p CreateVetDetailsParams) (*VetDetailsRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := &VetDetailsRecord{
		SubjectID:             p.SubjectID,
		Species:               p.Species,
		Breed:                 p.Breed,
		Sex:                   p.Sex,
		Desexed:               p.Desexed,
		DateOfBirth:           p.DateOfBirth,
		Color:                 p.Color,
		Microchip:             p.Microchip,
		WeightKg:              p.WeightKg,
		Allergies:             p.Allergies,
		ChronicConditions:     p.ChronicConditions,
		AdmissionWarnings:     p.AdmissionWarnings,
		InsuranceProviderName: p.InsuranceProviderName,
		InsurancePolicyNumber: p.InsurancePolicyNumber,
		ReferringVetName:      p.ReferringVetName,
		CreatedAt:             domain.TimeNow(),
		UpdatedAt:             domain.TimeNow(),
	}
	f.vetDetails[d.SubjectID] = d
	return cloneVet(d), nil
}

func (f *fakeRepo) GetSubjectByID(_ context.Context, id, clinicID uuid.UUID) (*SubjectRow, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s, ok := f.subjects[id]
	if !ok || s.ClinicID != clinicID || s.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	row := &SubjectRow{Subject: *cloneSubject(s)}
	if s.ContactID != nil {
		if c, ok := f.contacts[*s.ContactID]; ok && c.ArchivedAt == nil {
			row.Contact = clone(c)
		}
	}
	if d, ok := f.vetDetails[s.ID]; ok {
		row.VetDetails = cloneVet(d)
	}
	if dd, ok := f.dentalDetails[s.ID]; ok {
		row.DentalDetails = cloneDental(dd)
	}
	if g, ok := f.generalDetails[s.ID]; ok {
		row.GeneralDetails = cloneGeneral(g)
	}
	if a, ok := f.agedCareDetails[s.ID]; ok {
		row.AgedCareDetails = cloneAgedCare(a)
	}
	return row, nil
}

func (f *fakeRepo) ListSubjects(_ context.Context, clinicID uuid.UUID, p ListSubjectsParams) ([]*SubjectRow, int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var all []*SubjectRow
	for _, s := range f.subjects {
		if s.ClinicID != clinicID || s.ArchivedAt != nil {
			continue
		}
		if p.Status != nil && s.Status != *p.Status {
			continue
		}
		if p.ContactID != nil && (s.ContactID == nil || *s.ContactID != *p.ContactID) {
			continue
		}
		if p.CreatedBy != nil && s.CreatedBy != *p.CreatedBy {
			continue
		}
		if p.Search != nil && *p.Search != "" {
			if !strings.Contains(strings.ToLower(s.DisplayName), strings.ToLower(*p.Search)) {
				continue
			}
		}
		row := &SubjectRow{Subject: *cloneSubject(s)}
		if s.ContactID != nil {
			if c, ok := f.contacts[*s.ContactID]; ok {
				row.Contact = clone(c)
			}
		}
		if d, ok := f.vetDetails[s.ID]; ok {
			if p.Species != nil && d.Species != *p.Species {
				continue
			}
			row.VetDetails = cloneVet(d)
		} else if p.Species != nil {
			continue
		}
		if dd, ok := f.dentalDetails[s.ID]; ok {
			row.DentalDetails = cloneDental(dd)
		}
		if g, ok := f.generalDetails[s.ID]; ok {
			row.GeneralDetails = cloneGeneral(g)
		}
		if a, ok := f.agedCareDetails[s.ID]; ok {
			row.AgedCareDetails = cloneAgedCare(a)
		}
		all = append(all, row)
	}
	total := len(all)
	start := p.Offset
	if start > total {
		return []*SubjectRow{}, total, nil
	}
	end := start + p.Limit
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func (f *fakeRepo) UpdateSubject(_ context.Context, id, clinicID uuid.UUID, p UpdateSubjectParams) (*SubjectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.subjects[id]
	if !ok || s.ClinicID != clinicID || s.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	if p.DisplayName != nil {
		s.DisplayName = *p.DisplayName
	}
	if p.Status != nil {
		s.Status = *p.Status
	}
	if p.ContactID != nil {
		s.ContactID = p.ContactID
	}
	if p.PhotoURL != nil {
		s.PhotoURL = p.PhotoURL
	}
	s.UpdatedAt = domain.TimeNow()
	return cloneSubject(s), nil
}

func (f *fakeRepo) UpdateVetDetails(_ context.Context, subjectID uuid.UUID, p UpdateVetDetailsParams) (*VetDetailsRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.vetDetails[subjectID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if p.Breed != nil {
		d.Breed = p.Breed
	}
	if p.Sex != nil {
		d.Sex = p.Sex
	}
	if p.Desexed != nil {
		d.Desexed = p.Desexed
	}
	if p.DateOfBirth != nil {
		d.DateOfBirth = p.DateOfBirth
	}
	if p.Color != nil {
		d.Color = p.Color
	}
	if p.Microchip != nil {
		d.Microchip = p.Microchip
	}
	if p.WeightKg != nil {
		d.WeightKg = p.WeightKg
	}
	if p.Allergies != nil {
		d.Allergies = p.Allergies
	}
	if p.ChronicConditions != nil {
		d.ChronicConditions = p.ChronicConditions
	}
	if p.AdmissionWarnings != nil {
		d.AdmissionWarnings = p.AdmissionWarnings
	}
	if p.InsuranceProviderName != nil {
		d.InsuranceProviderName = p.InsuranceProviderName
	}
	if p.InsurancePolicyNumber != nil {
		d.InsurancePolicyNumber = p.InsurancePolicyNumber
	}
	if p.ReferringVetName != nil {
		d.ReferringVetName = p.ReferringVetName
	}
	d.UpdatedAt = domain.TimeNow()
	return cloneVet(d), nil
}

func (f *fakeRepo) ArchiveSubject(_ context.Context, id, clinicID uuid.UUID) (*SubjectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.subjects[id]
	if !ok || s.ClinicID != clinicID || s.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	now := domain.TimeNow()
	s.ArchivedAt = &now
	s.Status = domain.SubjectStatusArchived
	s.UpdatedAt = now
	return cloneSubject(s), nil
}

func (f *fakeRepo) LinkContact(_ context.Context, subjectID, clinicID, contactID uuid.UUID) (*SubjectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.subjects[subjectID]
	if !ok || s.ClinicID != clinicID || s.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	s.ContactID = &contactID
	s.UpdatedAt = domain.TimeNow()
	return cloneSubject(s), nil
}

func (f *fakeRepo) ListSubjectsByContact(_ context.Context, contactID, clinicID uuid.UUID) ([]*SubjectRow, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var rows []*SubjectRow
	for _, s := range f.subjects {
		if s.ClinicID != clinicID || s.ArchivedAt != nil {
			continue
		}
		if s.ContactID == nil || *s.ContactID != contactID {
			continue
		}
		row := &SubjectRow{Subject: *cloneSubject(s)}
		if c, ok := f.contacts[contactID]; ok {
			row.Contact = clone(c)
		}
		if d, ok := f.vetDetails[s.ID]; ok {
			row.VetDetails = cloneVet(d)
		}
		if dd, ok := f.dentalDetails[s.ID]; ok {
			row.DentalDetails = cloneDental(dd)
		}
		if g, ok := f.generalDetails[s.ID]; ok {
			row.GeneralDetails = cloneGeneral(g)
		}
		if a, ok := f.agedCareDetails[s.ID]; ok {
			row.AgedCareDetails = cloneAgedCare(a)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (f *fakeRepo) LookupDisplayNames(_ context.Context, clinicID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make(map[uuid.UUID]string, len(ids))
	for _, id := range ids {
		s, ok := f.subjects[id]
		if !ok || s.ClinicID != clinicID || s.ArchivedAt != nil {
			continue
		}
		out[id] = s.DisplayName
	}
	return out, nil
}

func (f *fakeRepo) CreateDentalDetails(_ context.Context, p CreateDentalDetailsParams) (*DentalDetailsRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dd := &DentalDetailsRecord{
		SubjectID:             p.SubjectID,
		DateOfBirth:           p.DateOfBirth,
		Sex:                   p.Sex,
		MedicalAlerts:         p.MedicalAlerts,
		Medications:           p.Medications,
		Allergies:             p.Allergies,
		ChronicConditions:     p.ChronicConditions,
		AdmissionWarnings:     p.AdmissionWarnings,
		InsuranceProviderName: p.InsuranceProviderName,
		InsurancePolicyNumber: p.InsurancePolicyNumber,
		ReferringDentistName:  p.ReferringDentistName,
		PrimaryDentistName:    p.PrimaryDentistName,
		CreatedAt:             domain.TimeNow(),
		UpdatedAt:             domain.TimeNow(),
	}
	f.dentalDetails[dd.SubjectID] = dd
	return cloneDental(dd), nil
}

func (f *fakeRepo) UpdateDentalDetails(_ context.Context, subjectID uuid.UUID, p UpdateDentalDetailsParams) (*DentalDetailsRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dd, ok := f.dentalDetails[subjectID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if p.DateOfBirth != nil {
		dd.DateOfBirth = p.DateOfBirth
	}
	if p.Sex != nil {
		dd.Sex = p.Sex
	}
	if p.MedicalAlerts != nil {
		dd.MedicalAlerts = p.MedicalAlerts
	}
	if p.Medications != nil {
		dd.Medications = p.Medications
	}
	if p.Allergies != nil {
		dd.Allergies = p.Allergies
	}
	if p.ChronicConditions != nil {
		dd.ChronicConditions = p.ChronicConditions
	}
	if p.AdmissionWarnings != nil {
		dd.AdmissionWarnings = p.AdmissionWarnings
	}
	if p.InsuranceProviderName != nil {
		dd.InsuranceProviderName = p.InsuranceProviderName
	}
	if p.InsurancePolicyNumber != nil {
		dd.InsurancePolicyNumber = p.InsurancePolicyNumber
	}
	if p.ReferringDentistName != nil {
		dd.ReferringDentistName = p.ReferringDentistName
	}
	if p.PrimaryDentistName != nil {
		dd.PrimaryDentistName = p.PrimaryDentistName
	}
	dd.UpdatedAt = domain.TimeNow()
	return cloneDental(dd), nil
}

// ── Clone helpers (prevent mutation of stored records) ────────────────────────

func clone(c *ContactRecord) *ContactRecord {
	cp := *c
	return &cp
}

func cloneSubject(s *SubjectRecord) *SubjectRecord {
	cp := *s
	return &cp
}

func cloneVet(d *VetDetailsRecord) *VetDetailsRecord {
	cp := *d
	return &cp
}

func cloneDental(d *DentalDetailsRecord) *DentalDetailsRecord {
	cp := *d
	return &cp
}

func cloneGeneral(g *GeneralDetailsRecord) *GeneralDetailsRecord {
	cp := *g
	return &cp
}

func (f *fakeRepo) CreateGeneralDetails(_ context.Context, p CreateGeneralDetailsParams) (*GeneralDetailsRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := &GeneralDetailsRecord{
		SubjectID:             p.SubjectID,
		DateOfBirth:           p.DateOfBirth,
		Sex:                   p.Sex,
		MedicalAlerts:         p.MedicalAlerts,
		Medications:           p.Medications,
		Allergies:             p.Allergies,
		ChronicConditions:     p.ChronicConditions,
		AdmissionWarnings:     p.AdmissionWarnings,
		InsuranceProviderName: p.InsuranceProviderName,
		InsurancePolicyNumber: p.InsurancePolicyNumber,
		ReferringProviderName: p.ReferringProviderName,
		PrimaryProviderName:   p.PrimaryProviderName,
		CreatedAt:             domain.TimeNow(),
		UpdatedAt:             domain.TimeNow(),
	}
	f.generalDetails[g.SubjectID] = g
	return cloneGeneral(g), nil
}

func (f *fakeRepo) UpdateGeneralDetails(_ context.Context, subjectID uuid.UUID, p UpdateGeneralDetailsParams) (*GeneralDetailsRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.generalDetails[subjectID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if p.DateOfBirth != nil {
		g.DateOfBirth = p.DateOfBirth
	}
	if p.Sex != nil {
		g.Sex = p.Sex
	}
	if p.MedicalAlerts != nil {
		g.MedicalAlerts = p.MedicalAlerts
	}
	if p.Medications != nil {
		g.Medications = p.Medications
	}
	if p.Allergies != nil {
		g.Allergies = p.Allergies
	}
	if p.ChronicConditions != nil {
		g.ChronicConditions = p.ChronicConditions
	}
	if p.AdmissionWarnings != nil {
		g.AdmissionWarnings = p.AdmissionWarnings
	}
	if p.InsuranceProviderName != nil {
		g.InsuranceProviderName = p.InsuranceProviderName
	}
	if p.InsurancePolicyNumber != nil {
		g.InsurancePolicyNumber = p.InsurancePolicyNumber
	}
	if p.ReferringProviderName != nil {
		g.ReferringProviderName = p.ReferringProviderName
	}
	if p.PrimaryProviderName != nil {
		g.PrimaryProviderName = p.PrimaryProviderName
	}
	g.UpdatedAt = domain.TimeNow()
	return cloneGeneral(g), nil
}

func cloneAgedCare(a *AgedCareDetailsRecord) *AgedCareDetailsRecord {
	cp := *a
	return &cp
}

func (f *fakeRepo) CreateAgedCareDetails(_ context.Context, p CreateAgedCareDetailsParams) (*AgedCareDetailsRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := &AgedCareDetailsRecord{
		SubjectID:            p.SubjectID,
		DateOfBirth:          p.DateOfBirth,
		Sex:                  p.Sex,
		Room:                 p.Room,
		NHINumber:            p.NHINumber,
		MedicareNumber:       p.MedicareNumber,
		Ethnicity:            p.Ethnicity,
		PreferredLanguage:    p.PreferredLanguage,
		MedicalAlerts:        p.MedicalAlerts,
		Medications:          p.Medications,
		Allergies:            p.Allergies,
		ChronicConditions:    p.ChronicConditions,
		CognitiveStatus:      p.CognitiveStatus,
		MobilityStatus:       p.MobilityStatus,
		ContinenceStatus:     p.ContinenceStatus,
		DietNotes:            p.DietNotes,
		AdvanceDirectiveFlag: p.AdvanceDirectiveFlag,
		FundingLevel:         p.FundingLevel,
		AdmissionDate:        p.AdmissionDate,
		PrimaryGPName:        p.PrimaryGPName,
		CreatedAt:            domain.TimeNow(),
		UpdatedAt:            domain.TimeNow(),
	}
	f.agedCareDetails[a.SubjectID] = a
	return cloneAgedCare(a), nil
}

func (f *fakeRepo) UpdateAgedCareDetails(_ context.Context, subjectID uuid.UUID, p UpdateAgedCareDetailsParams) (*AgedCareDetailsRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.agedCareDetails[subjectID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if p.DateOfBirth != nil {
		a.DateOfBirth = p.DateOfBirth
	}
	if p.Sex != nil {
		a.Sex = p.Sex
	}
	if p.Room != nil {
		a.Room = p.Room
	}
	if p.NHINumber != nil {
		a.NHINumber = p.NHINumber
	}
	if p.MedicareNumber != nil {
		a.MedicareNumber = p.MedicareNumber
	}
	if p.Ethnicity != nil {
		a.Ethnicity = p.Ethnicity
	}
	if p.PreferredLanguage != nil {
		a.PreferredLanguage = p.PreferredLanguage
	}
	if p.MedicalAlerts != nil {
		a.MedicalAlerts = p.MedicalAlerts
	}
	if p.Medications != nil {
		a.Medications = p.Medications
	}
	if p.Allergies != nil {
		a.Allergies = p.Allergies
	}
	if p.ChronicConditions != nil {
		a.ChronicConditions = p.ChronicConditions
	}
	if p.CognitiveStatus != nil {
		a.CognitiveStatus = p.CognitiveStatus
	}
	if p.MobilityStatus != nil {
		a.MobilityStatus = p.MobilityStatus
	}
	if p.ContinenceStatus != nil {
		a.ContinenceStatus = p.ContinenceStatus
	}
	if p.DietNotes != nil {
		a.DietNotes = p.DietNotes
	}
	if p.AdvanceDirectiveFlag != nil {
		a.AdvanceDirectiveFlag = *p.AdvanceDirectiveFlag
	}
	if p.FundingLevel != nil {
		a.FundingLevel = p.FundingLevel
	}
	if p.AdmissionDate != nil {
		a.AdmissionDate = p.AdmissionDate
	}
	if p.PrimaryGPName != nil {
		a.PrimaryGPName = p.PrimaryGPName
	}
	a.UpdatedAt = domain.TimeNow()
	return cloneAgedCare(a), nil
}

// ── Subject ↔ contact links ───────────────────────────────────────────────────

func (f *fakeRepo) CreateSubjectContact(
	_ context.Context,
	clinicID uuid.UUID,
	p CreateSubjectContactParams,
) (*SubjectContactRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.subjects[p.SubjectID]
	if !ok || s.ArchivedAt != nil || s.ClinicID != clinicID {
		return nil, domain.ErrNotFound
	}
	c, ok := f.contacts[p.ContactID]
	if !ok || c.ArchivedAt != nil || c.ClinicID != clinicID {
		return nil, domain.ErrNotFound
	}
	for _, existing := range f.subjectContacts {
		if existing.SubjectID == p.SubjectID && existing.ContactID == p.ContactID && existing.Role == p.Role {
			return nil, domain.ErrConflict
		}
	}
	now := domain.TimeNow()
	rec := &SubjectContactRecord{
		SubjectID: p.SubjectID,
		ContactID: p.ContactID,
		Role:      p.Role,
		Note:      p.Note,
		CreatedAt: now,
		UpdatedAt: now,
	}
	f.subjectContacts = append(f.subjectContacts, rec)
	cp := *rec
	return &cp, nil
}

func (f *fakeRepo) DeleteSubjectContact(
	_ context.Context,
	clinicID, subjectID, contactID uuid.UUID,
	role domain.SubjectContactRole,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.subjects[subjectID]
	if !ok || s.ClinicID != clinicID {
		return domain.ErrNotFound
	}
	for i, existing := range f.subjectContacts {
		if existing.SubjectID == subjectID && existing.ContactID == contactID && existing.Role == role {
			f.subjectContacts = append(f.subjectContacts[:i], f.subjectContacts[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeRepo) ListSubjectContacts(
	_ context.Context,
	clinicID, subjectID uuid.UUID,
) ([]*SubjectContactWithContact, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s, ok := f.subjects[subjectID]
	if !ok || s.ClinicID != clinicID || s.ArchivedAt != nil {
		return nil, nil
	}
	var out []*SubjectContactWithContact
	for _, link := range f.subjectContacts {
		if link.SubjectID != subjectID {
			continue
		}
		contact, ok := f.contacts[link.ContactID]
		if !ok || contact.ArchivedAt != nil {
			continue
		}
		out = append(out, &SubjectContactWithContact{
			Role:    link.Role,
			Note:    link.Note,
			Contact: clone(contact),
		})
	}
	return out, nil
}

// ── Access log ────────────────────────────────────────────────────────────────

func (f *fakeRepo) CreateSubjectAccessLog(_ context.Context, p CreateSubjectAccessLogParams) (*SubjectAccessLogRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := &SubjectAccessLogRecord{
		ID:        p.ID,
		SubjectID: p.SubjectID,
		StaffID:   p.StaffID,
		ClinicID:  p.ClinicID,
		Action:    p.Action,
		Purpose:   p.Purpose,
		At:        domain.TimeNow(),
	}
	f.accessLog = append(f.accessLog, rec)
	return rec, nil
}
