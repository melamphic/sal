package auth

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
	"github.com/melamphic/sal/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func newTestService(t *testing.T) (*Service, *fakeRepo, *testutil.FakeMailer) {
	t.Helper()
	r := newFakeRepo()
	m := &testutil.FakeMailer{}
	c := testutil.TestCipher(t)
	svc := NewService(r, c, m, testutil.TestJWTSecret, ServiceConfig{
		JWTAccessTTL:  15 * time.Minute,
		JWTRefreshTTL: 720 * time.Hour,
		MagicLinkTTL:  15 * time.Minute,
		AppURL:        "https://app.salvia.test",
	})
	return svc, r, m
}

// seedActiveStaff adds an active staff member to the fake repo and returns
// their email hash (for magic link requests).
func seedActiveStaff(t *testing.T, r *fakeRepo, email string) *staffRow {
	t.Helper()
	c := testutil.TestCipher(t)
	encName, err := c.Encrypt("Dr. Sarah Chen")
	require.NoError(t, err)

	s := &staffRow{
		ID:        uuid.New(),
		ClinicID:  uuid.New(),
		EmailHash: c.Hash(email),
		FullName:  encName,
		Role:      domain.StaffRoleSuperAdmin,
		NoteTier:  domain.NoteTierStandard,
		Status:    domain.StaffStatusActive,
		Perms:     domain.DefaultPermissions(domain.StaffRoleSuperAdmin),
	}
	r.seedStaff(s)
	return s
}

// ── SendMagicLink ─────────────────────────────────────────────────────────────

func TestService_SendMagicLink_KnownEmail_SendsEmail(t *testing.T) {
	t.Parallel()
	svc, r, m := newTestService(t)
	ctx := context.Background()
	email := "sarah@riverside.co.nz"
	seedActiveStaff(t, r, email)

	err := svc.SendMagicLink(ctx, email, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, m.Count("magic_link"), "should send exactly one magic link email")
	last := m.Last()
	require.NotNil(t, last)
	assert.Equal(t, email, last.To)
	assert.Contains(t, last.Data["login_url"], "https://app.salvia.test/auth/verify?token=")
	assert.Equal(t, "Dr.", last.Data["name"], "email should use first name only")
}

func TestService_SendMagicLink_UnknownEmail_SilentSuccess(t *testing.T) {
	t.Parallel()
	// Prevents email enumeration — unknown addresses must not produce an error.
	svc, _, m := newTestService(t)

	err := svc.SendMagicLink(context.Background(), "nobody@nowhere.com", nil)

	require.NoError(t, err, "must return nil even for unknown email")
	assert.Equal(t, 0, m.Count("magic_link"), "must not send email for unknown address")
}

func TestService_SendMagicLink_DeactivatedStaff_SilentSuccess(t *testing.T) {
	t.Parallel()
	svc, r, m := newTestService(t)
	email := "deactivated@clinic.co.nz"
	s := seedActiveStaff(t, r, email)
	s.Status = domain.StaffStatusDeactivated

	err := svc.SendMagicLink(context.Background(), email, nil)

	require.NoError(t, err)
	assert.Equal(t, 0, m.Count("magic_link"), "deactivated staff must not receive a login link")
}

func TestService_SendMagicLink_StoresHashedTokenNotRaw(t *testing.T) {
	t.Parallel()
	svc, r, m := newTestService(t)
	email := "sarah@riverside.co.nz"
	seedActiveStaff(t, r, email)
	_ = m

	require.NoError(t, svc.SendMagicLink(context.Background(), email, nil))

	// The stored token hash must not equal the raw token in the email URL.
	rawToken := extractTokenFromURL(t, m.Last().Data["login_url"])
	storedHash := r.last.createdTokenHash
	assert.NotEqual(t, rawToken, storedHash, "raw token must never be stored — only its hash")
	assert.Equal(t, hashToken(rawToken), storedHash, "stored hash must be SHA-256 of raw token")
}

// ── VerifyMagicLink ───────────────────────────────────────────────────────────

func TestService_VerifyMagicLink_ValidToken_ReturnsTokenPair(t *testing.T) {
	t.Parallel()
	svc, r, m := newTestService(t)
	email := "james@riverside.co.nz"
	staff := seedActiveStaff(t, r, email)
	require.NoError(t, svc.SendMagicLink(context.Background(), email, nil))

	rawToken := extractTokenFromURL(t, m.Last().Data["login_url"])
	pair, err := svc.VerifyMagicLink(context.Background(), rawToken)

	require.NoError(t, err)
	require.NotNil(t, pair)
	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.True(t, pair.ExpiresAt.After(time.Now()), "access token should not already be expired")

	// Decode the JWT and verify claims.
	claims := &mw.Claims{}
	_, err = jwt.ParseWithClaims(pair.AccessToken, claims, func(*jwt.Token) (interface{}, error) {
		return testutil.TestJWTSecret, nil
	})
	require.NoError(t, err)
	assert.Equal(t, staff.ID, claims.StaffID)
	assert.Equal(t, staff.ClinicID, claims.ClinicID)
	assert.Equal(t, staff.Role, claims.Role)
}

func TestService_VerifyMagicLink_TokenReplayBlocked(t *testing.T) {
	t.Parallel()
	svc, r, m := newTestService(t)
	email := "james@riverside.co.nz"
	seedActiveStaff(t, r, email)
	require.NoError(t, svc.SendMagicLink(context.Background(), email, nil))

	rawToken := extractTokenFromURL(t, m.Last().Data["login_url"])

	// First use: should succeed.
	_, err := svc.VerifyMagicLink(context.Background(), rawToken)
	require.NoError(t, err)

	// Second use: should be blocked.
	_, err = svc.VerifyMagicLink(context.Background(), rawToken)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenUsed)
}

