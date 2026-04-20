// Package mathutil provides a Prime Factorization API with LRU caching.
//
// Algorithm: hybrid trial division + Pollard's rho (Brent's variant).
//   - Trial division with 2/3/5 wheel up to 10^6 removes all small factors.
//   - Pollard's rho handles remaining large factors in O(n^¼).
//
// Time complexity:  O(n^¼ · log n) per call after cache miss.
// Space complexity: O(k) per result where k = number of distinct prime factors.
// Cache:            bounded LRU — O(1) amortised get/put.
package mathutil

import (
"errors"
"fmt"
"math/big"
"math/rand"
"sync"
"time"
)

// ── Public types ──────────────────────────────────────────────────────────────

// Factor is a prime base with its exponent (e.g. 2³ → {Prime:2, Exp:3}).
type Factor struct {
Prime int64
Exp   int
}

// Factorization is the ordered list of prime factors for a number.
type Factorization struct {
N          int64
Factors    []Factor
IsPrime    bool
ComputedAt time.Time
FromCache  bool
}

// ErrInputOutOfRange is returned when n < 2.
var ErrInputOutOfRange = errors.New("input must be an integer ≥ 2")

// ErrInputTooLarge is returned when n > MaxSafeInput.
var ErrInputTooLarge = errors.New("input exceeds maximum safe value (10^15)")

// MaxSafeInput is the largest value Factorize will accept.
const MaxSafeInput int64 = 1_000_000_000_000_000

// trialDivisionLimit: trial divide up to 10^6, then switch to Pollard's rho.
const trialDivisionLimit = int64(1_000_000)

// ── LRU cache ────────────────────────────────────────────────────────────────

type cacheNode struct {
key        int64
value      Factorization
prev, next *cacheNode
}

type lruCache struct {
mu     sync.Mutex
cap    int
items  map[int64]*cacheNode
head   *cacheNode
tail   *cacheNode
hits   int64
misses int64
}

func newLRUCache(capacity int) *lruCache {
head := &cacheNode{}
tail := &cacheNode{}
head.next = tail
tail.prev = head
return &lruCache{cap: capacity, items: make(map[int64]*cacheNode, capacity), head: head, tail: tail}
}

func (c *lruCache) get(key int64) (Factorization, bool) {
c.mu.Lock()
defer c.mu.Unlock()
node, ok := c.items[key]
if !ok {
c.misses++
return Factorization{}, false
}
c.moveToFront(node)
c.hits++
return node.value, true
}

func (c *lruCache) put(key int64, val Factorization) {
c.mu.Lock()
defer c.mu.Unlock()
if node, ok := c.items[key]; ok {
node.value = val
c.moveToFront(node)
return
}
node := &cacheNode{key: key, value: val}
c.items[key] = node
c.addToFront(node)
if len(c.items) > c.cap {
lru := c.tail.prev
c.removeNode(lru)
delete(c.items, lru.key)
}
}

func (c *lruCache) removeNode(n *cacheNode) { n.prev.next = n.next; n.next.prev = n.prev }
func (c *lruCache) addToFront(n *cacheNode) {
n.next = c.head.next; n.prev = c.head; c.head.next.prev = n; c.head.next = n
}
func (c *lruCache) moveToFront(n *cacheNode) { c.removeNode(n); c.addToFront(n) }
func (c *lruCache) stats() (hits, misses int64) {
c.mu.Lock()
defer c.mu.Unlock()
return c.hits, c.misses
}

// ── Factorizer ────────────────────────────────────────────────────────────────

// Factorizer is a thread-safe prime factorization service with LRU caching.
type Factorizer struct{ cache *lruCache }

// NewFactorizer creates a Factorizer with the given LRU cache capacity.
func NewFactorizer(cacheSize int) *Factorizer {
if cacheSize <= 0 {
cacheSize = 1
}
return &Factorizer{cache: newLRUCache(cacheSize)}
}

// DefaultFactorizer is a package-level instance with a 1024-entry cache.
var DefaultFactorizer = NewFactorizer(1024)

// Factorize returns the prime factorization of n (package-level convenience).
func Factorize(n int64) (*Factorization, error) { return DefaultFactorizer.Factorize(n) }

