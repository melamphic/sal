package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ─── Pack listings: publish + import ─────────────────────────────────────────

func TestService_CreateListing_Pack_RequiresAtLeastTwoForms(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)

	_, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "Pack",
		Slug:               "pack",
		ShortDescription:   "x",
		PricingType:        "free",
		BundleType:         "pack",
		SourceFormIDs:      []uuid.UUID{uuid.New()}, // only 1
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("expected ErrValidation for pack with <2 forms, got %v", err)
	}
}

func TestService_PublishVersion_Pack_BuildsFormsArray(t *testing.T) {
	t.Parallel()
	svc, repo, snap, _ := newTestService(t)
	pub := seedPublisher(t, repo)

	// Two source tenant forms with snapshots.
	form1 := uuid.New()
	form2 := uuid.New()
	snap.snapshots[form1] = &FormSnapshot{
		
		FormVersionID: uuid.New(),
		Name:          "Vaccination consent",
		Tags:          []string{"vet"},
		Fields: []FormSnapshotField{
			{Position: 1, Title: "Patient name", Type: "text", Config: json.RawMessage(`{}`)},
			{Position: 2, Title: "DOB", Type: "date", Config: json.RawMessage(`{}`)},
		},
	}
	snap.snapshots[form2] = &FormSnapshot{
		
		FormVersionID: uuid.New(),
		Name:          "Recall reminder",
		Tags:          []string{"vet"},
		Fields: []FormSnapshotField{
			{Position: 1, Title: "Recall date", Type: "date", Config: json.RawMessage(`{}`)},
		},
	}

	// Create the pack listing — the service will write the pack composition
	// to the listing_forms table.
	resp, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "Vaccination workflow pack",
		Slug:               "vax-pack",
		ShortDescription:   "Consent + recall",
		PricingType:        "free",
		BundleType:         "pack",
		SourceFormIDs:      []uuid.UUID{form1, form2},
	})
	if err != nil {
		t.Fatalf("CreateListing pack: %v", err)
	}
	listingID := uuid.MustParse(resp.ID)

	// Publish a version off the pack — service should snapshot both forms
	// and produce a Package with Forms array (not Fields) populated.
	versionResp, err := svc.PublishVersion(context.Background(), PublishVersionInput{
		ListingID:  listingID,
		ClinicID:   pub.ClinicID,
		StaffID:    uuid.New(),
		ChangeType: "major",
	})
	if err != nil {
		t.Fatalf("PublishVersion pack: %v", err)
	}
	if versionResp == nil {
		t.Fatal("expected version response")
	}

	// Inspect the persisted version's payload to confirm pack shape.
	versionID := uuid.MustParse(versionResp.ID)
	v, err := repo.GetVersionByID(context.Background(), versionID)
	if err != nil {
		t.Fatalf("GetVersionByID: %v", err)
	}
	var pkg Package
	if err := json.Unmarshal(v.PackagePayload, &pkg); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if pkg.Meta.SchemaVersion != "2" {
		t.Errorf("pack payloads use schema_version=2, got %q", pkg.Meta.SchemaVersion)
	}
	if pkg.Meta.BundleType != "pack" {
		t.Errorf("expected bundle_type=pack in payload, got %q", pkg.Meta.BundleType)
	}
	if len(pkg.Forms) != 2 {
		t.Fatalf("expected 2 forms in payload, got %d", len(pkg.Forms))
	}
	if pkg.Forms[0].Position != 1 || pkg.Forms[1].Position != 2 {
		t.Errorf("forms not in pack-position order: %v", []int{pkg.Forms[0].Position, pkg.Forms[1].Position})
	}
	if len(pkg.Forms[0].Fields) != 2 || len(pkg.Forms[1].Fields) != 1 {
		t.Errorf("per-form field counts wrong: %d / %d",
			len(pkg.Forms[0].Fields), len(pkg.Forms[1].Fields))
	}
	if v.FieldCount != 3 {
		t.Errorf("expected total field_count=3, got %d", v.FieldCount)
	}
	if len(pkg.Fields) != 0 {
		t.Errorf("pack payloads should not populate top-level Fields, got %d", len(pkg.Fields))
	}
}

