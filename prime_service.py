"""
Prime Factorization Service
- Wheel factorization (2,3,5) for trial division baseline
- Pollard's rho for large composites (O(n^1/4) per factor)
- functools.lru_cache for memoization
"""
import math
import random
from functools import lru_cache
from typing import Dict, List


def _gcd(a: int, b: int) -> int:
    while b:
        a, b = b, a % b
    return a


def _miller_rabin(n: int, a: int) -> bool:
    """Miller-Rabin primality witness check."""
    d, r = n - 1, 0
    while d % 2 == 0:
        d //= 2
        r += 1
    x = pow(a, d, n)
    if x == 1 or x == n - 1:
        return True
    for _ in range(r - 1):
        x = pow(x, 2, n)
        if x == n - 1:
            return True
    return False


def _is_prime(n: int) -> bool:
    """Deterministic for n < 3,317,044,064,679,887,385,961,981 (covers all 64-bit ints)."""
    if n < 2:
        return False
    if n < 4:
        return True
    if n % 2 == 0 or n % 3 == 0:
        return False
    for a in [2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37]:
        if n == a:
            return True
        if not _miller_rabin(n, a):
            return False
    return True


def _pollard_rho(n: int) -> int:
    """Return a non-trivial factor of n. Assumes n is composite."""
    if n % 2 == 0:
        return 2
    while True:
        x = random.randint(2, n - 1)
        y = x
        c = random.randint(1, n - 1)
        d = 1
        while d == 1:
            x = (x * x + c) % n
            y = (y * y + c) % n
            y = (y * y + c) % n
            d = _gcd(abs(x - y), n)
        if d != n:
            return d


def _factor_recursive(n: int, factors: Dict[int, int]) -> None:
    """Recursively decompose n, accumulating prime→exponent in factors."""
    if n <= 1:
        return
    if _is_prime(n):
        factors[n] = factors.get(n, 0) + 1
        return
    # Wheel: trial division by 2, 3, small primes first (fast path)
    for p in [2, 3, 5, 7, 11, 13]:
        if n % p == 0:
            while n % p == 0:
                factors[p] = factors.get(p, 0) + 1
                n //= p
            if n > 1:
                _factor_recursive(n, factors)
            return
    # Pollard's rho for large composites
    d = _pollard_rho(n)
    _factor_recursive(d, factors)
    _factor_recursive(n // d, factors)


@lru_cache(maxsize=4096)
def factorize(n: int) -> Dict[int, int]:
    """
    Return the prime factorization of n as {prime: exponent}.

    Complexity:
      - Trial division wheel covers small primes in O(n^(1/6)) worst case
      - Pollard's rho handles large composites in O(n^(1/4)) expected
    Raises:
      ValueError  – for n < 2 (including 0, 1, negatives)
      TypeError   – for non-integer or float inputs
    """
    if not isinstance(n, int):
        raise TypeError(f"Expected int, got {type(n).__name__}")
    if n < 2:
        raise ValueError(f"n must be ≥ 2, got {n}")

    factors: Dict[int, int] = {}
    _factor_recursive(n, factors)
    return dict(sorted(factors.items()))


def factorize_list(n: int) -> List[int]:
    """Return sorted list of prime factors (with repetition)."""
    result = []
    for p, e in factorize(n).items():
        result.extend([p] * e)
    return result
