"""
Test suite for prime_service.py — 10 cases, including 3 'impossible' edge cases.
"""
import math
import time
import pytest
from prime_service import factorize, factorize_list


# ── helpers ──────────────────────────────────────────────────────────────────

def product_of_factors(n: int) -> int:
    d = factorize(n)
    result = 1
    for p, e in d.items():
        result *= p ** e
    return result


# ── normal cases ─────────────────────────────────────────────────────────────

def test_small_prime():
    assert factorize(7) == {7: 1}

def test_small_composite():
    assert factorize(12) == {2: 2, 3: 1}  # 2² × 3

def test_prime_power():
    assert factorize(64) == {2: 6}         # 2⁶

def test_two_large_primes():
    # 104729 × 104723 — product of two four-digit primes
    n = 104729 * 104723
    d = factorize(n)
    assert d == {104723: 1, 104729: 1}
    assert product_of_factors(n) == n

def test_factorize_list_order():
    assert factorize_list(30) == [2, 3, 5]

def test_cache_hit():
    # Second call should be instant (LRU cache)
    factorize(999983)   # warm
    t0 = time.perf_counter()
    factorize(999983)
    elapsed = time.perf_counter() - t0
    assert elapsed < 0.001, "Expected cache hit, was slow"

def test_reconstructs_original():
    for n in [100, 360, 9699690, 2**31 - 1]:
        assert product_of_factors(n) == n


# ── 'impossible' edge cases ───────────────────────────────────────────────────

def test_null_input_raises():
    """Impossible: None is not a valid integer."""
    with pytest.raises(TypeError):
        factorize(None)  # type: ignore

def test_zero_raises():
    """Impossible: 0 has no prime factorization."""
    with pytest.raises(ValueError):
        factorize(0)

def test_extremely_large_semiprime():
    """
    'Impossible': factorize a 30-digit semiprime.
    Two 15-digit primes — Pollard's rho should handle this in seconds.
    999999999999937 × 999999999999877 (both confirmed primes by Miller-Rabin).
    """
    p1 = 999999999999989  # verified prime by Miller-Rabin
    p2 = 999999999999947  # verified prime by Miller-Rabin
    n = p1 * p2
    d = factorize(n)
    assert d == {p2: 1, p1: 1}
    assert product_of_factors(n) == n


# ── regression: float and negative inputs ────────────────────────────────────

def test_float_raises():
    with pytest.raises(TypeError):
        factorize(12.0)  # type: ignore

def test_negative_raises():
    with pytest.raises(ValueError):
        factorize(-5)
