package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// helper returning the same deps as newTestService but also the ClinicInfo
// fake so tests can flip clinic status mid-run.
func newTestServiceWithClinic(t *testing.T, status string) (*Service, *fakeRepo, *fakeSnapshotter, *fakePolicySnapshotter, *fakeImporter, *fakePolicyImporter) {
	t.Helper()
	repo := newFakeRepo()
	snap := newFakeSnapshotter()
	polSnap := newFakePolicySnapshotter()
	imp := &fakeImporter{}
	polImp := &fakePolicyImporter{}
	ci := newFakeClinicInfo(status, "veterinary")
	svc := NewService(repo, snap, polSnap, imp, polImp, &fakeNamer{}, ci, newFakeStripe(), ServiceConfig{})
	return svc, repo, snap, polSnap, imp, polImp
}

func TestService_TrialClinic_CannotPublish(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newTestServiceWithClinic(t, "trial")
	pub := seedPublisher(t, repo)

	_, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "x",
		Slug:               "trial-block",
		ShortDescription:   "x",
		PricingType:        "free",
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden for trial, got %v", err)
	}
}

func TestService_TrialClinic_CannotPurchase(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newTestServiceWithClinic(t, "trial")
	pub := seedPublisher(t, repo)

	// Publisher clinic is 'active' for seedPublisher; simulate by flipping CI
	// to 'active' briefly to create listing, then 'trial' for purchase.
	// Easier: use a separate purchaseClinic that's in trial.
	trialClinic := uuid.New()

	// Publisher stripe onboarding complete + price set via direct repo seed.
	pub.StripeOnboardingComplete = true
	acct := "acct_test"
	pub.StripeConnectAccountID = &acct

	price := 1000
	// Bypass the active-clinic gate for listing creation by using repo directly.
	_, err := repo.CreateListing(context.Background(), CreateListingParams{
		ID:                 uuid.New(),
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "paid",
		Slug:               "trial-paid",
		ShortDescription:   "paid",
		BundleType:         "bundled",
		PricingType:        "paid",
		PriceCents:         &price,
		Currency:           "NZD",
		Status:             "published",
	})
	if err != nil {
		t.Fatalf("seed listing: %v", err)
	}

	// Seed a version so Purchase can find one.
	listingRec, err := repo.GetListingBySlug(context.Background(), "trial-paid")
	if err != nil {
		t.Fatalf("get listing: %v", err)
	}
	pkg, _ := json.Marshal(Package{})
	_, _, err = repo.CreateVersion(context.Background(), CreateVersionParams{
		ID:              uuid.New(),
		ListingID:       listingRec.ID,
		VersionMajor:    1,
		VersionMinor:    0,
		ChangeType:      "major",
		PackagePayload:  pkg,
		PayloadChecksum: "x",
		PublishedBy:     uuid.New(),
		PublishedAt:     domain.TimeNow(),
	})
	if err != nil {
		t.Fatalf("seed version: %v", err)
	}

	_, err = svc.Purchase(context.Background(), PurchaseInput{
		ListingID: listingRec.ID,
		ClinicID:  trialClinic,
		StaffID:   uuid.New(),
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden for trial purchase, got %v", err)
	}
}

func TestService_PlatformFee_AuthorityZero(t *testing.T) {
	t.Parallel()
	svc, _, _, _, _, _ := newTestServiceWithClinic(t, "active")

	authority := "authority"
	p := &PublisherRecord{AuthorityType: &authority}
	if pct := svc.platformFeePctForPublisher(p); pct != 0 {
		t.Errorf("expected 0%% for authority, got %d", pct)
	}
	salvia := "salvia"
	p2 := &PublisherRecord{AuthorityType: &salvia}
	if pct := svc.platformFeePctForPublisher(p2); pct != 0 {
		t.Errorf("expected 0%% for salvia, got %d", pct)
	}
	// Regular publisher (no authority)
	p3 := &PublisherRecord{}
	if pct := svc.platformFeePctForPublisher(p3); pct != 30 {
		t.Errorf("expected 30%% for regular, got %d", pct)
	}
}

func TestService_Import_WithBundledPolicies(t *testing.T) {
	t.Parallel()
	svc, repo, snap, polSnap, imp, polImp := newTestServiceWithClinic(t, "active")
	pub := seedPublisher(t, repo)

	// Create a bundled-type listing.
	resp, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "with-policy",
		Slug:               "with-policy",
		ShortDescription:   "x",
		BundleType:         "bundled",
		PricingType:        "free",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	listingID := uuid.MustParse(resp.ID)

	// Seed snapshotter with a source form that has 1 linked policy.
	sourceFormID := uuid.New()
	linkedPolicyID := uuid.New()
	snap.snapshots[sourceFormID] = &FormSnapshot{
		FormVersionID: uuid.New(),
		Name:          "Form",
		Fields:        []FormSnapshotField{{Position: 1, Title: "f", Type: "text"}},
	}
	snap.policies[sourceFormID] = []uuid.UUID{linkedPolicyID}
	polSnap.snapshots[linkedPolicyID] = &PolicySnapshot{
		PolicyID: linkedPolicyID,
		Name:     "Bundled Policy",
		Content:  json.RawMessage(`[{"block_id":"b1"}]`),
		Clauses: []PolicySnapshotClause{
			{BlockID: "b1", Title: "Clause 1", Parity: "high"},
		},
	}

	if _, err := svc.PublishVersion(context.Background(), PublishVersionInput{
		ListingID:    listingID,
		ClinicID:     pub.ClinicID,
		SourceFormID: sourceFormID,
		StaffID:      uuid.New(),
		ChangeType:   "major",
	}); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	if _, err := svc.PublishListing(context.Background(), listingID); err != nil {
		t.Fatalf("publish listing: %v", err)
	}

	// Consumer acquires + imports with IncludePolicies=true.
	consumerClinic := uuid.New()
	consumerStaff := uuid.New()
	acq, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: listingID,
		ClinicID:  consumerClinic,
		StaffID:   consumerStaff,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	imp.returnedID = uuid.New()
	importResp, err := svc.Import(context.Background(), ImportInput{
		AcquisitionID:             uuid.MustParse(acq.ID),
		ClinicID:                  consumerClinic,
		StaffID:                   consumerStaff,
		IncludePolicies:           true,
		AcceptedPolicyAttribution: true,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !polImp.called {
		t.Error("policy importer not called")
	}
	if len(polImp.imported) != 1 {
		t.Errorf("expected 1 imported policy, got %d", len(polImp.imported))
	}
	if len(imp.linkedPolicies) != 1 {
		t.Errorf("expected 1 form↔policy link, got %d", len(imp.linkedPolicies))
	}
	if importResp.ImportedFormID == nil {
		t.Error("imported_form_id not set")
	}
}

func TestService_Import_IncludePoliciesRequiresAttribution(t *testing.T) {
	t.Parallel()
	svc, repo, snap, polSnap, imp, _ := newTestServiceWithClinic(t, "active")
	pub := seedPublisher(t, repo)

	resp, _ := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "pol",
		Slug:               "pol",
		ShortDescription:   "x",
		BundleType:         "bundled",
		PricingType:        "free",
	})
	listingID := uuid.MustParse(resp.ID)

	sourceFormID := uuid.New()
	linkedPolicyID := uuid.New()
	snap.snapshots[sourceFormID] = &FormSnapshot{
		FormVersionID: uuid.New(),
		Name:          "Form",
		Fields:        []FormSnapshotField{{Position: 1, Title: "f", Type: "text"}},
	}
	snap.policies[sourceFormID] = []uuid.UUID{linkedPolicyID}
	polSnap.snapshots[linkedPolicyID] = &PolicySnapshot{
		PolicyID: linkedPolicyID,
		Name:     "P",
		Content:  json.RawMessage(`[]`),
		Clauses:  []PolicySnapshotClause{{BlockID: "b", Title: "t", Parity: "high"}},
	}
	if _, err := svc.PublishVersion(context.Background(), PublishVersionInput{
		ListingID: listingID, ClinicID: pub.ClinicID, SourceFormID: sourceFormID,
		StaffID: uuid.New(), ChangeType: "major",
	}); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	if _, err := svc.PublishListing(context.Background(), listingID); err != nil {
		t.Fatalf("publish listing: %v", err)
	}

	consumerClinic := uuid.New()
	acq, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: listingID, ClinicID: consumerClinic, StaffID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	imp.returnedID = uuid.New()
	_, err = svc.Import(context.Background(), ImportInput{
		AcquisitionID:             uuid.MustParse(acq.ID),
		ClinicID:                  consumerClinic,
		StaffID:                   uuid.New(),
		IncludePolicies:           true,
		AcceptedPolicyAttribution: false, // MISSING acknowledgment
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden without attribution, got %v", err)
	}
}

func TestService_SuspendListing_OnlySalvia(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newTestServiceWithClinic(t, "active")

	// Non-salvia caller cannot suspend.
	authority := "authority"
	otherPub, _ := repo.CreatePublisher(context.Background(), CreatePublisherParams{
		ID:            uuid.New(),
		ClinicID:      uuid.New(),
		DisplayName:   "NZVA",
		AuthorityType: &authority,
		Status:        "active",
	})

	// Seed a listing.
	pub := seedPublisher(t, repo)
	listing := seedPublishedListing(t, svc, repo, pub, "suspendable", "free")

	_, err := svc.SuspendListing(context.Background(), otherPub.ClinicID, uuid.MustParse(listing.ID))
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden for non-salvia caller, got %v", err)
	}

	// Salvia publisher can suspend.
	resp, err := svc.SuspendListing(context.Background(), pub.ClinicID, uuid.MustParse(listing.ID))
	if err != nil {
		t.Fatalf("salvia suspend: %v", err)
	}
	if resp.Status != "suspended" {
		t.Errorf("expected suspended, got %s", resp.Status)
	}
}

func TestService_BadgeGrant_OnlySalviaGrantsAuthority(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newTestServiceWithClinic(t, "active")

	// Non-salvia caller cannot grant authority.
	authority := "authority"
	caller, _ := repo.CreatePublisher(context.Background(), CreatePublisherParams{
		ID:            uuid.New(),
		ClinicID:      uuid.New(),
		DisplayName:   "NZVA",
		AuthorityType: &authority,
		Status:        "active",
	})
	target, _ := repo.CreatePublisher(context.Background(), CreatePublisherParams{
		ID:          uuid.New(),
		ClinicID:    uuid.New(),
		DisplayName: "College",
		Status:      "active",
	})

	newAuth := "authority"
	_, err := svc.GrantBadge(context.Background(), GrantBadgeInput{
		GranterClinicID:   caller.ClinicID,
		TargetPublisherID: target.ID,
		VerifiedBadge:     true,
		AuthorityType:     &newAuth,
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden when authority tries to grant authority, got %v", err)
	}
}