func TestService_Import_Pack_MaterialisesAllForms(t *testing.T) {
	t.Parallel()
	svc, repo, snap, imp := newTestService(t)
	pub := seedPublisher(t, repo)

	form1 := uuid.New()
	form2 := uuid.New()
	snap.snapshots[form1] = &FormSnapshot{
		
		FormVersionID: uuid.New(),
		Name:          "Form A",
		Fields:        []FormSnapshotField{{Position: 1, Title: "a", Type: "text", Config: json.RawMessage(`{}`)}},
	}
	snap.snapshots[form2] = &FormSnapshot{
		
		FormVersionID: uuid.New(),
		Name:          "Form B",
		Fields:        []FormSnapshotField{{Position: 1, Title: "b", Type: "text", Config: json.RawMessage(`{}`)}},
	}

	// Create + publish a pack listing.
	resp, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "Test pack",
		Slug:               "test-pack",
		ShortDescription:   "x",
		PricingType:        "free",
		BundleType:         "pack",
		SourceFormIDs:      []uuid.UUID{form1, form2},
	})
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	listingID := uuid.MustParse(resp.ID)
	if _, err := svc.PublishVersion(context.Background(), PublishVersionInput{
		ListingID:  listingID,
		ClinicID:   pub.ClinicID,
		StaffID:    uuid.New(),
		ChangeType: "major",
	}); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}
	if _, err := svc.PublishListingByOwner(context.Background(), pub.ClinicID, listingID); err != nil {
		t.Fatalf("PublishListingByOwner: %v", err)
	}

	buyerClinic := uuid.New()
	buyerStaff := uuid.New()
	acq, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: listingID,
		ClinicID:  buyerClinic,
		StaffID:   buyerStaff,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Reset importer call log so we count ONLY this import's calls.
	imp.allInputs = nil
	imp.lastInput = FormImportInput{}

	importResp, err := svc.Import(context.Background(), ImportInput{
		AcquisitionID: uuid.MustParse(acq.ID),
		ClinicID:      buyerClinic,
		StaffID:       buyerStaff,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if len(imp.allInputs) != 2 {
		t.Fatalf("expected 2 ImportForm calls for pack, got %d", len(imp.allInputs))
	}
	// Both forms must share the same source_marketplace_acquisition_id so
	// the upgrade/lineage queries can scope to the pack as a unit.
	if imp.allInputs[0].SourceMarketplaceAcquisitionID !=
		imp.allInputs[1].SourceMarketplaceAcquisitionID {
		t.Error("pack-imported forms do not share acquisition id")
	}
	if imp.allInputs[0].Name != "Form A" || imp.allInputs[1].Name != "Form B" {
		t.Errorf("pack forms imported out of order: %s, %s",
			imp.allInputs[0].Name, imp.allInputs[1].Name)
	}
	if importResp.ImportedFormID == nil {
		t.Error("acquisition's imported_form_id should point at the first form")
	}
}

// ─── Policy-only listings: publish + import ──────────────────────────────────

func TestService_CreateListing_PolicyOnly_RequiresSourcePolicyID(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)

	_, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "Policy-only",
		Slug:               "policy-only",
		ShortDescription:   "x",
		PricingType:        "free",
		BundleType:         "policy_only",
		// SourcePolicyID intentionally nil
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("expected ErrValidation for policy_only without source, got %v", err)
	}
}

func TestService_PublishVersion_PolicyOnly_BuildsPolicyPayload(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)

	policyID := uuid.New()
	// Reach in and seed the fake policy snapshotter via the service's
	// adapter — easier than threading a returnPolicy through. We use the
	// service's policy snap reference directly.
	if ps, ok := svc.policySnap.(*fakePolicySnapshotter); ok {
		ps.snapshots[policyID] = &PolicySnapshot{
			PolicyID:    policyID,
			Name:        "Sterile field policy",
			Description: nil,
			Content:     json.RawMessage(`[{"type":"paragraph","children":[]}]`),
			Clauses: []PolicySnapshotClause{
				{BlockID: "b1", Title: "Pre-op", Body: "Hand hygiene"},
			},
		}
	} else {
		t.Skip("policy snapshotter is not the fake — cannot seed")
	}

	resp, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "Sterile field guidance",
		Slug:               "sterile-field",
		ShortDescription:   "x",
		PricingType:        "free",
		BundleType:         "policy_only",
		SourcePolicyID:     &policyID,
	})
	if err != nil {
		t.Fatalf("CreateListing policy_only: %v", err)
	}
	listingID := uuid.MustParse(resp.ID)

	versionResp, err := svc.PublishVersion(context.Background(), PublishVersionInput{
		ListingID:  listingID,
		ClinicID:   pub.ClinicID,
		StaffID:    uuid.New(),
		ChangeType: "major",
	})
	if err != nil {
		t.Fatalf("PublishVersion policy_only: %v", err)
	}

	v, err := repo.GetVersionByID(context.Background(), uuid.MustParse(versionResp.ID))
	if err != nil {
		t.Fatalf("GetVersionByID: %v", err)
	}
	var pkg Package
	if err := json.Unmarshal(v.PackagePayload, &pkg); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if pkg.Policy == nil {
		t.Fatal("policy_only payloads must populate Policy")
	}
	if pkg.Policy.Name != "Sterile field policy" {
		t.Errorf("policy name wrong: %q", pkg.Policy.Name)
	}
	if len(pkg.Policy.Clauses) != 1 {
		t.Errorf("expected 1 clause, got %d", len(pkg.Policy.Clauses))
	}
	if v.FieldCount != 0 {
		t.Errorf("policy_only versions have zero fields, got %d", v.FieldCount)
	}
	if len(pkg.Fields) != 0 || len(pkg.Forms) != 0 {
		t.Errorf("policy_only payloads should leave Fields/Forms empty")
	}
}

