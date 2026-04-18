package verticals

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClinicProvider struct {
	vertical domain.Vertical
	err      error
}

func (f *fakeClinicProvider) GetClinicVertical(_ context.Context, _ uuid.UUID) (domain.Vertical, error) {
	return f.vertical, f.err
}

func TestService_SchemaForClinic_ReturnsRegisteredSchema(t *testing.T) {
	t.Parallel()
	svc := NewService(&fakeClinicProvider{vertical: domain.VerticalVeterinary})

	schema, err := svc.SchemaForClinic(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, domain.VerticalVeterinary, schema.Vertical)
	assert.Equal(t, "Patient", schema.SubjectLabel)
	assert.Equal(t, "Owner", schema.ContactLabel)
	assert.NotEmpty(t, schema.Fields)
}

func TestService_SchemaForClinic_Dental_ReturnsDentalSchema(t *testing.T) {
	t.Parallel()
	svc := NewService(&fakeClinicProvider{vertical: domain.VerticalDental})

	schema, err := svc.SchemaForClinic(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, domain.VerticalDental, schema.Vertical)
	assert.Equal(t, "Guardian", schema.ContactLabel)
}

func TestService_SchemaForClinic_UnknownVertical_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService(&fakeClinicProvider{vertical: "bogus"})

	_, err := svc.SchemaForClinic(context.Background(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestService_SchemaForClinic_ProviderError_Wraps(t *testing.T) {
	t.Parallel()
	boom := errors.New("db boom")
	svc := NewService(&fakeClinicProvider{err: boom})

	_, err := svc.SchemaForClinic(context.Background(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}
