//! Bounded notification delivery independent of the correction worker.

use std::sync::mpsc::{self, SyncSender};

/// One bounded sender to the independent OS notification worker.
pub struct Notifications {
    sender: SyncSender<String>,
}

impl Notifications {
    pub(super) fn start(mut deliver: impl FnMut(&str) + Send + 'static) -> std::io::Result<Self> {
        let (sender, receiver) = mpsc::sync_channel::<String>(1);
        std::thread::Builder::new()
            .name("fixkey-notifications".into())
            .spawn(move || {
                while let Ok(message) = receiver.recv() {
                    deliver(&message);
                }
            })?;
        Ok(Self { sender })
    }

    pub(super) fn enqueue(&self, message: String) -> bool {
        // Native notification backends can block, including during macOS
        // bundle discovery. Keep one delivery worker and one pending message;
        // never wait for that backend or grow a thread per status transition.
        self.sender.try_send(message).is_ok()
    }
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use super::*;

    #[test]
    fn blocked_delivery_does_not_block_callers_or_grow_the_queue() {
        let (started, observed) = mpsc::channel();
        let (release, wait) = mpsc::channel();
        let notifications = Notifications::start(move |message| {
            started.send(message.to_owned()).unwrap();
            wait.recv().unwrap();
        })
        .unwrap();
        assert!(notifications.enqueue("first".into()));
        assert_eq!(
            observed.recv_timeout(Duration::from_secs(1)).unwrap(),
            "first"
        );
        assert!(notifications.enqueue("second".into()));
        assert!(!notifications.enqueue("overflow".into()));
        release.send(()).unwrap();
        assert_eq!(
            observed.recv_timeout(Duration::from_secs(1)).unwrap(),
            "second"
        );
        release.send(()).unwrap();
        drop(notifications);
        assert!(observed.recv_timeout(Duration::from_secs(1)).is_err());
    }
}
