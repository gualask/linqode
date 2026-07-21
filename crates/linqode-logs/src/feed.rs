use std::sync::mpsc::Receiver;

/// One event of a followed log stream, ready for display.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum LogEvent {
    /// A complete log line (the followed command's stdout).
    Line(String),
    /// A diagnostic line the remote command wrote to stderr.
    Stderr(String),
    /// The stream terminated remotely; no more events follow.
    Ended { exit_code: Option<u32> },
}

/// Consumer end of a followed log stream, polled from a synchronous UI
/// loop. The producer (an async pump feeding a [`crate::LineAssembler`])
/// lives elsewhere; `stop` tears it down.
pub struct LogFeed {
    events: Receiver<LogEvent>,
    stop: Option<Box<dyn FnOnce() + Send>>,
}

impl LogFeed {
    pub fn new(events: Receiver<LogEvent>, stop: impl FnOnce() + Send + 'static) -> Self {
        Self {
            events,
            stop: Some(Box::new(stop)),
        }
    }

    /// Next pending event, without blocking. `None` means nothing pending
    /// right now, or the stream is over (after [`LogEvent::Ended`]).
    pub fn try_next(&mut self) -> Option<LogEvent> {
        self.events.try_recv().ok()
    }

    /// Cancels the remote command. Idempotent; also runs on drop.
    pub fn stop(&mut self) {
        if let Some(stop) = self.stop.take() {
            stop();
        }
    }
}

impl Drop for LogFeed {
    fn drop(&mut self) {
        self.stop();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::{Arc, mpsc};

    #[test]
    fn delivers_events_then_none() {
        let (tx, rx) = mpsc::channel();
        let mut feed = LogFeed::new(rx, || {});
        tx.send(LogEvent::Line("a".into())).unwrap();
        assert_eq!(feed.try_next(), Some(LogEvent::Line("a".into())));
        assert_eq!(feed.try_next(), None);
    }

    #[test]
    fn drop_stops_exactly_once() {
        let stopped = Arc::new(AtomicBool::new(false));
        let flag = Arc::clone(&stopped);
        let (_tx, rx) = mpsc::channel();
        let mut feed = LogFeed::new(rx, move || {
            assert!(!flag.swap(true, Ordering::SeqCst), "stopped twice");
        });
        feed.stop();
        drop(feed);
        assert!(stopped.load(Ordering::SeqCst));
    }
}
