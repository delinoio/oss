use std::{fs, io};

use materialized_artifact::Artifact;

const PRELOAD: Artifact = Artifact::__new("preload", b"trusted preload", "fixedhash");

#[test]
fn existing_artifact_bytes_are_verified_before_reuse() {
    let directory = tempfile::tempdir().expect("create directory");
    let target = PRELOAD
        .materialize()
        .suffix(".dylib")
        .at(directory.path())
        .expect("materialize trusted bytes");
    assert_eq!(
        fs::read(&target).expect("read artifact"),
        b"trusted preload"
    );
    assert_eq!(
        PRELOAD
            .materialize()
            .suffix(".dylib")
            .at(directory.path())
            .expect("reuse trusted artifact"),
        target
    );

    fs::write(&target, b"untrusted preload").expect("replace bytes");
    assert_eq!(
        PRELOAD
            .materialize()
            .suffix(".dylib")
            .at(directory.path())
            .unwrap_err()
            .kind(),
        io::ErrorKind::InvalidData
    );
}

#[cfg(unix)]
#[test]
fn existing_artifact_symlink_is_rejected_without_following_it() {
    use std::os::unix::fs::symlink;

    let directory = tempfile::tempdir().expect("create directory");
    let outside = directory.path().join("outside");
    fs::write(&outside, b"untrusted preload").expect("write outside file");
    symlink(&outside, directory.path().join("preload_fixedhash.dylib"))
        .expect("create preload symlink");

    assert_eq!(
        PRELOAD
            .materialize()
            .suffix(".dylib")
            .at(directory.path())
            .unwrap_err()
            .kind(),
        io::ErrorKind::InvalidData
    );
    assert_eq!(
        fs::read(&outside).expect("read outside file"),
        b"untrusted preload"
    );
}