func TestService_Import_PolicyOnly_MaterialisesPolicyNotForm(t *testing.T) {
	t.Parallel()
	svc, repo, _, imp := newTestService(t)
	pub := seedPublisher(t, repo)

	policyID := uuid.New()
	if ps, ok := svc.policySnap.(*fakePolicySnapshotter); ok {
		ps.snapshots[policyID] = &PolicySnapshot{
			PolicyID: policyID,
			Name:     "Test policy",
			Content:  json.RawMessage(`[]`),
		}
	}

	resp, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "Policy listing",
		Slug:               "policy-listing",
		ShortDescription:   "x",
		PricingType:        "free",
		BundleType:         "policy_only",
		SourcePolicyID:     &policyID,
	})
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	listingID := uuid.MustParse(resp.ID)
	if _, err := svc.PublishVersion(context.Background(), PublishVersionInput{
		ListingID:  listingID,
		ClinicID:   pub.ClinicID,
		StaffID:    uuid.New(),
		ChangeType: "major",
	}); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}
	if _, err := svc.PublishListingByOwner(context.Background(), pub.ClinicID, listingID); err != nil {
		t.Fatalf("PublishListingByOwner: %v", err)
	}

	buyerClinic := uuid.New()
	buyerStaff := uuid.New()
	acq, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: listingID,
		ClinicID:  buyerClinic,
		StaffID:   buyerStaff,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	imp.allInputs = nil
	polImp, _ := svc.policyImporter.(*fakePolicyImporter)
	if polImp != nil {
		polImp.imported = nil
	}

	importResp, err := svc.Import(context.Background(), ImportInput{
		AcquisitionID:             uuid.MustParse(acq.ID),
		ClinicID:                  buyerClinic,
		StaffID:                   buyerStaff,
		AcceptedPolicyAttribution: true, // policy_only requires it
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if len(imp.allInputs) != 0 {
		t.Errorf("policy_only imports must not create a tenant form, got %d ImportForm calls", len(imp.allInputs))
	}
	if polImp != nil && len(polImp.imported) != 1 {
		t.Errorf("expected 1 ImportPolicy call, got %d", len(polImp.imported))
	}
	if importResp.ImportedFormID != nil {
		t.Error("policy_only acquisition.imported_form_id must stay nil")
	}
}

func TestService_Import_PolicyOnly_RequiresAttribution(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newTestService(t)
	pub := seedPublisher(t, repo)

	policyID := uuid.New()
	if ps, ok := svc.policySnap.(*fakePolicySnapshotter); ok {
		ps.snapshots[policyID] = &PolicySnapshot{
			PolicyID: policyID,
			Name:     "Test policy",
			Content:  json.RawMessage(`[]`),
		}
	}
	resp, err := svc.CreateListing(context.Background(), CreateListingInput{
		CallerClinicID:     pub.ClinicID,
		PublisherAccountID: pub.ID,
		Vertical:           "veterinary",
		Name:               "p",
		Slug:               "p-attr",
		ShortDescription:   "x",
		PricingType:        "free",
		BundleType:         "policy_only",
		SourcePolicyID:     &policyID,
	})
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	listingID := uuid.MustParse(resp.ID)
	if _, err := svc.PublishVersion(context.Background(), PublishVersionInput{
		ListingID: listingID, ClinicID: pub.ClinicID, StaffID: uuid.New(), ChangeType: "major",
	}); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}
	if _, err := svc.PublishListingByOwner(context.Background(), pub.ClinicID, listingID); err != nil {
		t.Fatalf("PublishListingByOwner: %v", err)
	}

	buyerClinic := uuid.New()
	acq, err := svc.Acquire(context.Background(), AcquireInput{
		ListingID: listingID, ClinicID: buyerClinic, StaffID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	_, err = svc.Import(context.Background(), ImportInput{
		AcquisitionID:             uuid.MustParse(acq.ID),
		ClinicID:                  buyerClinic,
		StaffID:                   uuid.New(),
		AcceptedPolicyAttribution: false,
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden when attribution not accepted, got %v", err)
	}
}
