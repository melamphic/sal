package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func newTestService(t *testing.T) (*Service, *fakeRepo, *fakeSnapshotter, *fakeImporter) {
	t.Helper()
	repo := newFakeRepo()
	snap := newFakeSnapshotter()
	polSnap := newFakePolicySnapshotter()
	imp := &fakeImporter{}
	polImp := &fakePolicyImporter{}
	ci := newFakeClinicInfo("active", "veterinary")
	svc := NewService(repo, snap, polSnap, imp, polImp, &fakeNamer{}, ci, newFakeStripe(), ServiceConfig{})
	return svc, repo, snap, imp
}

func seedPublisher(t *testing.T, repo *fakeRepo) *PublisherRecord {
	t.Helper()
	authority := "salvia"
	p, err := repo.CreatePublisher(context.Background(), CreatePublisherParams{
		ID:            uuid.New(),
		ClinicID:      uuid.New(),
		DisplayName:   "Salvia Curated",
		AuthorityType: &authority,
		Status:        "active",
	})
	if err != nil {
		t.Fatalf("seedPublisher: %v", err)
	}
	// Give publisher a verified badge so VerifiedOnly filters work in tests.
	p.VerifiedBadge = true
	return p
}

func seedPublishedListing(t *testing.T, svc *Service, repo *fakeRepo, pub *PublisherRecord, slug, pricing string) *ListingResponse {
	t.Helper()
	var priceCents *int
	if pricing == "paid" {
		p := 1000
		priceCents = &p
	}
	resp, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "Surgical Consent",
		Slug:               slug,
		ShortDescription:   "Consent form",
		PricingType:        pricing,
		PriceCents:         priceCents,
	})
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}

	// Seed a version directly via repo (bypasses snapshotter).
	listingID := uuid.MustParse(resp.ID)
	payload, _ := json.Marshal(Package{Meta: PackageMeta{SchemaVersion: "1"}})
	_, _, err = repo.CreateVersion(context.Background(), CreateVersionParams{
		ID:              uuid.New(),
		ListingID:       listingID,
		VersionMajor:    1,
		VersionMinor:    0,
		ChangeType:      "major",
		PackagePayload:  payload,
		PayloadChecksum: "stub",
		FieldCount:      0,
		PublishedBy:     uuid.New(),
		PublishedAt:     domain.TimeNow(),
	})
	if err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}

	published, err := svc.PublishListing(context.Background(), listingID)
	if err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	return published
}

// seedAdditionalPublishedVersion inserts a second/third/etc. published version
// for an existing listing, returning the new version record. Bypasses the
// snapshotter since unit tests don't need a real source form — just a row that
// satisfies the import flow's foreign-key + status checks.
func seedAdditionalPublishedVersion(t *testing.T, repo *fakeRepo, listingID uuid.UUID, major, minor int) *VersionRecord {
	t.Helper()
	payload, _ := json.Marshal(Package{Meta: PackageMeta{SchemaVersion: "1"}})
	v, _, err := repo.CreateVersion(context.Background(), CreateVersionParams{
		ID:              uuid.New(),
		ListingID:       listingID,
		VersionMajor:    major,
		VersionMinor:    minor,
		ChangeType:      "major",
		PackagePayload:  payload,
		PayloadChecksum: "stub",
		FieldCount:      0,
		PublishedBy:     uuid.New(),
		PublishedAt:     domain.TimeNow(),
	})
	if err != nil {
		t.Fatalf("seedAdditionalPublishedVersion: CreateVersion: %v", err)
	}
	return v
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestService_EnsurePublisher_IdempotentOnRepeatedCalls(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newTestService(t)
	clinicID := uuid.New()

	first, err := svc.EnsurePublisher(context.Background(), clinicID, "Salvia")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.EnsurePublisher(context.Background(), clinicID, "Salvia")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("publisher id changed across idempotent calls: %s vs %s", first.ID, second.ID)
	}
}

