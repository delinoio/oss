use fspy_shared::{ipc::AccessMode, windows_access::creation_mode};

#[test]
fn windows_information_mutations_never_silently_drop_rename_or_delete() {
    use fspy_shared::windows_access::{information_mutation, InformationMutation};
    // Include both ordinary and bypass-access-check rename/link ABI classes.
    for class in [10, 11, 56, 57, 65, 66, 72, 73, 999] {
        assert_eq!(information_mutation(class), InformationMutation::Unresolved);
    }
    for class in [4, 13, 19, 20, 39, 64, 71, 75] {
        assert_eq!(information_mutation(class), InformationMutation::Write);
    }
    for class in [14, 16, 30, 41, 43, 61] {
        assert_eq!(information_mutation(class), InformationMutation::HandleOnly);
    }
}

#[test]
fn windows_creation_modes_preserve_mutating_intent_with_read_access() {
    for disposition in [0, 2, 3, 4, 5] {
        assert_eq!(creation_mode(AccessMode::READ, disposition, 0), AccessMode::READ | AccessMode::WRITE);
    }
    assert_eq!(creation_mode(AccessMode::READ, 1, 0), AccessMode::READ);
    assert!(creation_mode(AccessMode::READ, 1, 0x1000).contains(AccessMode::WRITE));
    assert!(creation_mode(AccessMode::READ, 6, 0).contains(AccessMode::UNSUPPORTED));
}
