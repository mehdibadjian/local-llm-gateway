package embed_test

import (
	"testing"
	"time"

	"github.com/caw/wrapper/internal/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbedCircuitBreaker_OpensAfter3Failures(t *testing.T) {
	cb := embed.NewCircuitBreaker()

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	assert.Equal(t, embed.StateOpen, cb.State())
}

func TestEmbedCircuitBreaker_FailFastWhenOpen(t *testing.T) {
	cb := embed.NewCircuitBreaker()

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	err := cb.Allow()
	assert.ErrorIs(t, err, embed.ErrCircuitOpen)
}

func TestEmbedCircuitBreaker_HalfOpenProbe(t *testing.T) {
	cb := embed.NewCircuitBreaker(embed.WithOpenDuration(50 * time.Millisecond))

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}
	assert.Equal(t, embed.StateOpen, cb.State())

	time.Sleep(60 * time.Millisecond)

	err := cb.Allow()
	require.NoError(t, err)
	assert.Equal(t, embed.StateHalfOpen, cb.State())
}

func TestEmbedCircuitBreaker_ClosesOnSuccess(t *testing.T) {
	cb := embed.NewCircuitBreaker(embed.WithOpenDuration(50 * time.Millisecond))

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	time.Sleep(60 * time.Millisecond)

	err := cb.Allow()
	require.NoError(t, err)

	cb.RecordSuccess()
	assert.Equal(t, embed.StateClosed, cb.State())
}
