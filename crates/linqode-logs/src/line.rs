/// Reassembles complete lines from a stream of byte chunks whose boundaries
/// fall anywhere, keeping the trailing partial line across calls.
#[derive(Debug, Default)]
pub struct LineAssembler {
    partial: Vec<u8>,
}

impl LineAssembler {
    /// Feeds one chunk; returns the lines it completed, in order.
    /// Line endings (`\n`, `\r\n`) are stripped; invalid UTF-8 is replaced.
    pub fn push(&mut self, chunk: &[u8]) -> Vec<String> {
        let mut lines = Vec::new();
        for &byte in chunk {
            if byte == b'\n' {
                lines.push(Self::take(&mut self.partial));
            } else {
                self.partial.push(byte);
            }
        }
        lines
    }

    /// The unterminated final line, if the stream ended without a newline.
    pub fn finish(mut self) -> Option<String> {
        (!self.partial.is_empty()).then(|| Self::take(&mut self.partial))
    }

    fn take(partial: &mut Vec<u8>) -> String {
        if partial.last() == Some(&b'\r') {
            partial.pop();
        }
        let line = String::from_utf8_lossy(partial).into_owned();
        partial.clear();
        line
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn splits_lines_across_chunk_boundaries() {
        let mut assembler = LineAssembler::default();
        assert_eq!(assembler.push(b"first li"), Vec::<String>::new());
        assert_eq!(assembler.push(b"ne\nsecond\nthi"), vec!["first line", "second"]);
        assert_eq!(assembler.push(b"rd\n"), vec!["third"]);
        assert_eq!(assembler.finish(), None);
    }

    #[test]
    fn strips_crlf_and_flushes_trailing_partial() {
        let mut assembler = LineAssembler::default();
        assert_eq!(assembler.push(b"windows\r\ntail"), vec!["windows"]);
        assert_eq!(assembler.finish(), Some("tail".to_string()));
    }

    #[test]
    fn replaces_invalid_utf8() {
        let mut assembler = LineAssembler::default();
        assert_eq!(assembler.push(b"a\xff b\n"), vec!["a\u{fffd} b"]);
    }

    #[test]
    fn utf8_split_across_chunks_survives() {
        let mut assembler = LineAssembler::default();
        let bytes = "è\n".as_bytes(); // 0xC3 0xA8 0x0A
        assert_eq!(assembler.push(&bytes[..1]), Vec::<String>::new());
        assert_eq!(assembler.push(&bytes[1..]), vec!["è"]);
    }
}