func TestService_VerifyMagicLink_ExpiredToken_Rejected(t *testing.T) {
	t.Parallel()
	svc, r, _ := newTestService(t)
	email := "priya@riverside.co.nz"
	s := seedActiveStaff(t, r, email)

	// Manually insert an already-expired token.
	raw, hash, err := generateOpaqueToken()
	require.NoError(t, err)
	require.NoError(t, r.CreateAuthToken(
		context.Background(), s.ID, hash, "magic_link", "",
		time.Now().Add(-1*time.Hour), // expired an hour ago
	))

	_, err = svc.VerifyMagicLink(context.Background(), raw)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenExpired)
}

func TestService_VerifyMagicLink_UnknownToken_Rejected(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	_, err := svc.VerifyMagicLink(context.Background(), "completely-fake-token")
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestService_VerifyMagicLink_RefreshTokenCantBeUsedAsMagicLink(t *testing.T) {
	t.Parallel()
	svc, r, m := newTestService(t)
	email := "tom@riverside.co.nz"
	seedActiveStaff(t, r, email)
	require.NoError(t, svc.SendMagicLink(context.Background(), email, nil))

	// Get a legitimate token pair to extract the refresh token.
	rawMagic := extractTokenFromURL(t, m.Last().Data["login_url"])
	pair, err := svc.VerifyMagicLink(context.Background(), rawMagic)
	require.NoError(t, err)

	// Try to use the refresh token as a magic link.
	_, err = svc.VerifyMagicLink(context.Background(), pair.RefreshToken)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenInvalid)
}

// ── RefreshTokens ─────────────────────────────────────────────────────────────

func TestService_RefreshTokens_ValidToken_IssuesNewPair(t *testing.T) {
	t.Parallel()
	svc, r, m := newTestService(t)
	email := "emma@riverside.co.nz"
	seedActiveStaff(t, r, email)
	require.NoError(t, svc.SendMagicLink(context.Background(), email, nil))

	rawMagic := extractTokenFromURL(t, m.Last().Data["login_url"])
	firstPair, err := svc.VerifyMagicLink(context.Background(), rawMagic)
	require.NoError(t, err)

	// Refresh using the refresh token.
	newPair, err := svc.RefreshTokens(context.Background(), firstPair.RefreshToken)
	require.NoError(t, err)
	assert.NotEmpty(t, newPair.AccessToken)
	assert.NotEmpty(t, newPair.RefreshToken)
	// Refresh token must rotate — access token may be identical if issued in the same second.
	assert.NotEqual(t, firstPair.RefreshToken, newPair.RefreshToken)
}

func TestService_RefreshTokens_OldRefreshTokenInvalidatedAfterUse(t *testing.T) {
	t.Parallel()
	svc, r, m := newTestService(t)
	email := "helen@riverside.co.nz"
	seedActiveStaff(t, r, email)
	require.NoError(t, svc.SendMagicLink(context.Background(), email, nil))

	rawMagic := extractTokenFromURL(t, m.Last().Data["login_url"])
	firstPair, err := svc.VerifyMagicLink(context.Background(), rawMagic)
	require.NoError(t, err)

	// Use refresh token once.
	_, err = svc.RefreshTokens(context.Background(), firstPair.RefreshToken)
	require.NoError(t, err)

	// Using it again must fail.
	_, err = svc.RefreshTokens(context.Background(), firstPair.RefreshToken)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenUsed)
}

func TestService_RefreshTokens_MagicLinkCantBeUsedAsRefresh(t *testing.T) {
	t.Parallel()
	svc, r, m := newTestService(t)
	email := "sarah@riverside.co.nz"
	seedActiveStaff(t, r, email)
	require.NoError(t, svc.SendMagicLink(context.Background(), email, nil))
	rawMagic := extractTokenFromURL(t, m.Last().Data["login_url"])

	// Attempt to use the magic link token as a refresh token.
	_, err := svc.RefreshTokens(context.Background(), rawMagic)
	// The magic link token should either be invalid type or not found after first use
	require.Error(t, err)
}

// ── Logout ────────────────────────────────────────────────────────────────────

func TestService_Logout_DeletesRefreshTokens(t *testing.T) {
	t.Parallel()
	svc, r, m := newTestService(t)
	email := "sarah@riverside.co.nz"
	staff := seedActiveStaff(t, r, email)
	require.NoError(t, svc.SendMagicLink(context.Background(), email, nil))

	rawMagic := extractTokenFromURL(t, m.Last().Data["login_url"])
	pair, err := svc.VerifyMagicLink(context.Background(), rawMagic)
	require.NoError(t, err)

	// Logout.
	require.NoError(t, svc.Logout(context.Background(), staff.ID))

	// Refresh token should now be gone.
	_, err = svc.RefreshTokens(context.Background(), pair.RefreshToken)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// extractTokenFromURL parses the raw token from a magic link URL.
func extractTokenFromURL(t *testing.T, url string) string {
	t.Helper()
	const prefix = "token="
	idx := indexStr(url, prefix)
	require.GreaterOrEqual(t, idx, 0, "url %q does not contain token=", url)
	return url[idx+len(prefix):]
}

func indexStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
