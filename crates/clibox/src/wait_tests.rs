use std::{
    cell::{Cell, RefCell},
    future::pending,
    rc::Rc,
};

use super::*;

fn options(timeout: Option<Duration>) -> Options {
    Options {
        timeout,
        interval: Duration::from_millis(250),
        quiet: false,
        json: false,
    }
}

#[tokio::test(start_paused = true)]
async fn checks_immediately_then_waits_after_completion_without_overlap() {
    let started = Rc::new(RefCell::new(Vec::new()));
    let active = Rc::new(Cell::new(false));
    let start = Instant::now();
    let report = run(
        Kind::Tcp,
        &options(None),
        Some(Duration::from_secs(3)),
        || {
            assert!(!active.replace(true));
            started.borrow_mut().push(start.elapsed());
            let attempt = started.borrow().len();
            let active = active.clone();
            async move {
                sleep(Duration::from_millis(100)).await;
                active.set(false);
                if attempt == 3 {
                    Ok(())
                } else {
                    Err(Code::ConnectionRefused)
                }
            }
        },
        pending(),
    )
    .await;
    assert_eq!(
        *started.borrow(),
        vec![
            Duration::ZERO,
            Duration::from_millis(350),
            Duration::from_millis(700)
        ]
    );
    assert_eq!(report.status, Status::Ready);
    assert_eq!(report.attempts, 3);
    assert_eq!(report.elapsed_ms, 800);
}

#[tokio::test(start_paused = true)]
async fn implicit_and_explicit_unlimited_waits_have_no_hidden_deadline() {
    for timeout in [None, Some(Duration::ZERO)] {
        let count = Cell::new(0);
        let report = run(
            Kind::File,
            &options(timeout),
            None,
            || {
                count.set(count.get() + 1);
                let done = count.get() == 2;
                async move {
                    sleep(Duration::from_secs(10_000)).await;
                    if done {
                        Ok(())
                    } else {
                        Err(Code::FileMissing)
                    }
                }
            },
            pending(),
        )
        .await;
        assert_eq!(report.status, Status::Ready);
        assert_eq!(report.elapsed_ms, 20_000_250);
    }
}

#[tokio::test(start_paused = true)]
async fn overall_deadline_clips_attempts_and_delays() {
    let report = run(
        Kind::Tcp,
        &options(Some(Duration::from_millis(100))),
        Some(Duration::from_secs(3)),
        pending,
        pending(),
    )
    .await;
    assert_eq!(report.status, Status::Timeout);
    assert_eq!(report.attempts, 1);
    assert_eq!(report.elapsed_ms, 100);
    assert!(report.error.unwrap().message.contains("attempt_timeout"));
    let report = run(
        Kind::File,
        &options(Some(Duration::from_millis(100))),
        None,
        || async { Err(Code::FileMissing) },
        pending(),
    )
    .await;
    assert_eq!(report.elapsed_ms, 100);
    assert_eq!(report.attempts, 1);
    assert!(report.error.unwrap().message.contains("file_missing"));
}

struct DropGuard(Rc<Cell<usize>>);
impl Drop for DropGuard {
    fn drop(&mut self) {
        self.0.set(self.0.get() + 1);
    }
}

#[tokio::test(start_paused = true)]
async fn attempt_budget_retries_and_drops_owned_work() {
    let drops = Rc::new(Cell::new(0));
    let report = run(
        Kind::Http,
        &options(Some(Duration::from_millis(800))),
        Some(Duration::from_millis(200)),
        || {
            let guard = DropGuard(drops.clone());
            async move {
                let _guard = guard;
                pending().await
            }
        },
        pending(),
    )
    .await;
    assert_eq!(report.status, Status::Timeout);
    assert_eq!(report.attempts, 2);
    assert_eq!(report.elapsed_ms, 800);
    assert_eq!(drops.get(), 2);
}

#[tokio::test(start_paused = true)]
async fn cancellation_interrupts_checks_and_delays_and_cleans_up() {
    for during_check in [true, false] {
        let drops = Rc::new(Cell::new(0));
        let report = run(
            Kind::File,
            &options(None),
            None,
            || {
                let guard = DropGuard(drops.clone());
                async move {
                    let _guard = guard;
                    if during_check {
                        pending::<()>().await;
                    }
                    Err(Code::FileMissing)
                }
            },
            async {
                sleep(Duration::from_millis(10)).await;
                Code::Interrupted
            },
        )
        .await;
        assert_eq!(report.status, Status::Cancelled);
        assert_eq!(report.exit_code, 130);
        assert_eq!(report.attempts, 1);
        assert_eq!(drops.get(), 1);
        assert_eq!(report.elapsed_ms, 10);
    }
}

#[tokio::test(start_paused = true)]
async fn terminal_failure_does_not_retry_and_early_cancellation_starts_no_attempt() {
    let report = run(
        Kind::File,
        &options(None),
        None,
        || async { Err(Code::PermissionDenied) },
        pending(),
    )
    .await;
    assert_eq!(report.status, Status::Failed);
    assert_eq!(report.attempts, 1);
    assert_eq!(report.elapsed_ms, 0);
    let report = run(
        Kind::Tcp,
        &options(None),
        None,
        || async { panic!("must not poll") },
        async { Code::Terminated },
    )
    .await;
    assert_eq!(report.status, Status::Cancelled);
    assert_eq!(report.exit_code, 143);
    assert_eq!(report.attempts, 0);
}

#[tokio::test(start_paused = true)]
async fn dns_recovers_in_a_later_poll() {
    let count = Cell::new(0);
    let report = run(
        Kind::Tcp,
        &options(None),
        None,
        || {
            count.set(count.get() + 1);
            let result = if count.get() < 3 {
                Err(Code::DnsLookup)
            } else {
                Ok(())
            };
            async move { result }
        },
        pending(),
    )
    .await;
    assert_eq!(report.status, Status::Ready);
    assert_eq!(report.attempts, 3);
}