// Factorize is the instance method.
func (f *Factorizer) Factorize(n int64) (*Factorization, error) {
if n < 2 {
return nil, fmt.Errorf("%w: got %d", ErrInputOutOfRange, n)
}
if n > MaxSafeInput {
return nil, fmt.Errorf("%w: got %d", ErrInputTooLarge, n)
}
if cached, ok := f.cache.get(n); ok {
cached.FromCache = true
return &cached, nil
}
primes := collectPrimes(n)
result := Factorization{
N:          n,
Factors:    buildFactors(primes),
ComputedAt: time.Now(),
}
result.IsPrime = len(result.Factors) == 1 && result.Factors[0].Exp == 1
f.cache.put(n, result)
return &result, nil
}

// ── Core algorithms ───────────────────────────────────────────────────────────

// collectPrimes returns all prime factors of n (with multiplicity).
func collectPrimes(n int64) []int64 {
var primes []int64

// Fast path: Miller-Rabin primality check is O(log²n) — much cheaper than
// trial-dividing to 10^6. Short-circuit immediately for large primes.
if IsPrime(n) {
return []int64{n}
}

// Phase 1: wheel trial division removes small factors (p ≤ 10^6).
for _, p := range []int64{2, 3, 5} {
for n%p == 0 {
primes = append(primes, p)
n /= p
}
}
inc := []int64{4, 2, 4, 2, 4, 6, 2, 6}
d, idx := int64(7), 0
for d <= trialDivisionLimit && d*d <= n {
for n%d == 0 {
primes = append(primes, d)
n /= d
}
d += inc[idx]
idx = (idx + 1) % 8
}

if n == 1 {
return primes
}

// Phase 2: remaining cofactor has only large prime factors — Pollard's rho.
return append(primes, pollardFactors(n)...)
}

// pollardFactors recursively factors n via Brent's Pollard rho.
func pollardFactors(n int64) []int64 {
if n == 1 {
return nil
}
if IsPrime(n) {
return []int64{n}
}
d := pollardRho(n)
return append(pollardFactors(d), pollardFactors(n/d)...)
}

// pollardRho returns a non-trivial divisor of composite n.
func pollardRho(n int64) int64 {
if n%2 == 0 {
return 2
}
rng := rand.New(rand.NewSource(time.Now().UnixNano()))
for {
x := rng.Int63n(n-2) + 2
y := x
c := rng.Int63n(n-1) + 1
d := int64(1)
for d == 1 {
x = addmod(mulmod(x, x, n), c, n)
y = addmod(mulmod(y, y, n), c, n)
y = addmod(mulmod(y, y, n), c, n)
diff := x - y
if diff < 0 {
diff = -diff
}
d = gcd(diff, n)
}
if d != n {
return d
}
}
}

func mulmod(a, b, m int64) int64 {
r := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
return r.Mod(r, big.NewInt(m)).Int64()
}

func addmod(a, b, m int64) int64 {
r := a + b
if r >= m {
r -= m
}
return r
}

func gcd(a, b int64) int64 {
for b != 0 {
a, b = b, a%b
}
return a
}

// buildFactors deduplicates a sorted prime list into Factor structs.
func buildFactors(primes []int64) []Factor {
for i := 1; i < len(primes); i++ {
for j := i; j > 0 && primes[j] < primes[j-1]; j-- {
primes[j], primes[j-1] = primes[j-1], primes[j]
}
}
var factors []Factor
for _, p := range primes {
if len(factors) > 0 && factors[len(factors)-1].Prime == p {
factors[len(factors)-1].Exp++
} else {
factors = append(factors, Factor{Prime: p, Exp: 1})
}
}
return factors
}

// CacheStats returns the hit/miss counts for the DefaultFactorizer's cache.
func CacheStats() (hits, misses int64) { return DefaultFactorizer.cache.stats() }

// IsPrime reports whether n is prime (deterministic Miller-Rabin, O(log² n)).
func IsPrime(n int64) bool {
if n < 2 {
return false
}
return big.NewInt(n).ProbablyPrime(20)
}
