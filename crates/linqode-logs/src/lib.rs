//! Log engine for Linqode.
//!
//! Docker-agnostic by design: everything here consumes a generic stream of
//! bytes or lines, so it can later be pointed at plain files as well as
//! `docker compose logs`. This crate does no I/O; feeding it from an SSH
//! channel is the caller's job. M3 provides line assembly, a bounded tail
//! buffer, and search; M4 adds JSONL records, field filters, live
//! aggregations, and [`LogStore`] tying them together for one stream.

mod agg;
mod feed;
mod filter;
mod line;
mod record;
mod store;
mod tail;

pub use agg::Aggregator;
pub use feed::{LogEvent, LogFeed};
pub use filter::Filter;
pub use line::LineAssembler;
pub use record::{LogLine, Record};
pub use store::LogStore;
pub use tail::TailBuffer;

/// Byte offset of the first ASCII-case-insensitive occurrence of `needle`
/// in `haystack`. Multi-byte UTF-8 sequences only match themselves, so the
/// returned offset is always a char boundary. An empty needle finds nothing.
pub fn find_ascii_ci(haystack: &str, needle: &str) -> Option<usize> {
    let (haystack, needle) = (haystack.as_bytes(), needle.as_bytes());
    if needle.is_empty() || needle.len() > haystack.len() {
        return None;
    }
    (0..=haystack.len() - needle.len())
        .find(|&i| haystack[i..i + needle.len()].eq_ignore_ascii_case(needle))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn finds_case_insensitively() {
        assert_eq!(find_ascii_ci("level=ERROR msg=boom", "error"), Some(6));
        assert_eq!(find_ascii_ci("all quiet", "error"), None);
    }

    #[test]
    fn offsets_stay_on_char_boundaries() {
        // 'à' is 0xC3 0xA0; a pure-ASCII needle must not match inside it.
        assert_eq!(find_ascii_ci("àPÀ", "p"), Some(2));
        assert_eq!(find_ascii_ci("naïve", "ïv"), Some(2));
    }

    #[test]
    fn empty_needle_finds_nothing() {
        assert_eq!(find_ascii_ci("anything", ""), None);
    }
}
