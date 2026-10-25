use std::time::{Duration, Instant};

#[derive(Debug, Clone)]
pub struct ReconnectBackoff {
    base: Duration,
    max: Duration,
    reset_after: Duration,
    attempt: u32,
    connected_since: Option<Instant>,
}

impl ReconnectBackoff {
    pub fn new(base: Duration, max: Duration, reset_after: Duration) -> Self {
        assert!(!base.is_zero(), "backoff base must be non-zero");
        assert!(max >= base, "backoff max must be at least base");
        Self {
            base,
            max,
            reset_after,
            attempt: 0,
            connected_since: None,
        }
    }

    pub fn production() -> Self {
        Self::new(
            Duration::from_millis(250),
            Duration::from_secs(30),
            Duration::from_secs(30),
        )
    }

    pub fn mark_connected(&mut self, now: Instant) {
        if self.connected_since.is_none() {
            self.connected_since = Some(now);
        }
    }

    pub fn mark_disconnected(&mut self, now: Instant) {
        if self
            .connected_since
            .is_some_and(|since| now.saturating_duration_since(since) >= self.reset_after)
        {
            self.attempt = 0;
        }
        self.connected_since = None;
    }

    /// Return the next retry delay. `entropy` is supplied by the caller so
    /// tests can be fully deterministic and production can use any local,
    /// non-cryptographic entropy source without coupling policy to an RNG.
    pub fn next_delay(&mut self, entropy: u64) -> Duration {
        let exponent = self.attempt.min(31);
        let factor = 1_u64.checked_shl(exponent).unwrap_or(u64::MAX);
        let base_ms = self.base.as_millis().min(u128::from(u64::MAX)) as u64;
        let max_ms = self.max.as_millis().min(u128::from(u64::MAX)) as u64;
        let exponential_ms = base_ms.saturating_mul(factor).min(max_ms);

        // Symmetric bounded jitter in [75%, 125%], clamped to the hard max.
        let jitter_percent = 75_u64 + (entropy % 51);
        let jittered_ms = exponential_ms
            .saturating_mul(jitter_percent)
            .saturating_div(100)
            .max(1)
            .min(max_ms);

        self.attempt = self.attempt.saturating_add(1);
        Duration::from_millis(jittered_ms)
    }

    pub fn attempt(&self) -> u32 {
        self.attempt
    }
}

impl Default for ReconnectBackoff {
    fn default() -> Self {
        Self::production()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn backoff_is_exponential_jittered_and_hard_bounded() {
        let mut backoff = ReconnectBackoff::new(
            Duration::from_millis(100),
            Duration::from_millis(1000),
            Duration::from_secs(10),
        );
        let delays = (0..8)
            .map(|_| backoff.next_delay(25)) // 100% jitter factor
            .collect::<Vec<_>>();
        assert_eq!(
            delays,
            vec![100, 200, 400, 800, 1000, 1000, 1000, 1000]
                .into_iter()
                .map(Duration::from_millis)
                .collect::<Vec<_>>()
        );
    }

    #[test]
    fn jitter_never_escapes_policy_bounds() {
        let mut low = ReconnectBackoff::new(
            Duration::from_millis(100),
            Duration::from_millis(1000),
            Duration::from_secs(10),
        );
        let mut high = low.clone();
        assert_eq!(low.next_delay(0), Duration::from_millis(75));
        assert_eq!(high.next_delay(50), Duration::from_millis(125));
        for _ in 0..20 {
            assert!(high.next_delay(50) <= Duration::from_millis(1000));
        }
    }

    #[test]
    fn sustained_success_resets_attempts_but_short_success_does_not() {
        let start = Instant::now();
        let mut backoff = ReconnectBackoff::new(
            Duration::from_millis(100),
            Duration::from_secs(5),
            Duration::from_secs(10),
        );
        backoff.next_delay(25);
        backoff.next_delay(25);
        assert_eq!(backoff.attempt(), 2);

        backoff.mark_connected(start);
        backoff.mark_disconnected(start + Duration::from_secs(5));
        assert_eq!(backoff.attempt(), 2);

        backoff.mark_connected(start + Duration::from_secs(6));
        backoff.mark_disconnected(start + Duration::from_secs(16));
        assert_eq!(backoff.attempt(), 0);
        assert_eq!(backoff.next_delay(25), Duration::from_millis(100));
    }
}
