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


#[cfg(windows)]
#[test]
fn windows_unc_ipc_paths_preserve_network_roots() {
    use fspy_shared::ipc::IpcPath;
    use std::path::Path;
    for prefix in [r"\\?\UNC\", r"\??\UNC\", r"\\.\UNC\", r"\\"] {
        let path = format!("{prefix}server\\share\\directory\\input.txt");
        let wide = path.encode_utf16().collect::<Vec<_>>();
        let ipc = IpcPath::from_wide(&wide);
        let converted = ipc.to_path_buf();
        assert!(converted.is_absolute());
        assert_eq!(converted, Path::new(r"\\server\share\directory\input.txt"));
        ipc.strip_path_prefix(Path::new(r"\\server\share"), |relative| {
            assert_eq!(relative.unwrap(), Path::new(r"directory\input.txt"));
        });
        ipc.strip_path_prefix(Path::new(r"C:\worktree"), |relative| assert!(relative.is_err()));
    }
}

#[test]
fn normalized_prefixes_preserve_owned_relative_paths() {
    use std::{ffi::OsStr, path::Path};
    let (path, base, expected) = if cfg!(windows) {
        (r"\\?\C:\repo\input", r"C:\repo", "input")
    } else {
        ("/repo/input", "/repo", "input")
    };
    assert_eq!(vt_path::strip_path_prefix(OsStr::new(path), OsStr::new(base)).unwrap().as_ref(), Path::new(expected));
    let absolute = vt_path::AbsolutePath::new(Path::new(path)).unwrap();
    let base = vt_path::AbsolutePath::new(Path::new(base)).unwrap();
    assert_eq!(absolute.strip_prefix(base).unwrap().unwrap().as_str(), expected);
}