func TestService_CreateListing_RejectsPaidWithoutPrice(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)

	_, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "x",
		Slug:               "x",
		ShortDescription:   "x",
		PricingType:        "paid",
		// no PriceCents
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("expected ErrValidation, got %v", err)
	}
}

func TestService_CreateListing_RejectsFreeWithPrice(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)

	cents := 100
	_, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "x",
		Slug:               "x",
		ShortDescription:   "x",
		PricingType:        "free",
		PriceCents:         &cents,
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("expected ErrValidation, got %v", err)
	}
}

func TestService_PublishListing_WithoutVersion_Rejected(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)

	listing, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "x",
		Slug:               "x-noversion",
		ShortDescription:   "x",
		PricingType:        "free",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.PublishListing(context.Background(), uuid.MustParse(listing.ID))
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict publishing listing with zero versions, got %v", err)
	}
}

func TestService_PublishVersion_BuildsPackageWithChecksum(t *testing.T) {
	t.Parallel()
	svc, repo, snap, _ := newTestService(t)
	pub := seedPublisher(t, repo)

	listing, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "Consent",
		Slug:               "consent",
		ShortDescription:   "Consent form",
		PricingType:        "free",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	sourceFormID := uuid.New()
	snap.snapshots[sourceFormID] = &FormSnapshot{
		FormVersionID: uuid.New(),
		Name:          "Consent",
		Tags:          []string{"consent"},
		Fields: []FormSnapshotField{
			{Position: 1, Title: "Procedure", Type: "text", Required: true},
		},
	}
	// No policies linked for this test — bundle_type defaults to 'bundled'
	// but no snapshotter call needed when policies list is empty.

	vResp, err := svc.PublishVersion(context.Background(), PublishVersionInput{
		ListingID:    uuid.MustParse(listing.ID),
		ClinicID:     pub.ClinicID, // must match publisher's clinic
		SourceFormID: sourceFormID,
		StaffID:      uuid.New(),
		ChangeType:   "major",
	})
	if err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}
	if vResp.VersionMajor != 1 || vResp.VersionMinor != 0 {
		t.Errorf("expected v1.0, got v%d.%d", vResp.VersionMajor, vResp.VersionMinor)
	}

	rec, err := repo.GetVersionByID(context.Background(), uuid.MustParse(vResp.ID))
	if err != nil {
		t.Fatalf("GetVersionByID: %v", err)
	}
	if rec.PayloadChecksum == "" {
		t.Error("payload checksum is empty")
	}
	var pkg Package
	if err := json.Unmarshal(rec.PackagePayload, &pkg); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if pkg.Meta.Checksum == "" {
		t.Error("package envelope missing checksum")
	}
	if pkg.Listing.PolicyDependencyCount != 0 {
		t.Errorf("expected PolicyDependencyCount=0 with no linked policies, got %d", pkg.Listing.PolicyDependencyCount)
	}
	if len(pkg.Fields) != 1 {
		t.Errorf("expected 1 field, got %d", len(pkg.Fields))
	}
}

func TestService_Acquire_FreePath_SetsStatusActive(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)
	listing := seedPublishedListing(t, svc, repo, pub, "free-listing", "free")

	acq, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: uuid.MustParse(listing.ID),
		ClinicID:  uuid.New(),
		StaffID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if acq.Status != "active" {
		t.Errorf("expected active, got %s", acq.Status)
	}
	if acq.AcquisitionType != "free" {
		t.Errorf("expected free, got %s", acq.AcquisitionType)
	}
}

func TestService_Acquire_RejectsDuplicateActive(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)
	listing := seedPublishedListing(t, svc, repo, pub, "dupe", "free")
	clinicID := uuid.New()

	_, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: uuid.MustParse(listing.ID),
		ClinicID:  clinicID,
		StaffID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err = svc.Acquire(context.Background(), AcquireInput{
		ListingID: uuid.MustParse(listing.ID),
		ClinicID:  clinicID,
		StaffID:   uuid.New(),
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict on repeat acquire, got %v", err)
	}
}

