use crate::record::Record;

/// A conjunction of field conditions applied to structured records, parsed
/// from expressions like `level=error http.status!=200`. Lines that are not
/// JSONL never match a non-empty filter.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Filter {
    terms: Vec<Term>,
    /// The source expression, kept verbatim for display and re-editing.
    expr: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct Term {
    key: String,
    value: String,
    negated: bool,
}

impl Filter {
    /// Parses a whitespace-separated list of `key=value` / `key!=value`
    /// terms. Values compare ASCII-case-insensitively. `Ok(None)` means the
    /// expression was empty (no filter).
    pub fn parse(expr: &str) -> Result<Option<Self>, String> {
        let mut terms = Vec::new();
        for word in expr.split_ascii_whitespace() {
            let (key, value, negated) = match word.split_once("!=") {
                Some((key, value)) => (key, value, true),
                None => match word.split_once('=') {
                    Some((key, value)) => (key, value, false),
                    None => return Err(format!("`{word}`: expected key=value or key!=value")),
                },
            };
            if key.is_empty() {
                return Err(format!("`{word}`: missing field name"));
            }
            terms.push(Term {
                key: key.to_string(),
                value: value.to_string(),
                negated,
            });
        }
        if terms.is_empty() {
            return Ok(None);
        }
        Ok(Some(Self {
            terms,
            expr: expr.trim().to_string(),
        }))
    }

    /// The expression this filter was parsed from.
    pub fn expr(&self) -> &str {
        &self.expr
    }

    /// True when every term holds for `record`; plain-text lines
    /// (`record` = `None`) never match.
    pub fn matches(&self, record: Option<&Record>) -> bool {
        let Some(record) = record else {
            return false;
        };
        self.terms.iter().all(|term| {
            let holds = record
                .get(&term.key)
                .is_some_and(|value| value.eq_ignore_ascii_case(&term.value));
            holds != term.negated
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn record(json: &str) -> Record {
        Record::parse(json).unwrap()
    }

    #[test]
    fn empty_expression_means_no_filter() {
        assert_eq!(Filter::parse("").unwrap(), None);
        assert_eq!(Filter::parse("   ").unwrap(), None);
    }

    #[test]
    fn matches_case_insensitively() {
        let filter = Filter::parse("level=error").unwrap().unwrap();
        assert!(filter.matches(Some(&record(r#"{"level":"ERROR"}"#))));
        assert!(!filter.matches(Some(&record(r#"{"level":"info"}"#))));
        assert!(!filter.matches(Some(&record(r#"{"msg":"no level"}"#))));
        assert!(!filter.matches(None));
    }

    #[test]
    fn terms_are_anded() {
        let filter = Filter::parse("level=error app=api").unwrap().unwrap();
        assert!(filter.matches(Some(&record(r#"{"level":"error","app":"api"}"#))));
        assert!(!filter.matches(Some(&record(r#"{"level":"error","app":"web"}"#))));
    }

    #[test]
    fn negation_excludes_matches_and_missing_fields_pass() {
        let filter = Filter::parse("level!=debug").unwrap().unwrap();
        assert!(filter.matches(Some(&record(r#"{"level":"info"}"#))));
        assert!(!filter.matches(Some(&record(r#"{"level":"DEBUG"}"#))));
        // A record without the field is "not debug", so it passes.
        assert!(filter.matches(Some(&record(r#"{"msg":"x"}"#))));
    }

    #[test]
    fn matches_flattened_paths_and_empty_values() {
        let filter = Filter::parse("http.status=500").unwrap().unwrap();
        assert!(filter.matches(Some(&record(r#"{"http":{"status":500}}"#))));
        let empty = Filter::parse("note=").unwrap().unwrap();
        assert!(empty.matches(Some(&record(r#"{"note":""}"#))));
    }

    #[test]
    fn rejects_malformed_terms() {
        assert!(Filter::parse("plainword").is_err());
        assert!(Filter::parse("=value").is_err());
        assert!(Filter::parse("!=value").is_err());
        assert!(Filter::parse("level=error oops").is_err());
    }

    #[test]
    fn keeps_the_source_expression() {
        let filter = Filter::parse(" level=error ").unwrap().unwrap();
        assert_eq!(filter.expr(), "level=error");
    }
}
