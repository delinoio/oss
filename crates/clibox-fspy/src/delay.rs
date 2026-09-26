//! Bounded admission waits for traced operations.

use std::{
    sync::atomic::{AtomicBool, Ordering},
    thread,
    time::{Duration, Instant},
};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DelayFailure {
    Cancelled,
    Timeout,
}

pub fn wait(
    duration: Duration,
    cancelled: &AtomicBool,
    deadline: Option<Instant>,
) -> Result<Duration, DelayFailure> {
    let began = Instant::now();
    let delay_until = began.checked_add(duration).ok_or(DelayFailure::Timeout)?;
    loop {
        if cancelled.load(Ordering::Acquire) {
            return Err(DelayFailure::Cancelled);
        }
        let now = Instant::now();
        if deadline.is_some_and(|limit| now >= limit) {
            return Err(DelayFailure::Timeout);
        }
        if now >= delay_until {
            return Ok(began.elapsed());
        }
        let remaining = delay_until.saturating_duration_since(now);
        let until_deadline = deadline
            .map(|limit| limit.saturating_duration_since(now))
            .unwrap_or(remaining);
        thread::sleep(remaining.min(until_deadline).min(Duration::from_millis(20)));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn deadline_interrupts_long_admission_delay() {
        let cancelled = AtomicBool::new(false);
        let began = Instant::now();
        assert_eq!(
            wait(
                Duration::from_secs(60),
                &cancelled,
                Some(began + Duration::from_millis(30))
            ),
            Err(DelayFailure::Timeout)
        );
        assert!(began.elapsed() < Duration::from_secs(1));
    }

    #[test]
    fn cancellation_interrupts_long_admission_delay() {
        let cancelled = AtomicBool::new(false);
        let began = Instant::now();
        thread::scope(|scope| {
            scope.spawn(|| {
                thread::sleep(Duration::from_millis(30));
                cancelled.store(true, Ordering::Release);
            });
            assert_eq!(
                wait(Duration::from_secs(60), &cancelled, None),
                Err(DelayFailure::Cancelled)
            );
        });
        assert!(began.elapsed() < Duration::from_secs(1));
    }
}
