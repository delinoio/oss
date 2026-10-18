//! Word boundary detection and eligibility classification for typed text.
//!
//! The tokenizer only sees characters that the input-event adapter reports as
//! directly typed after IME composition completed. It holds characters whose
//! role (internal word character or trailing punctuation) is decided by the
//! next input, and it never triggers evaluation on Enter or Tab.

use crate::types::SkipReason;

/// Characters that can be internal to a word: apostrophes and hyphens.
const JOINERS: [char; 3] = ['\'', '\u{2019}', '-'];
/// Separators that can occur inside URLs and email addresses.
const URL_SEPARATORS: [char; 6] = ['.', '/', ':', '@', '?', '#'];

fn is_joiner(c: char) -> bool {
    JOINERS.contains(&c)
}

fn is_held_separator(c: char) -> bool {
    is_joiner(c) || c == '_' || URL_SEPARATORS.contains(&c)
}

fn is_emoji(c: char) -> bool {
    matches!(
        u32::from(c),
        0x1F000..=0x1FAFF | 0x2600..=0x27BF | 0x2B00..=0x2BFF | 0xFE0F | 0x200D | 0x20E3
    )
}

fn is_hangul(c: char) -> bool {
    matches!(u32::from(c), 0xAC00..=0xD7A3 | 0x3131..=0x3163)
}

/// Result of finishing a token.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum TokenOutcome {
    /// A word is complete and eligible for evaluation.
    Evaluate {
        /// Completed word without its boundary.
        word: String,
        /// Boundary characters that followed the word, preserved on
        /// replacement.
        boundary: String,
    },
    /// The token is excluded from automatic correction.
    Skipped(SkipReason),
}

/// Classifies a pending token, returning the exclusion reason if any.
///
/// A token that survives contains only letters of one supported script plus
/// internal apostrophes and hyphens.
#[must_use]
pub fn classify_token(token: &str) -> Option<SkipReason> {
    if token.chars().any(is_emoji) {
        return Some(SkipReason::Emoji);
    }
    if token.contains('@') && !token.starts_with('@') && !token.ends_with('@') {
        return Some(SkipReason::Email);
    }
    if token
        .chars()
        .any(|c| URL_SEPARATORS.contains(&c) || c == '_')
    {
        return Some(SkipReason::Url);
    }
    if token.chars().any(char::is_numeric) {
        return Some(SkipReason::Numeric);
    }
    let has_hangul = token.chars().any(is_hangul);
    let has_ascii = token.chars().any(|c| c.is_ascii_alphabetic());
    let has_other = token
        .chars()
        .any(|c| c.is_alphabetic() && !c.is_ascii() && !is_hangul(c));
    if (has_hangul && has_ascii) || has_other {
        return Some(SkipReason::MixedScript);
    }
    None
}

/// Incremental tokenizer for one active correction context.
#[derive(Debug, Default)]
pub struct Tokenizer {
    token: String,
    held: String,
}

impl Tokenizer {
    /// Creates an empty tokenizer.
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Returns whether a token is pending.
    #[must_use]
    pub const fn has_pending(&self) -> bool {
        !self.token.is_empty()
    }

    /// Discards the pending token and any held separator.
    pub fn reset(&mut self) {
        self.token.clear();
        self.held.clear();
    }

    /// Feeds one directly typed character.
    pub fn feed_char(&mut self, c: char) -> Option<TokenOutcome> {
        if c.is_whitespace() {
            return self.finish(c);
        }
        if is_held_separator(c) {
            if self.token.is_empty() {
                // A separator with no word before it cannot start a word.
                return None;
            }
            self.held.push(c);
            return None;
        }
        if c.is_alphanumeric() || is_emoji(c) {
            if !self.held.is_empty() {
                let held = std::mem::take(&mut self.held);
                self.token.push_str(&held);
            }
            self.token.push(c);
            return None;
        }
        // Any other punctuation ends the word.
        self.finish(c)
    }

    /// Feeds Enter or Tab.
    ///
    /// These keys never trigger evaluation by themselves. When a separator is
    /// held after a word, the word is classified once as trailing punctuation
    /// so the caller can evaluate it against the still-matching context;
    /// otherwise the pending token is discarded.
    pub fn feed_enter_or_tab(&mut self) -> Option<TokenOutcome> {
        if self.held.is_empty() {
            self.reset();
            return None;
        }
        self.finish_with_boundary("")
    }

    fn finish(&mut self, terminator: char) -> Option<TokenOutcome> {
        self.finish_with_boundary(terminator.encode_utf8(&mut [0; 4]))
    }

