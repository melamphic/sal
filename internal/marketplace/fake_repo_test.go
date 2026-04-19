package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// fakeRepo is an in-memory implementation of the marketplace repo interface.
// Good enough for unit tests; does not implement tsvector search.
type fakeRepo struct {
	mu                 sync.Mutex
	publishers         map[uuid.UUID]*PublisherRecord
	listings           map[uuid.UUID]*ListingRecord
	versions           map[uuid.UUID]*VersionRecord
	fieldsByVer        map[uuid.UUID][]*VersionFieldRecord
	acquisitions       map[uuid.UUID]*AcquisitionRecord
	publishersByClinic map[uuid.UUID]uuid.UUID // clinic_id → publisher_id
	listingsBySlug     map[string]uuid.UUID    // slug → listing_id
	processedEvents    map[string]bool         // stripe event dedupe
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		publishers:         make(map[uuid.UUID]*PublisherRecord),
		listings:           make(map[uuid.UUID]*ListingRecord),
		versions:           make(map[uuid.UUID]*VersionRecord),
		fieldsByVer:        make(map[uuid.UUID][]*VersionFieldRecord),
		acquisitions:       make(map[uuid.UUID]*AcquisitionRecord),
		publishersByClinic: make(map[uuid.UUID]uuid.UUID),
		listingsBySlug:     make(map[string]uuid.UUID),
	}
}