func TestService_Acquire_PaidListing_Rejected(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)
	listing := seedPublishedListing(t, svc, repo, pub, "paid", "paid")

	_, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: uuid.MustParse(listing.ID),
		ClinicID:  uuid.New(),
		StaffID:   uuid.New(),
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict for paid listing, got %v", err)
	}
}

func TestService_Import_CallsImporter_AndMarksAcquisition(t *testing.T) {
	t.Parallel()
	svc, repo, _, imp := newTestService(t)
	pub := seedPublisher(t, repo)
	listing := seedPublishedListing(t, svc, repo, pub, "imported", "free")
	clinicID := uuid.New()
	staffID := uuid.New()

	acq, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: uuid.MustParse(listing.ID),
		ClinicID:  clinicID,
		StaffID:   staffID,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	newFormID := uuid.New()
	imp.returnedID = newFormID

	importResp, err := svc.Import(context.Background(), ImportInput{
		AcquisitionID: uuid.MustParse(acq.ID),
		ClinicID:      clinicID,
		StaffID:       staffID,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !imp.called {
		t.Fatal("importer never called")
	}
	if imp.lastInput.ClinicID != clinicID {
		t.Errorf("importer got wrong clinic_id")
	}
	if importResp.ImportedFormID == nil || *importResp.ImportedFormID != newFormID.String() {
		t.Errorf("expected imported_form_id=%s, got %v", newFormID, importResp.ImportedFormID)
	}
	// Importer must receive lineage stamps so the resulting tenant form can
	// be linked back to its marketplace origin in the upgrade UX.
	if imp.lastInput.SourceMarketplaceListingID == uuid.Nil {
		t.Errorf("importer missing SourceMarketplaceListingID")
	}
	if imp.lastInput.SourceMarketplaceVersionID == uuid.Nil {
		t.Errorf("importer missing SourceMarketplaceVersionID")
	}
	if imp.lastInput.SourceMarketplaceAcquisitionID != uuid.MustParse(acq.ID) {
		t.Errorf("importer got wrong SourceMarketplaceAcquisitionID")
	}
}

func TestService_Import_ReImport_TargetsSpecificVersion_DismissesNotification(t *testing.T) {
	t.Parallel()
	// Upgrade flow: buyer acquires v1, publisher ships v2, buyer re-imports
	// targeting v2. A NEW tenant form is created (separate from v1) and the
	// matching upgrade notification is dismissed.
	svc, repo, _, imp := newTestService(t)
	pub := seedPublisher(t, repo)
	listing := seedPublishedListing(t, svc, repo, pub, "upgrade-flow", "free")
	clinicID := uuid.New()
	staffID := uuid.New()

	acq, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: uuid.MustParse(listing.ID),
		ClinicID:  clinicID,
		StaffID:   staffID,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	acquisitionID := uuid.MustParse(acq.ID)

	// First import — uses pinned version.
	v1FormID := uuid.New()
	imp.returnedID = v1FormID
	if _, err := svc.Import(context.Background(), ImportInput{
		AcquisitionID: acquisitionID,
		ClinicID:      clinicID,
		StaffID:       staffID,
	}); err != nil {
		t.Fatalf("first Import: %v", err)
	}

	// Publisher ships a new version — manually seed v2 for the same listing
	// since the test harness uses helpers that publish only one version each.
	listingID := uuid.MustParse(listing.ID)
	v2 := seedAdditionalPublishedVersion(t, repo, listingID, 2, 0)

	// Re-import targeting v2.
	v2FormID := uuid.New()
	imp.returnedID = v2FormID
	importResp, err := svc.Import(context.Background(), ImportInput{
		AcquisitionID: acquisitionID,
		ClinicID:      clinicID,
		StaffID:       staffID,
		VersionID:     &v2.ID,
	})
	if err != nil {
		t.Fatalf("re-Import: %v", err)
	}

	// Acquisition's imported_form_id moves to the latest (v2), v1 stays
	// discoverable through forms.source_marketplace_acquisition_id (verified
	// by the form-lineage integration tests).
	if importResp.ImportedFormID == nil || *importResp.ImportedFormID != v2FormID.String() {
		t.Errorf("expected imported_form_id=%s after re-import, got %v", v2FormID, importResp.ImportedFormID)
	}
	if imp.lastInput.SourceMarketplaceVersionID != v2.ID {
		t.Errorf("importer should be stamping v2 ID, got %v", imp.lastInput.SourceMarketplaceVersionID)
	}

	// Notification dismissal was requested for the (acquisition, v2) pair.
	key := acquisitionID.String() + ":" + v2.ID.String()
	if repo.dismissedNotifications[key] != 1 {
		t.Errorf("expected exactly one notification dismissal call for (%s, v2), got %d",
			acquisitionID, repo.dismissedNotifications[key])
	}
}

func TestService_Import_ReImport_VersionFromOtherListing_Forbidden(t *testing.T) {
	t.Parallel()
	// Caller cannot pass a version_id from a listing they did not acquire.
	svc, repo, _, imp := newTestService(t)
	pub := seedPublisher(t, repo)
	listingA := seedPublishedListing(t, svc, repo, pub, "listing-a", "free")
	listingB := seedPublishedListing(t, svc, repo, pub, "listing-b", "free")
	clinicID := uuid.New()
	staffID := uuid.New()

	acq, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: uuid.MustParse(listingA.ID),
		ClinicID:  clinicID,
		StaffID:   staffID,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Steal a version from listingB by querying the repo directly.
	bLatest, err := repo.GetLatestVersion(context.Background(), uuid.MustParse(listingB.ID))
	if err != nil {
		t.Fatalf("GetLatestVersion(listingB): %v", err)
	}
	bVersion := bLatest.ID

	imp.returnedID = uuid.New()
	_, err = svc.Import(context.Background(), ImportInput{
		AcquisitionID: uuid.MustParse(acq.ID),
		ClinicID:      clinicID,
		StaffID:       staffID,
		VersionID:     &bVersion,
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden when version belongs to a different listing, got %v", err)
	}
}

func TestService_Import_CrossTenantAcquisition_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)
	listing := seedPublishedListing(t, svc, repo, pub, "tenant", "free")
	clinicA := uuid.New()
	clinicB := uuid.New()

	acq, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: uuid.MustParse(listing.ID),
		ClinicID:  clinicA,
		StaffID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	_, err = svc.Import(context.Background(), ImportInput{
		AcquisitionID: uuid.MustParse(acq.ID),
		ClinicID:      clinicB,
		StaffID:       uuid.New(),
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound cross-tenant, got %v", err)
	}
}

func TestService_ListListings_AppliesFilters(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)
	seedPublishedListing(t, svc, repo, pub, "a", "free")
	seedPublishedListing(t, svc, repo, pub, "b", "paid")

	vertical := "veterinary"
	pricing := "free"
	resp, err := svc.ListListings(context.Background(), ListListingsInput{
		Vertical:    &vertical,
		PricingType: &pricing,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 free listing, got %d", resp.Total)
	}
}

func TestService_GetVersion_PaidListing_LocksBeyondPreview(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)

	// Create paid listing with preview_field_count=1 and 3 fields.
	cents := 500
	listingResp, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "Locked",
		Slug:               "locked",
		ShortDescription:   "Paid",
		PricingType:        "paid",
		PriceCents:         &cents,
		PreviewFieldCount:  1,
	})
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}

	listingID := uuid.MustParse(listingResp.ID)
	versionID := uuid.New()
	pkg, _ := json.Marshal(Package{Meta: PackageMeta{SchemaVersion: "1"}})
	fields := []CreateVersionFieldParams{
		{ID: uuid.New(), Position: 1, Title: "f1", Type: "text"},
		{ID: uuid.New(), Position: 2, Title: "f2", Type: "text"},
		{ID: uuid.New(), Position: 3, Title: "f3", Type: "text"},
	}
	_, _, err = repo.CreateVersion(context.Background(), CreateVersionParams{
		ID:              versionID,
		ListingID:       listingID,
		VersionMajor:    1,
		VersionMinor:    0,
		ChangeType:      "major",
		PackagePayload:  pkg,
		PayloadChecksum: "x",
		FieldCount:      3,
		PublishedBy:     uuid.New(),
		PublishedAt:     domain.TimeNow(),
		Fields:          fields,
	})
	if err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	if _, err := svc.PublishListing(context.Background(), listingID); err != nil {
		t.Fatalf("PublishListing: %v", err)
	}

	got, err := svc.GetVersion(context.Background(), listingID, versionID)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if len(got.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(got.Fields))
	}
	if got.Fields[0].Locked {
		t.Error("first field should not be locked")
	}
	if !got.Fields[1].Locked || !got.Fields[2].Locked {
		t.Error("fields past preview limit should be locked")
	}
}

