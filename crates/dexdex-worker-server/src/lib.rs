#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CommitMetadata {
    pub sha: String,
    pub parents: Vec<String>,
    pub message: String,
    pub authored_at_unix_ns: i64,
    pub committed_at_unix_ns: i64,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum CommitChainValidationError {
    EmptyCommitChain,
    MissingSha {
        index: usize,
    },
    MissingMessage {
        index: usize,
    },
    InvalidTimestamp {
        index: usize,
    },
    NonMonotonicCommitTime {
        index: usize,
    },
    MissingParentLink {
        index: usize,
        expected_parent_sha: String,
    },
}

pub fn validate_commit_chain(
    commit_chain: &[CommitMetadata],
) -> Result<(), CommitChainValidationError> {
    if commit_chain.is_empty() {
        return Err(CommitChainValidationError::EmptyCommitChain);
    }

    for (index, commit) in commit_chain.iter().enumerate() {
        if commit.sha.trim().is_empty() {
            return Err(CommitChainValidationError::MissingSha { index });
        }

        if commit.message.trim().is_empty() {
            return Err(CommitChainValidationError::MissingMessage { index });
        }

        if commit.authored_at_unix_ns <= 0 || commit.committed_at_unix_ns <= 0 {
            return Err(CommitChainValidationError::InvalidTimestamp { index });
        }

        if index > 0 {
            let previous = &commit_chain[index - 1];
            if commit.committed_at_unix_ns < previous.committed_at_unix_ns {
                return Err(CommitChainValidationError::NonMonotonicCommitTime { index });
            }

            if !commit.parents.iter().any(|parent| parent == &previous.sha) {
                return Err(CommitChainValidationError::MissingParentLink {
                    index,
                    expected_parent_sha: previous.sha.clone(),
                });
            }
        }
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{validate_commit_chain, CommitChainValidationError, CommitMetadata};

    fn commit(index: i64, sha: &str, parent: Option<&str>) -> CommitMetadata {
        let mut parents = Vec::new();
        if let Some(value) = parent {
            parents.push(value.to_owned());
        }

        CommitMetadata {
            sha: sha.to_owned(),
            parents,
            message: format!("commit-{index}"),
            authored_at_unix_ns: 1_000 + index,
            committed_at_unix_ns: 2_000 + index,
        }
    }

    #[test]
    fn accepts_ordered_real_commit_chain() {
        let chain = vec![
            commit(1, "sha-1", None),
            commit(2, "sha-2", Some("sha-1")),
            commit(3, "sha-3", Some("sha-2")),
        ];

        validate_commit_chain(&chain).unwrap();
    }

    #[test]
    fn rejects_empty_chain() {
        let error = validate_commit_chain(&[]).unwrap_err();
        assert_eq!(error, CommitChainValidationError::EmptyCommitChain);
    }

    #[test]
    fn rejects_missing_parent_link() {
        let chain = vec![commit(1, "sha-1", None), commit(2, "sha-2", Some("sha-x"))];

        let error = validate_commit_chain(&chain).unwrap_err();
        assert_eq!(
            error,
            CommitChainValidationError::MissingParentLink {
                index: 1,
                expected_parent_sha: "sha-1".to_owned(),
            }
        );
    }

    #[test]
    fn rejects_non_monotonic_commit_time() {
        let mut second = commit(2, "sha-2", Some("sha-1"));
        second.committed_at_unix_ns = 1_900;
        let chain = vec![commit(1, "sha-1", None), second];

        let error = validate_commit_chain(&chain).unwrap_err();
        assert_eq!(
            error,
            CommitChainValidationError::NonMonotonicCommitTime { index: 1 }
        );
    }
}
