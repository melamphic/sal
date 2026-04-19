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

	// Second import must fail — acquisition is already imported.
	_, err = svc.Import(context.Background(), ImportInput{
		AcquisitionID: uuid.MustParse(acq.ID),
		ClinicID:      clinicID,
		StaffID:       staffID,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict on second import, got %v", err)
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
