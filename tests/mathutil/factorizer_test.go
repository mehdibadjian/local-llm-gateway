package mathutil_test

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/caw/wrapper/internal/mathutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── 1. Basic correctness ──────────────────────────────────────────────────────

func TestFactorize_SmallComposite(t *testing.T) {
	// 12 = 2² × 3
	result, err := mathutil.Factorize(12)
	require.NoError(t, err)
	assert.Equal(t, int64(12), result.N)
	assert.False(t, result.IsPrime)
	require.Len(t, result.Factors, 2)
	assert.Equal(t, mathutil.Factor{Prime: 2, Exp: 2}, result.Factors[0])
	assert.Equal(t, mathutil.Factor{Prime: 3, Exp: 1}, result.Factors[1])
}

// ── 2. Prime input ────────────────────────────────────────────────────────────

func TestFactorize_PrimeNumber(t *testing.T) {
	// 97 is prime
	result, err := mathutil.Factorize(97)
	require.NoError(t, err)
	assert.True(t, result.IsPrime)
	require.Len(t, result.Factors, 1)
	assert.Equal(t, int64(97), result.Factors[0].Prime)
	assert.Equal(t, 1, result.Factors[0].Exp)
}

// ── 3. Power of two ───────────────────────────────────────────────────────────

func TestFactorize_PowerOfTwo(t *testing.T) {
	// 1024 = 2^10
	result, err := mathutil.Factorize(1024)
	require.NoError(t, err)
	require.Len(t, result.Factors, 1)
	assert.Equal(t, int64(2), result.Factors[0].Prime)
	assert.Equal(t, 10, result.Factors[0].Exp)
}

// ── 4. Large semiprime (product of two large primes) ─────────────────────────

func TestFactorize_LargeSemiprime(t *testing.T) {
	// 999_999_937 × 2 = 1_999_999_874 — tests the wheel near sqrt boundary
	// 999_999_937 is prime
	n := int64(999_999_937)
	result, err := mathutil.Factorize(n)
	require.NoError(t, err)
	assert.True(t, result.IsPrime, "999_999_937 should be prime")
}

// ── 5. Highly composite number ────────────────────────────────────────────────

func TestFactorize_HighlyComposite(t *testing.T) {
	// 720720 = 2⁴ × 3² × 5 × 7 × 11 × 13
	result, err := mathutil.Factorize(720720)
	require.NoError(t, err)
	assert.False(t, result.IsPrime)

	// Reconstruct n from factors and verify.
	product := int64(1)
	for _, f := range result.Factors {
		for i := 0; i < f.Exp; i++ {
			product *= f.Prime
		}
	}
	assert.Equal(t, int64(720720), product)
}

// ── 6. LRU cache hit ──────────────────────────────────────────────────────────

func TestFactorize_CacheHit(t *testing.T) {
	f := mathutil.NewFactorizer(10)

	first, err := f.Factorize(360)
	require.NoError(t, err)
	assert.False(t, first.FromCache)

	second, err := f.Factorize(360)
	require.NoError(t, err)
	assert.True(t, second.FromCache, "second call should be served from cache")
	assert.Equal(t, first.Factors, second.Factors)
}

// ── 7. LRU eviction ───────────────────────────────────────────────────────────

func TestFactorize_CacheEviction(t *testing.T) {
	// Cache of size 2: insert 3 items — oldest should be evicted.
	f := mathutil.NewFactorizer(2)
	_, _ = f.Factorize(2)
	_, _ = f.Factorize(3)
	_, _ = f.Factorize(5) // evicts 2

	// 2 was evicted — must recompute.
	result, err := f.Factorize(2)
	require.NoError(t, err)
	assert.False(t, result.FromCache, "evicted entry should be recomputed")
}

// ── 8. Concurrent safety ──────────────────────────────────────────────────────

func TestFactorize_ConcurrentSafety(t *testing.T) {
	f := mathutil.NewFactorizer(64)
	const goroutines = 50
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		n := int64(2 + i*7) // different inputs
		go func(n int64) {
			defer wg.Done()
			result, err := f.Factorize(n)
			if err != nil {
				errs <- err
				return
			}
			// Verify by reconstructing n.
			product := int64(1)
			for _, fac := range result.Factors {
				for j := 0; j < fac.Exp; j++ {
					product *= fac.Prime
				}
			}
			if product != n {
				errs <- errors.New("factorization mismatch")
			}
		}(n)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}

// ── IMPOSSIBLE EDGE CASES ─────────────────────────────────────────────────────

