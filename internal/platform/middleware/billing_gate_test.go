package middleware

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// fakeStatusReader returns a fixed status (or error) for any clinic id.
type fakeStatusReader struct {
	status domain.ClinicStatus
	err    error
	calls  int
}

func (f *fakeStatusReader) GetStatus(_ context.Context, _ uuid.UUID) (domain.ClinicStatus, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.status, nil
}

var testJWTSecret = []byte("test-secret-for-grace-gate")

func tokenFor(t *testing.T, clinicID uuid.UUID) string {
	t.Helper()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
		ClinicID: clinicID,
		StaffID:  uuid.New(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(testJWTSecret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func newTestHandler(reader ClinicStatusReader) http.Handler {
	mw := BlockWritesOnGracePeriod(reader, testJWTSecret, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
}

func TestBlockWritesOnGracePeriod_AllowsReadsEvenInGrace(t *testing.T) {
	t.Parallel()
	reader := &fakeStatusReader{status: domain.ClinicStatusGracePeriod}
	h := newTestHandler(reader)

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, "/api/v1/notes", nil)
		req.Header.Set("Authorization", "Bearer "+tokenFor(t, uuid.New()))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d, want 200", method, rec.Code)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("reads must not query status, got %d calls", reader.calls)
	}
}

func TestBlockWritesOnGracePeriod_BlocksWriteWhenGrace(t *testing.T) {
	t.Parallel()
	reader := &fakeStatusReader{status: domain.ClinicStatusGracePeriod}
	h := newTestHandler(reader)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/notes", nil)
		req.Header.Set("Authorization", "Bearer "+tokenFor(t, uuid.New()))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("%s: got %d, want 402", method, rec.Code)
		}
	}
}

func TestBlockWritesOnGracePeriod_AllowsWriteWhenActive(t *testing.T) {
	t.Parallel()
	reader := &fakeStatusReader{status: domain.ClinicStatusActive}
	h := newTestHandler(reader)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", nil)
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, uuid.New()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}

func TestBlockWritesOnGracePeriod_AllowsBillingPathInGrace(t *testing.T) {
	t.Parallel()
	reader := &fakeStatusReader{status: domain.ClinicStatusGracePeriod}
	h := newTestHandler(reader)

	for _, path := range []string{
		"/api/v1/billing/portal",
		"/api/v1/billing/checkout",
		"/api/v1/auth/login",
		"/api/v1/auth/refresh",
		"/health",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer "+tokenFor(t, uuid.New()))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d, want 200 (exempt)", path, rec.Code)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("exempt paths must not query status, got %d calls", reader.calls)
	}
}

func TestBlockWritesOnGracePeriod_PassThroughOnMissingJWT(t *testing.T) {
	t.Parallel()
	reader := &fakeStatusReader{status: domain.ClinicStatusGracePeriod}
	h := newTestHandler(reader)

	// No Authorization header — middleware can't determine clinic_id, so
	// it lets the request through; per-operation auth middleware will
	// produce the canonical 401.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (pass-through)", rec.Code)
	}
	if reader.calls != 0 {
		t.Fatalf("missing JWT must not trigger status lookup, got %d", reader.calls)
	}
}

func TestBlockWritesOnGracePeriod_PassThroughOnInvalidJWT(t *testing.T) {
	t.Parallel()
	reader := &fakeStatusReader{status: domain.ClinicStatusGracePeriod}
	h := newTestHandler(reader)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-jwt")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (pass-through, auth-mw will reject)", rec.Code)
	}
}

func TestBlockWritesOnGracePeriod_FailOpenOnDBError(t *testing.T) {
	t.Parallel()
	reader := &fakeStatusReader{err: errors.New("db down")}
	h := newTestHandler(reader)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", nil)
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, uuid.New()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// A transient DB blip must not 402-lock every clinic.
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (fail-open)", rec.Code)
	}
}

func TestBlockWritesOnGracePeriod_PassThroughOnNotFound(t *testing.T) {
	t.Parallel()
	reader := &fakeStatusReader{err: domain.ErrNotFound}
	h := newTestHandler(reader)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", nil)
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, uuid.New()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (clinic not found = pass)", rec.Code)
	}
}

func TestBlockWritesOnGracePeriod_PastDueAllowsWrites(t *testing.T) {
	t.Parallel()
	// past_due is the dunning window — Stripe is still retrying. Writes
	// stay open; only grace_period (Stripe `unpaid`) blocks.
	reader := &fakeStatusReader{status: domain.ClinicStatusPastDue}
	h := newTestHandler(reader)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", nil)
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, uuid.New()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (past_due is not blocked)", rec.Code)
	}
}

func TestBlockWritesOnGracePeriod_TokenViaQueryParam(t *testing.T) {
	t.Parallel()
	reader := &fakeStatusReader{status: domain.ClinicStatusGracePeriod}
	h := newTestHandler(reader)

	tok := tokenFor(t, uuid.New())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes?token="+tok, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("got %d, want 402 (token query param honoured)", rec.Code)
	}
}