    fn finish_with_boundary(&mut self, terminator: &str) -> Option<TokenOutcome> {
        if self.token.is_empty() {
            self.reset();
            return None;
        }
        let word = std::mem::take(&mut self.token);
        let mut boundary = std::mem::take(&mut self.held);
        boundary.push_str(terminator);
        if let Some(reason) = classify_token(&word) {
            return Some(TokenOutcome::Skipped(reason));
        }
        Some(TokenOutcome::Evaluate { word, boundary })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn feed(tokenizer: &mut Tokenizer, text: &str) -> Vec<TokenOutcome> {
        text.chars()
            .filter_map(|c| tokenizer.feed_char(c))
            .collect()
    }

    fn evaluate(word: &str, boundary: &str) -> TokenOutcome {
        TokenOutcome::Evaluate {
            word: word.into(),
            boundary: boundary.into(),
        }
    }

    #[test]
    fn a_space_completes_a_word_and_is_preserved() {
        let mut t = Tokenizer::new();
        assert_eq!(feed(&mut t, "rPthrgo "), vec![evaluate("rPthrgo", " ")]);
        assert!(!t.has_pending());
    }

    #[test]
    fn punctuation_completes_a_word() {
        let mut t = Tokenizer::new();
        assert_eq!(feed(&mut t, "zlqhemrk,"), vec![evaluate("zlqhemrk", ",")]);
        assert_eq!(feed(&mut t, "abc!"), vec![evaluate("abc", "!")]);
    }

    #[test]
    fn internal_apostrophes_and_hyphens_stay_in_the_word() {
        let mut t = Tokenizer::new();
        assert_eq!(feed(&mut t, "don't "), vec![evaluate("don't", " ")]);
        assert_eq!(
            feed(&mut t, "well-known "),
            vec![evaluate("well-known", " ")]
        );
    }

    #[test]
    fn a_trailing_joiner_is_decided_by_the_next_character() {
        let mut t = Tokenizer::new();
        // Held: nothing is emitted while the decision is pending.
        assert_eq!(feed(&mut t, "dogs'"), vec![]);
        assert!(t.has_pending());
        assert_eq!(t.feed_char(' '), Some(evaluate("dogs", "' ")));
        assert_eq!(feed(&mut t, "go- "), vec![evaluate("go", "- ")]);
    }

    #[test]
    fn enter_and_tab_never_trigger_evaluation() {
        let mut t = Tokenizer::new();
        assert_eq!(feed(&mut t, "abc"), vec![]);
        assert_eq!(t.feed_enter_or_tab(), None);
        assert!(!t.has_pending());
        feed(&mut t, "abc");
        assert_eq!(t.feed_enter_or_tab(), None);
    }

    #[test]
    fn a_held_separator_before_enter_evaluates_once_as_trailing_punctuation() {
        let mut t = Tokenizer::new();
        feed(&mut t, "rPthrgo.");
        assert_eq!(t.feed_enter_or_tab(), Some(evaluate("rPthrgo", ".")));
        assert!(!t.has_pending());
    }

    #[test]
    fn a_held_separator_before_enter_is_discarded_for_excluded_tokens() {
        let mut t = Tokenizer::new();
        feed(&mut t, "123.");
        assert_eq!(
            t.feed_enter_or_tab(),
            Some(TokenOutcome::Skipped(SkipReason::Numeric))
        );
    }

    #[test]
    fn excluded_tokens_are_skipped_with_a_reason() {
        let mut t = Tokenizer::new();
        let cases = [
            ("abc123 ", SkipReason::Numeric),
            ("42 ", SkipReason::Numeric),
            ("hi😀 ", SkipReason::Emoji),
            ("https://example.com/a ", SkipReason::Url),
            ("example.com ", SkipReason::Url),
            ("snake_case ", SkipReason::Url),
            ("me@example.com ", SkipReason::Email),
            ("a가 ", SkipReason::MixedScript),
            ("привет ", SkipReason::MixedScript),
        ];
        for (input, reason) in cases {
            assert_eq!(
                feed(&mut t, input),
                vec![TokenOutcome::Skipped(reason)],
                "{input}"
            );
        }
    }

    #[test]
    fn separators_do_not_trigger_evaluation_on_their_own() {
        let mut t = Tokenizer::new();
        assert_eq!(feed(&mut t, "www."), vec![]);
        assert_eq!(feed(&mut t, "site.org"), vec![]);
        assert_eq!(
            t.feed_char(' '),
            Some(TokenOutcome::Skipped(SkipReason::Url))
        );
    }

    #[test]
    fn leading_separators_and_empty_tokens_produce_nothing() {
        let mut t = Tokenizer::new();
        assert_eq!(feed(&mut t, "  -- ' "), vec![]);
        assert!(!t.has_pending());
    }

    #[test]
    fn reset_clears_pending_state() {
        let mut t = Tokenizer::new();
        feed(&mut t, "abc-");
        t.reset();
        assert!(!t.has_pending());
        assert_eq!(feed(&mut t, "x "), vec![evaluate("x", " ")]);
    }

    #[test]
    fn hangul_words_are_eligible() {
        let mut t = Tokenizer::new();
        assert_eq!(feed(&mut t, "키보드가 "), vec![evaluate("키보드가", " ")]);
    }
}