func (r *fakeRepo) CreatePublisher(_ context.Context, p CreatePublisherParams) (*PublisherRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.publishersByClinic[p.ClinicID]; exists {
		return nil, domain.ErrConflict
	}
	now := time.Now().UTC()
	rec := &PublisherRecord{
		ID:            p.ID,
		ClinicID:      p.ClinicID,
		DisplayName:   p.DisplayName,
		Bio:           p.Bio,
		WebsiteURL:    p.WebsiteURL,
		AuthorityType: p.AuthorityType,
		Status:        p.Status,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	r.publishers[p.ID] = rec
	r.publishersByClinic[p.ClinicID] = p.ID
	return rec, nil
}

func (r *fakeRepo) GetPublisherByID(_ context.Context, id uuid.UUID) (*PublisherRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.publishers[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return p, nil
}

func (r *fakeRepo) GetPublisherByClinicID(_ context.Context, clinicID uuid.UUID) (*PublisherRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.publishersByClinic[clinicID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return r.publishers[id], nil
}

func (r *fakeRepo) CreateListing(_ context.Context, p CreateListingParams) (*ListingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.listingsBySlug[p.Slug]; exists {
		return nil, domain.ErrConflict
	}
	publisher, ok := r.publishers[p.PublisherAccountID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	now := time.Now().UTC()
	bundleType := p.BundleType
	if bundleType == "" {
		bundleType = "bundled"
	}
	rec := &ListingRecord{
		ID:                     p.ID,
		PublisherAccountID:     p.PublisherAccountID,
		Vertical:               p.Vertical,
		Name:                   p.Name,
		Slug:                   p.Slug,
		ShortDescription:       p.ShortDescription,
		LongDescription:        p.LongDescription,
		Tags:                   append([]string{}, p.Tags...),
		BundleType:             bundleType,
		PricingType:            p.PricingType,
		PriceCents:             p.PriceCents,
		Currency:               p.Currency,
		Status:                 p.Status,
		PreviewFieldCount:      p.PreviewFieldCount,
		CreatedAt:              now,
		UpdatedAt:              now,
		PublisherDisplayName:   publisher.DisplayName,
		PublisherVerifiedBadge: publisher.VerifiedBadge,
		PublisherAuthorityType: publisher.AuthorityType,
		PublisherClinicID:      publisher.ClinicID,
	}
	r.listings[p.ID] = rec
	r.listingsBySlug[p.Slug] = p.ID
	return rec, nil
}

func (r *fakeRepo) GetListingByID(_ context.Context, id uuid.UUID) (*ListingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.listings[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return l, nil
}

func (r *fakeRepo) GetListingBySlug(_ context.Context, slug string) (*ListingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.listingsBySlug[slug]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return r.listings[id], nil
}

func (r *fakeRepo) ListListings(_ context.Context, p ListListingsParams) ([]*ListingRecord, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var matches []*ListingRecord
	for _, l := range r.listings {
		if l.Status != "published" {
			continue
		}
		if p.Vertical != nil && l.Vertical != *p.Vertical {
			continue
		}
		if p.PricingType != nil && l.PricingType != *p.PricingType {
			continue
		}
		if p.VerifiedOnly && !l.PublisherVerifiedBadge {
			continue
		}
		if p.PolicyLinked != nil {
			hasPolicy := l.BundleType == "form_only"
			if hasPolicy != *p.PolicyLinked {
				continue
			}
		}
		matches = append(matches, l)
	}
	total := len(matches)
	// Deterministic order by CreatedAt desc for tests.
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	start := p.Offset
	if start > len(matches) {
		start = len(matches)
	}
	end := start + p.Limit
	if end > len(matches) {
		end = len(matches)
	}
	return matches[start:end], total, nil
}

func (r *fakeRepo) PublishListing(_ context.Context, id uuid.UUID, now time.Time) (*ListingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.listings[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if l.Status != "draft" && l.Status != "under_review" {
		return nil, domain.ErrNotFound
	}
	l.Status = "published"
	if l.PublishedAt == nil {
		t := now
		l.PublishedAt = &t
	}
	return l, nil
}

func (r *fakeRepo) IncrementDownloadCount(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.listings[id]
	if !ok {
		return domain.ErrNotFound
	}
	l.DownloadCount++
	return nil
}

func (r *fakeRepo) CreateVersion(_ context.Context, p CreateVersionParams) (*VersionRecord, []*VersionFieldRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Enforce (listing_id, major, minor) uniqueness.
	for _, v := range r.versions {
		if v.ListingID == p.ListingID && v.VersionMajor == p.VersionMajor && v.VersionMinor == p.VersionMinor {
			return nil, nil, domain.ErrConflict
		}
	}
	rec := &VersionRecord{
		ID:                  p.ID,
		ListingID:           p.ListingID,
		VersionMajor:        p.VersionMajor,
		VersionMinor:        p.VersionMinor,
		ChangeType:          p.ChangeType,
		ChangeSummary:       p.ChangeSummary,
		PackagePayload:      p.PackagePayload,
		PayloadChecksum:     p.PayloadChecksum,
		FieldCount:          p.FieldCount,
		SourceFormVersionID: p.SourceFormVersionID,
		Status:              "active",
		PublishedAt:         p.PublishedAt,
		PublishedBy:         p.PublishedBy,
		CreatedAt:           p.PublishedAt,
	}
	r.versions[p.ID] = rec

	fieldRecs := make([]*VersionFieldRecord, len(p.Fields))
	for i, f := range p.Fields {
		cfg := f.Config
		if cfg == nil {
			cfg = json.RawMessage(`{}`)
		}
		fieldRecs[i] = &VersionFieldRecord{
			ID:                   f.ID,
			MarketplaceVersionID: p.ID,
			Position:             f.Position,
			Title:                f.Title,
			Type:                 f.Type,
			Config:               cfg,
			AIPrompt:             f.AIPrompt,
			Required:             f.Required,
			Skippable:            f.Skippable,
			AllowInference:       f.AllowInference,
			MinConfidence:        f.MinConfidence,
		}
	}
	r.fieldsByVer[p.ID] = fieldRecs
	return rec, fieldRecs, nil
}

func (r *fakeRepo) GetVersionByID(_ context.Context, id uuid.UUID) (*VersionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.versions[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return v, nil
}

func (r *fakeRepo) GetLatestVersion(_ context.Context, listingID uuid.UUID) (*VersionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best *VersionRecord
	for _, v := range r.versions {
		if v.ListingID != listingID || v.Status != "active" {
			continue
		}
		if best == nil || v.VersionMajor > best.VersionMajor ||
			(v.VersionMajor == best.VersionMajor && v.VersionMinor > best.VersionMinor) {
			best = v
		}
	}
	if best == nil {
		return nil, domain.ErrNotFound
	}
	return best, nil
}

func (r *fakeRepo) ListVersionsByListing(_ context.Context, listingID uuid.UUID) ([]*VersionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*VersionRecord
	for _, v := range r.versions {
		if v.ListingID == listingID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].VersionMajor != out[j].VersionMajor {
			return out[i].VersionMajor > out[j].VersionMajor
		}
		return out[i].VersionMinor > out[j].VersionMinor
	})
	return out, nil
}

func (r *fakeRepo) GetFieldsByVersionID(_ context.Context, versionID uuid.UUID) ([]*VersionFieldRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fields, ok := r.fieldsByVer[versionID]
	if !ok {
		return nil, nil
	}
	return fields, nil
}

func (r *fakeRepo) CreateAcquisition(_ context.Context, p CreateAcquisitionParams) (*AcquisitionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Enforce partial-unique: one active entitlement per (listing, clinic).
	for _, a := range r.acquisitions {
		if a.ListingID == p.ListingID && a.ClinicID == p.ClinicID && a.Status == "active" {
			return nil, domain.ErrConflict
		}
	}
	rec := &AcquisitionRecord{
		ID:                   p.ID,
		ListingID:            p.ListingID,
		MarketplaceVersionID: p.MarketplaceVersionID,
		ClinicID:             p.ClinicID,
		AcquiredBy:           p.AcquiredBy,
		AcquisitionType:      p.AcquisitionType,
		Status:               p.Status,
		FulfilledAt:          p.FulfilledAt,
		CreatedAt:            time.Now().UTC(),
	}
	r.acquisitions[p.ID] = rec
	return rec, nil
}

func (r *fakeRepo) GetAcquisitionByID(_ context.Context, id, clinicID uuid.UUID) (*AcquisitionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.acquisitions[id]
	if !ok || a.ClinicID != clinicID {
		return nil, domain.ErrNotFound
	}
	return a, nil
}

func (r *fakeRepo) ListAcquisitionsByClinic(_ context.Context, clinicID uuid.UUID, limit, offset int) ([]*AcquisitionRecord, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var matches []*AcquisitionRecord
	for _, a := range r.acquisitions {
		if a.ClinicID == clinicID {
			matches = append(matches, a)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	total := len(matches)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return matches[start:end], total, nil
}

func (r *fakeRepo) SetAcquisitionImportedForm(_ context.Context, acquisitionID, clinicID, formID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.acquisitions[acquisitionID]
	if !ok || a.ClinicID != clinicID || a.Status != "active" {
		return domain.ErrNotFound
	}
	a.ImportedFormID = &formID
	return nil
}

// ── Fake implementations of the cross-domain interfaces ──────────────────────

type fakeSnapshotter struct {
	snapshots map[uuid.UUID]*FormSnapshot // form_id → snapshot
	policies  map[uuid.UUID][]uuid.UUID   // form_id → policy_ids
}

func newFakeSnapshotter() *fakeSnapshotter {
	return &fakeSnapshotter{
		snapshots: make(map[uuid.UUID]*FormSnapshot),
		policies:  make(map[uuid.UUID][]uuid.UUID),
	}
}

func (f *fakeSnapshotter) SnapshotForm(_ context.Context, formID, _ uuid.UUID) (*FormSnapshot, error) {
	s, ok := f.snapshots[formID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeSnapshotter) LinkedPolicyIDs(_ context.Context, formID, _ uuid.UUID) ([]uuid.UUID, error) {
	return f.policies[formID], nil
}

type fakeImporter struct {
	called         bool
	lastInput      FormImportInput
	returnedID     uuid.UUID
	err            error
	linkedPolicies []linkedPolicy
}

type linkedPolicy struct {
	formID   uuid.UUID
	clinicID uuid.UUID
	policyID uuid.UUID
	staffID  uuid.UUID
}

func (f *fakeImporter) ImportForm(_ context.Context, in FormImportInput) (uuid.UUID, error) {
	f.called = true
	f.lastInput = in
	if f.err != nil {
		return uuid.Nil, f.err
	}
	if f.returnedID == uuid.Nil {
		f.returnedID = uuid.New()
	}
	return f.returnedID, nil
}

// LinkFormToPolicy satisfies FormImporter.
func (f *fakeImporter) LinkFormToPolicy(_ context.Context, formID, clinicID, policyID, staffID uuid.UUID) error {
	f.linkedPolicies = append(f.linkedPolicies, linkedPolicy{formID, clinicID, policyID, staffID})
	return nil
}

type fakeNamer struct{}

func (f *fakeNamer) GetPolicyNames(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	for _, id := range ids {
		out[id] = "Stub Policy"
	}
	return out, nil
}

// ── Additional repo methods needed by Phase 2/3 service ───────────────────────

// UpdatePublisherStripeConnect stub.
func (r *fakeRepo) UpdatePublisherStripeConnect(_ context.Context, publisherID uuid.UUID, accountID string, onboardingComplete bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.publishers[publisherID]
	if !ok {
		return domain.ErrNotFound
	}
	p.StripeConnectAccountID = &accountID
	p.StripeOnboardingComplete = onboardingComplete
	return nil
}

// GetPublisherByStripeConnectAccountID stub.
func (r *fakeRepo) GetPublisherByStripeConnectAccountID(_ context.Context, accountID string) (*PublisherRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.publishers {
		if p.StripeConnectAccountID != nil && *p.StripeConnectAccountID == accountID {
			return p, nil
		}
	}
	return nil, domain.ErrNotFound
}

// SetPublisherBadge stub.
func (r *fakeRepo) SetPublisherBadge(_ context.Context, publisherID, granterID uuid.UUID, verified bool, authorityType *string, grantedAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.publishers[publisherID]
	if !ok {
		return domain.ErrNotFound
	}
	p.VerifiedBadge = verified
	p.AuthorityType = authorityType
	if authorityType != nil {
		gb := granterID
		p.AuthorityGrantedBy = &gb
		p.AuthorityGrantedAt = grantedAt
	} else {
		p.AuthorityGrantedBy = nil
		p.AuthorityGrantedAt = nil
	}
	return nil
}

// ListPublisherListings stub.
func (r *fakeRepo) ListPublisherListings(_ context.Context, publisherID uuid.UUID, limit, offset int) ([]*ListingRecord, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []*ListingRecord
	for _, l := range r.listings {
		if l.PublisherAccountID == publisherID {
			all = append(all, l)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	total := len(all)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

// UpdateListingStatus stub.
func (r *fakeRepo) UpdateListingStatus(_ context.Context, id uuid.UUID, status string) (*ListingRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.listings[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	l.Status = status
	return l, nil
}

// SetAcquisitionPolicyChoice stub.
func (r *fakeRepo) SetAcquisitionPolicyChoice(_ context.Context, acquisitionID, clinicID uuid.UUID, choice string, acceptedAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.acquisitions[acquisitionID]
	if !ok || a.ClinicID != clinicID {
		return domain.ErrNotFound
	}
	a.PolicyImportChoice = &choice
	a.PolicyAttributionAcceptedAt = acceptedAt
	return nil
}

// FulfillAcquisitionByPaymentIntent stub.
func (r *fakeRepo) FulfillAcquisitionByPaymentIntent(_ context.Context, paymentIntentID string, amountPaidCents, platformFeeCents int, currency string, fulfilledAt time.Time) (*AcquisitionRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.acquisitions {
		if a.StripePaymentIntentID != nil && *a.StripePaymentIntentID == paymentIntentID {
			if a.Status != "pending" {
				return a, false, nil
			}
			a.Status = "active"
			amt := amountPaidCents
			fee := platformFeeCents
			cur := currency
			ft := fulfilledAt
			a.AmountPaidCents = &amt
			a.PlatformFeeCents = &fee
			a.Currency = &cur
			a.FulfilledAt = &ft
			return a, true, nil
		}
	}
	return nil, false, domain.ErrNotFound
}

// RefundAcquisitionByPaymentIntent stub.
func (r *fakeRepo) RefundAcquisitionByPaymentIntent(_ context.Context, paymentIntentID string) (*AcquisitionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.acquisitions {
		if a.StripePaymentIntentID != nil && *a.StripePaymentIntentID == paymentIntentID && a.Status == "active" {
			a.Status = "refunded"
			return a, nil
		}
	}
	return nil, domain.ErrNotFound
}

// CreateReview stub.
func (r *fakeRepo) CreateReview(_ context.Context, p CreateReviewParams) (*ReviewRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &ReviewRecord{
		ID:            p.ID,
		ListingID:     p.ListingID,
		AcquisitionID: p.AcquisitionID,
		ClinicID:      p.ClinicID,
		StaffID:       p.StaffID,
		Rating:        p.Rating,
		Body:          p.Body,
		Status:        "published",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	// Update listing denorm counters.
	if l, ok := r.listings[p.ListingID]; ok {
		l.RatingCount++
		l.RatingSum += p.Rating
	}
	return rec, nil
}

// ListReviewsByListing stub.
func (r *fakeRepo) ListReviewsByListing(_ context.Context, listingID uuid.UUID, limit, offset int) ([]*ReviewRecord, int, error) {
	// Fake repo doesn't store reviews separately — return empty for now.
	_ = listingID
	_ = limit
	_ = offset
	return nil, 0, nil
}

// CreateUpgradeNotificationsForVersion stub.
func (r *fakeRepo) CreateUpgradeNotificationsForVersion(_ context.Context, listingID, newVersionID uuid.UUID, notificationType string) (int, error) {
	// Fake repo: count active acquisitions for the listing and return.
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, a := range r.acquisitions {
		if a.ListingID == listingID && a.Status == "active" {
			count++
		}
	}
	_ = newVersionID
	_ = notificationType
	return count, nil
}

// ListUnseenNotifications stub.
func (r *fakeRepo) ListUnseenNotifications(_ context.Context, _ uuid.UUID, _ int) ([]*UpdateNotificationRecord, error) {
	return nil, nil
}

// MarkNotificationSeen stub.
func (r *fakeRepo) MarkNotificationSeen(_ context.Context, _, _ uuid.UUID, _ time.Time) error {
	return nil
}

// MarkStripeEventProcessed stub — simple in-memory dedupe.
func (r *fakeRepo) MarkStripeEventProcessed(_ context.Context, eventID, _ string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processedEvents == nil {
		r.processedEvents = make(map[string]bool)
	}
	if r.processedEvents[eventID] {
		return false, nil
	}
	r.processedEvents[eventID] = true
	return true, nil
}

// ── fakes for new cross-domain interfaces ─────────────────────────────────────

type fakePolicySnapshotter struct {
	snapshots map[uuid.UUID]*PolicySnapshot
}

func newFakePolicySnapshotter() *fakePolicySnapshotter {
	return &fakePolicySnapshotter{snapshots: make(map[uuid.UUID]*PolicySnapshot)}
}

func (f *fakePolicySnapshotter) SnapshotPolicy(_ context.Context, policyID, _ uuid.UUID) (*PolicySnapshot, error) {
	s, ok := f.snapshots[policyID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}

type fakePolicyImporter struct {
	called   bool
	imported []PolicyImportInput
}

func (f *fakePolicyImporter) ImportPolicy(_ context.Context, in PolicyImportInput) (uuid.UUID, error) {
	f.called = true
	f.imported = append(f.imported, in)
	return uuid.New(), nil
}

type fakeClinicInfo struct {
	status   string
	vertical string
}

func newFakeClinicInfo(status, vertical string) *fakeClinicInfo {
	if status == "" {
		status = "active"
	}
	if vertical == "" {
		vertical = "veterinary"
	}
	return &fakeClinicInfo{status: status, vertical: vertical}
}

func (f *fakeClinicInfo) GetClinicInfo(_ context.Context, _ uuid.UUID) (*ClinicInfo, error) {
	return &ClinicInfo{Status: f.status, Vertical: f.vertical}, nil
}

type fakeStripe struct {
	paymentIntents map[string]string // id → clientSecret
	nextID         int
}

func newFakeStripe() *fakeStripe {
	return &fakeStripe{paymentIntents: make(map[string]string)}
}

func (f *fakeStripe) CreateConnectExpressAccount(_ context.Context, _, _ string) (string, error) {
	return "acct_test", nil
}

func (f *fakeStripe) CreateConnectAccountLink(_ context.Context, _, _, _ string) (string, error) {
	return "https://connect.stripe.com/onboard/test", nil
}

func (f *fakeStripe) CreatePaymentIntent(_ context.Context, _ StripePaymentIntentInput) (string, string, error) {
	f.nextID++
	id := fmt.Sprintf("pi_test_%d", f.nextID)
	secret := id + "_secret"
	f.paymentIntents[id] = secret
	return secret, id, nil
}

func (f *fakeStripe) VerifyAndParseWebhook(payload []byte, _ string) (*StripeEvent, error) {
	// Tests that need webhook routing can bypass this and call service methods
	// directly. This stub returns a minimal event.
	_ = payload
	return &StripeEvent{ID: "evt_stub", Type: "unknown"}, nil
}
