package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Handoff test fixtures ─────────────────────────────────────────────────────

var testHandoffSecret = []byte("mel-handoff-shared-secret-32-bytes-ok!")

// fakeHandoffProvisioner is a minimal HandoffProvisioner that returns a
// pre-seeded staff row for the happy path and records the received input
// for assertions. Individual tests can override its behavior.
type fakeHandoffProvisioner struct {
	mu        sync.Mutex
	called    int
	lastInput HandoffProvisionInput
	clinicID  uuid.UUID
	staffID   uuid.UUID
	err       error
}

func (p *fakeHandoffProvisioner) ProvisionFromHandoff(_ context.Context, in HandoffProvisionInput) (uuid.UUID, uuid.UUID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.called++
	p.lastInput = in
	if p.err != nil {
		return uuid.Nil, uuid.Nil, p.err
	}
	return p.clinicID, p.staffID, nil
}

func newHandoffService(t *testing.T) (*Service, *fakeHandoffProvisioner, *staffRow) {
	t.Helper()
	svc, repo, _ := newTestService(t)
	c := testutil.TestCipher(t)

	clinicID := uuid.New()
	encName, err := c.Encrypt("Jane Founder")
	require.NoError(t, err)
	staff := &staffRow{
		ID:        uuid.New(),
		ClinicID:  clinicID,
		EmailHash: c.Hash("founder@newclinic.test"),
		FullName:  encName,
		Role:      domain.StaffRoleSuperAdmin,
		NoteTier:  domain.NoteTierStandard,
		Status:    domain.StaffStatusActive,
		Perms:     domain.DefaultPermissions(domain.StaffRoleSuperAdmin),
	}
	repo.seedStaff(staff)

	prov := &fakeHandoffProvisioner{clinicID: clinicID, staffID: staff.ID}
	svc.SetMelHandoff(testHandoffSecret, prov)
	return svc, prov, staff
}

func signHandoff(t *testing.T, claims MelHandoffClaims, secret []byte) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	raw, err := tok.SignedString(secret)
	require.NoError(t, err)
	return raw
}

func validClaims(jti string) MelHandoffClaims {
	return MelHandoffClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email:      "founder@newclinic.test",
		FullName:   "Jane Founder",
		ClinicName: "Riverside Vets",
		Vertical:   domain.VerticalVeterinary,
		PlanCode:   string(domain.PlanPawsPracticeMonthly),
	}
}

// ── Happy path ────────────────────────────────────────────────────────────────

func TestService_HandoffFromMel_ValidToken_IssuesTokenPair(t *testing.T) {
	t.Parallel()
	svc, prov, staff := newHandoffService(t)

	raw := signHandoff(t, validClaims("jti-1"), testHandoffSecret)
	pair, err := svc.HandoffFromMel(context.Background(), raw)

	require.NoError(t, err)
	require.NotNil(t, pair)
	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Equal(t, 1, prov.called)
	assert.Equal(t, "founder@newclinic.test", prov.lastInput.Email)
	assert.Equal(t, "Jane Founder", prov.lastInput.FullName)
	assert.Equal(t, "Riverside Vets", prov.lastInput.ClinicName)
	require.NotNil(t, prov.lastInput.PlanCode)
	assert.Equal(t, domain.PlanPawsPracticeMonthly, *prov.lastInput.PlanCode)
	assert.Equal(t, staff.ID, prov.staffID)
}

func TestService_HandoffFromMel_TrialSignup_NoPlanCode(t *testing.T) {
	t.Parallel()
	svc, prov, _ := newHandoffService(t)
	claims := validClaims("jti-trial")
	claims.PlanCode = ""

	_, err := svc.HandoffFromMel(context.Background(), signHandoff(t, claims, testHandoffSecret))

	require.NoError(t, err)
	assert.Nil(t, prov.lastInput.PlanCode, "trial signup leaves plan_code nil for webhook to fill")
}

// ── Replay / consume-once ─────────────────────────────────────────────────────

func TestService_HandoffFromMel_ReplayedJTI_Blocked(t *testing.T) {
	t.Parallel()
	svc, prov, _ := newHandoffService(t)

	raw := signHandoff(t, validClaims("jti-replay"), testHandoffSecret)
	_, err := svc.HandoffFromMel(context.Background(), raw)
	require.NoError(t, err)

	_, err = svc.HandoffFromMel(context.Background(), raw)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenUsed)
	assert.Equal(t, 1, prov.called, "replay must not trigger a second provisioning call")
}

// ── Token validity ────────────────────────────────────────────────────────────

func TestService_HandoffFromMel_ExpiredToken_Rejected(t *testing.T) {
	t.Parallel()
	svc, prov, _ := newHandoffService(t)
	claims := validClaims("jti-expired")
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-1 * time.Minute))

	_, err := svc.HandoffFromMel(context.Background(), signHandoff(t, claims, testHandoffSecret))

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenExpired)
	assert.Equal(t, 0, prov.called)
}

func TestService_HandoffFromMel_BadSignature_Rejected(t *testing.T) {
	t.Parallel()
	svc, prov, _ := newHandoffService(t)
	raw := signHandoff(t, validClaims("jti-badsig"), []byte("wrong-secret-wrong-secret-32bytes!"))

	_, err := svc.HandoffFromMel(context.Background(), raw)

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenInvalid)
	assert.Equal(t, 0, prov.called)
}

func TestService_HandoffFromMel_MissingJTI_Rejected(t *testing.T) {
	t.Parallel()
	svc, _, _ := newHandoffService(t)
	claims := validClaims("")
	claims.ID = ""

	_, err := svc.HandoffFromMel(context.Background(), signHandoff(t, claims, testHandoffSecret))

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenInvalid)
}

func TestService_HandoffFromMel_UnsupportedVertical_Rejected(t *testing.T) {
	t.Parallel()
	svc, _, _ := newHandoffService(t)
	claims := validClaims("jti-aged")
	claims.Vertical = domain.VerticalAgedCare // excluded per spec-v2-addendum §1.1

	_, err := svc.HandoffFromMel(context.Background(), signHandoff(t, claims, testHandoffSecret))

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenInvalid)
}

func TestService_HandoffFromMel_UnknownPlanCode_Rejected(t *testing.T) {
	t.Parallel()
	svc, prov, _ := newHandoffService(t)
	claims := validClaims("jti-badplan")
	claims.PlanCode = "phantom_plan_monthly"

	_, err := svc.HandoffFromMel(context.Background(), signHandoff(t, claims, testHandoffSecret))

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenInvalid)
	assert.Equal(t, 0, prov.called, "unknown plan must reject before provisioning")
}

func TestService_HandoffFromMel_MissingRequiredField_Rejected(t *testing.T) {
	t.Parallel()
	svc, _, _ := newHandoffService(t)
	claims := validClaims("jti-noname")
	claims.FullName = ""

	_, err := svc.HandoffFromMel(context.Background(), signHandoff(t, claims, testHandoffSecret))

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenInvalid)
}

// ── Disabled mode ─────────────────────────────────────────────────────────────

func TestService_HandoffFromMel_DisabledWhenSecretUnset(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t) // handoff never configured

	_, err := svc.HandoffFromMel(context.Background(), "any-token")

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

// ── Provisioner failure ───────────────────────────────────────────────────────

func TestService_HandoffFromMel_ProvisionerError_Propagates(t *testing.T) {
	t.Parallel()
	svc, prov, _ := newHandoffService(t)
	prov.err = errors.New("clinic creation failed")

	_, err := svc.HandoffFromMel(context.Background(), signHandoff(t, validClaims("jti-bust"), testHandoffSecret))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "clinic creation failed")
}