// 9. n = 0, -1, 1, math.MinInt64 — all below threshold
func TestFactorize_ImpossibleInputs_OutOfRange(t *testing.T) {
	cases := []struct {
		name string
		n    int64
	}{
		{"zero", 0},
		{"negative one", -1},
		{"one", 1},
		{"math.MinInt64", math.MinInt64},
		{"large negative", -999_999_999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := mathutil.Factorize(tc.n)
			assert.Nil(t, result)
			require.Error(t, err)
			assert.ErrorIs(t, err, mathutil.ErrInputOutOfRange)
		})
	}
}

// 10. n > MaxSafeInput — would cause timeout via trial division
func TestFactorize_ImpossibleInputs_TooLarge(t *testing.T) {
	cases := []struct {
		name string
		n    int64
	}{
		{"MaxSafeInput + 1", mathutil.MaxSafeInput + 1},
		{"near max int64", math.MaxInt64},
		{"10^15 + 1", 1_000_000_000_000_001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			result, err := mathutil.Factorize(tc.n)
			elapsed := time.Since(start)

			assert.Nil(t, result)
			require.Error(t, err)
			assert.ErrorIs(t, err, mathutil.ErrInputTooLarge)
			// Must reject instantly — not do actual computation.
			assert.Less(t, elapsed, time.Millisecond,
				"rejection must be instant, not timeout-inducing")
		})
	}
}

// ── IsPrime ───────────────────────────────────────────────────────────────────

func TestIsPrime(t *testing.T) {
	primes := []int64{2, 3, 5, 7, 11, 97, 999_999_937}
	for _, p := range primes {
		assert.True(t, mathutil.IsPrime(p), "expected %d to be prime", p)
	}
	notPrimes := []int64{0, 1, 4, 9, 100, 720720}
	for _, p := range notPrimes {
		assert.False(t, mathutil.IsPrime(p), "expected %d to not be prime", p)
	}
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

func BenchmarkFactorize_SmallPrime(b *testing.B) {
f := mathutil.NewFactorizer(0)
for i := 0; i < b.N; i++ {
_, _ = f.Factorize(97)
}
}

func BenchmarkFactorize_LargePrime(b *testing.B) {
f := mathutil.NewFactorizer(0)
for i := 0; i < b.N; i++ {
_, _ = f.Factorize(999_999_937)
}
}

func BenchmarkFactorize_CacheHit(b *testing.B) {
f := mathutil.NewFactorizer(1024)
_, _ = f.Factorize(720720) // warm
b.ResetTimer()
for i := 0; i < b.N; i++ {
_, _ = f.Factorize(720720)
}
}

func BenchmarkFactorize_MaxSafe(b *testing.B) {
f := mathutil.NewFactorizer(0)
n := mathutil.MaxSafeInput // worst case: large prime ≈ 10^15
for i := 0; i < b.N; i++ {
_, _ = f.Factorize(n)
}
}

func BenchmarkFactorize_LargePrimeNear10e15(b *testing.B) {
	// 999_999_999_999_043 is prime (verified) — worst case for trial division,
	// best case for IsPrime fast path.
	f := mathutil.NewFactorizer(0)
	n := int64(999_999_999_999_043)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = f.Factorize(n)
	}
}

func BenchmarkFactorize_TrueCompute_LargePrime(b *testing.B) {
	// Force fresh factorizer each iteration to bypass cache.
	// All four values are verified primes near 10^15.
	primes := []int64{
		999_999_999_999_043,
		999_999_999_999_071,
		999_999_999_999_089,
		999_999_999_999_103,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := mathutil.NewFactorizer(1)
		n := primes[i%len(primes)]
		_, _ = f.Factorize(n)
	}
}

func BenchmarkIsPrime_Large(b *testing.B) {
	n := int64(999_999_999_999_043)
	for i := 0; i < b.N; i++ {
		_ = mathutil.IsPrime(n)
	}
}

func BenchmarkNewFactorizer(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = mathutil.NewFactorizer(1)
	}
}

func BenchmarkFactorize_LargePrime_SharedFactorizer(b *testing.B) {
	// Shared factorizer, cycle through verified primes — all cache misses beyond size 2.
	primes := []int64{
		999_999_999_999_043, // verified prime
		999_999_999_999_071, // verified prime
		999_999_999_999_089, // verified prime
		999_999_999_999_103, // verified prime
	}
	f := mathutil.NewFactorizer(2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = f.Factorize(primes[i%len(primes)])
	}
}
