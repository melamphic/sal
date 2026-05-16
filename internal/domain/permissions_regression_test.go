package domain

import "testing"

// Regression: Vet + VetNurse default permissions changed from
// ViewOwnPatients → ViewAllPatients (2026-05-14). Clinical staff in a
// real clinic need to see every chart on the day's roster, not just
// patients they personally created — receptionist-created patients
// would otherwise be invisible to the treating vet. ViewOwnPatients
// stays available as an opt-in (admin can downgrade a locum) but is
// not the default.
//
// Don't quietly revert this — clinic onboarding deadlocks without it.

func TestRegression_DefaultPermissions_VetSeesAllPatients(t *testing.T) {
	p := DefaultPermissions(StaffRoleVet)
	if !p.ViewAllPatients {
		t.Error("Vet default must include ViewAllPatients")
	}
	if p.ViewOwnPatients {
		t.Error("Vet default should NOT include ViewOwnPatients (set ViewAllPatients instead)")
	}
}

func TestRegression_DefaultPermissions_VetNurseSeesAllPatients(t *testing.T) {
	p := DefaultPermissions(StaffRoleVetNurse)
	if !p.ViewAllPatients {
		t.Error("VetNurse default must include ViewAllPatients")
	}
	if p.ViewOwnPatients {
		t.Error("VetNurse default should NOT include ViewOwnPatients")
	}
}

func TestRegression_DefaultPermissions_ReceptionistKeepsViewAllPatients(t *testing.T) {
	// Receptionist creates patients; needs to see everyone they enter.
	// Unchanged by the 2026-05-14 flip but pinned here so a future
	// "tighten reception perms" change doesn't break onboarding.
	p := DefaultPermissions(StaffRoleReceptionist)
	if !p.ViewAllPatients {
		t.Error("Receptionist default must keep ViewAllPatients")
	}
}
