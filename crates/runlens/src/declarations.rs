//! Bounded language intersection for declarations, independent of observations.
use std::collections::{HashSet, VecDeque};

use regex_automata::{
    Anchored, Input,
    dfa::{Automaton, StartKind, dense},
    nfa::thompson,
    util::syntax,
};

use crate::{config, error::Result};

#[derive(Debug, PartialEq, Eq)]
pub enum Overlap {
    Yes,
    No,
    Unknown,
}

pub fn overlap(inputs: &[String], outputs: &[String], os: &str) -> Result<Overlap> {
    intersect(inputs, outputs, os, 65_536)
}
fn intersect(
    inputs: &[String],
    outputs: &[String],
    os: &str,
    state_limit: usize,
) -> Result<Overlap> {
    if inputs.is_empty() || outputs.is_empty() {
        return Ok(Overlap::No);
    }
    let input = config::declaration_globs(inputs, os)?;
    let output = config::declaration_globs(outputs, os)?;
    let build = |patterns: &[&str]| {
        dense::Builder::new()
            .configure(
                dense::Config::new()
                    .start_kind(StartKind::Anchored)
                    .dfa_size_limit(Some(8 * 1024 * 1024))
                    .determinize_size_limit(Some(8 * 1024 * 1024)),
            )
            .syntax(syntax::Config::new().utf8(false))
            .thompson(
                thompson::Config::new()
                    .utf8(false)
                    .nfa_size_limit(Some(8 * 1024 * 1024)),
            )
            .build_many(patterns)
    };
    let (Ok(left), Ok(right), Ok(valid)) = (
        build(&input.iter().map(|g| g.regex()).collect::<Vec<_>>()),
        build(&output.iter().map(|g| g.regex()).collect::<Vec<_>>()),
        // Globs match bytes. Restrict witnesses to nonempty, valid UTF-8 paths
        // without NUL, so an invalid byte-string cannot prove an overlap.
        build(&[r"(?u)^[^\x00]+$"]),
    ) else {
        return Ok(Overlap::Unknown);
    };
    let start = Input::new(b"").anchored(Anchored::Yes);
    let (Ok(a), Ok(b), Ok(c)) = (
        left.start_state_forward(&start),
        right.start_state_forward(&start),
        valid.start_state_forward(&start),
    ) else {
        return Ok(Overlap::Unknown);
    };
    let mut seen = HashSet::from([(a, b, c)]);
    let mut pending = VecDeque::from([(a, b, c)]);
    let mut transitions = 0usize;
    while let Some((a, b, c)) = pending.pop_front() {
        if left.is_match_state(left.next_eoi_state(a))
            && right.is_match_state(right.next_eoi_state(b))
            && valid.is_match_state(valid.next_eoi_state(c))
        {
            return Ok(Overlap::Yes);
        }
        for byte in 1..=255 {
            transitions += 1;
            if transitions > 4_000_000 {
                return Ok(Overlap::Unknown);
            }
            let next = (
                left.next_state(a, byte),
                right.next_state(b, byte),
                valid.next_state(c, byte),
            );
            if left.is_dead_state(next.0)
                || right.is_dead_state(next.1)
                || valid.is_dead_state(next.2)
            {
                continue;
            }
            if !seen.contains(&next) {
                if seen.len() >= state_limit {
                    return Ok(Overlap::Unknown);
                }
                seen.insert(next);
                pending.push_back(next);
            }
        }
    }
    Ok(Overlap::No)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn declarations_intersect_without_observed_paths() {
        for (left, right, os, expected) in [
            ("src/**", "src/generated/**", "linux", Overlap::Yes),
            ("src/**", "src", "linux", Overlap::Yes),
            ("src/*.[ch]", "src/*.h", "linux", Overlap::Yes),
            ("{src,lib}/**", "lib/é*.rs", "linux", Overlap::Yes),
            ("Src/**", "src/generated/**", "windows", Overlap::Yes),
            ("Src/**", "src/generated/**", "linux", Overlap::No),
            ("src/*", "src/nested/file", "linux", Overlap::No),
            ("input/**", "output/**", "linux", Overlap::No),
            ("src/?.rs", "src/é.rs", "linux", Overlap::No),
        ] {
            assert_eq!(
                overlap(&[left.into()], &[right.into()], os).unwrap(),
                expected,
                "{left}, {right}, {os}"
            );
        }
        assert_eq!(
            intersect(&["src/**".into()], &["src/output/**".into()], "linux", 1).unwrap(),
            Overlap::Unknown
        );
    }
}