func TestNextMarketplaceVersion_BumpsCorrectly(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	listingID := uuid.New()

	// First publish — no previous version.
	major, minor := nextMarketplaceVersion(context.Background(), repo, listingID, "major")
	if major != 1 || minor != 0 {
		t.Errorf("first publish: expected 1.0, got %d.%d", major, minor)
	}

	// Seed v1.0.
	pkg, _ := json.Marshal(Package{})
	_, _, err := repo.CreateVersion(context.Background(), CreateVersionParams{
		ID:              uuid.New(),
		ListingID:       listingID,
		VersionMajor:    1,
		VersionMinor:    0,
		ChangeType:      "major",
		PackagePayload:  pkg,
		PayloadChecksum: "x",
		PublishedBy:     uuid.New(),
		PublishedAt:     domain.TimeNow(),
	})
	if err != nil {
		t.Fatalf("seed v1.0: %v", err)
	}

	// Minor bump.
	major, minor = nextMarketplaceVersion(context.Background(), repo, listingID, "minor")
	if major != 1 || minor != 1 {
		t.Errorf("minor bump from 1.0: expected 1.1, got %d.%d", major, minor)
	}
	// Major bump.
	major, minor = nextMarketplaceVersion(context.Background(), repo, listingID, "major")
	if major != 2 || minor != 0 {
		t.Errorf("major bump from 1.0: expected 2.0, got %d.%d", major, minor)
	}
}

func TestBayesianAverage_PriorBehaviour(t *testing.T) {
	t.Parallel()
	// No ratings — returns prior mean.
	if got := bayesianAverage(0, 0); got != 3.0 {
		t.Errorf("expected 3.0 with no ratings, got %v", got)
	}
	// 2 ratings of 5★ → (5*3 + 10)/(5+2) = 25/7 ≈ 3.571
	got := bayesianAverage(10, 2)
	if got < 3.57 || got > 3.58 {
		t.Errorf("expected ~3.571 for 2x5, got %v", got)
	}
}

func TestChecksumPackage_Deterministic(t *testing.T) {
	t.Parallel()
	pkg := Package{
		Meta:    PackageMeta{SchemaVersion: "1", FormVersion: "1.0", Vertical: "veterinary"},
		Listing: PackageListing{Name: "x", Tags: []string{"a"}},
		Fields:  []PackageField{{Position: 1, Title: "t", Type: "text"}},
	}
	a, err := checksumPackage(pkg)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := checksumPackage(pkg)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a != b {
		t.Errorf("checksum non-deterministic: %s vs %s", a, b)
	}
	if a == "" {
		t.Error("empty checksum")
	}
}
