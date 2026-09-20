use fspy_shared::{ipc::AccessMode, windows_access::creation_mode};

#[test]
fn windows_creation_modes_preserve_mutating_intent_with_read_access() {
    for disposition in [0, 2, 3, 4, 5] {
        assert_eq!(creation_mode(AccessMode::READ, disposition, 0), AccessMode::READ | AccessMode::WRITE);
    }
    assert_eq!(creation_mode(AccessMode::READ, 1, 0), AccessMode::READ);
    assert!(creation_mode(AccessMode::READ, 1, 0x1000).contains(AccessMode::WRITE));
    assert!(creation_mode(AccessMode::READ, 6, 0).contains(AccessMode::UNSUPPORTED));
}
