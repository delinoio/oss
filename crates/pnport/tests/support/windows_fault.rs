use std::{io, path::Path, process::Command};

#[derive(Debug, PartialEq, Eq)]
pub enum Symbols {
    Resolved(String),
    Unavailable,
}

pub fn symbolize(
    image: &Path,
    offset: usize,
    run: impl FnOnce(&mut Command) -> io::Result<(bool, Vec<u8>)>,
) -> Symbols {
    let mut command = Command::new("llvm-symbolizer.exe");
    command
        .env_remove("LLVM_SYMBOLIZER_OPTS")
        .arg("--relative-address")
        .arg("--obj")
        .arg(image)
        .arg(format!("0x{offset:x}"));
    let Ok((true, stdout)) = run(&mut command) else {
        return Symbols::Unavailable;
    };
    let Ok(symbols) = String::from_utf8(stdout) else {
        return Symbols::Unavailable;
    };
    if symbols.lines().any(|line| {
        let mut location = line.rsplitn(3, ':');
        let column = location.next().and_then(|value| value.parse::<u32>().ok());
        let line_number = location.next().and_then(|value| value.parse::<u32>().ok());
        let file = location.next();
        column.is_some()
            && line_number.is_some_and(|number| number > 0)
            && file.is_some_and(|name| !name.is_empty() && name != "??")
    }) {
        Symbols::Resolved(symbols)
    } else {
        Symbols::Unavailable
    }
}

#[cfg(test)]
mod tests {
    use std::ffi::OsStr;

    use super::*;

    #[test]
    fn loaded_image_is_the_only_symbolizer_object() {
        let debug = Path::new("C:/build/debug/pnport_preload.dll");
        let installed = Path::new("C:/installed stage/유니코드/pnport_preload.dll");
        for image in [installed, debug] {
            let result = symbolize(image, 0x123, |command| {
                assert_eq!(command.get_program(), "llvm-symbolizer.exe");
                assert!(command
                    .get_envs()
                    .any(|(name, value)| { name == "LLVM_SYMBOLIZER_OPTS" && value.is_none() }));
                assert_eq!(
                    command.get_args().collect::<Vec<_>>(),
                    [
                        OsStr::new("--relative-address"),
                        OsStr::new("--obj"),
                        image.as_os_str(),
                        OsStr::new("0x123"),
                    ]
                );
                Ok((
                    true,
                    b"fault_function\nC:\\source\\preload.rs:42:7\n\n".to_vec(),
                ))
            });
            assert!(matches!(result, Symbols::Resolved(_)));
        }
    }

    #[test]
    fn missing_or_unusable_symbols_are_explicitly_unavailable() {
        let image = Path::new("installed stage/pnport_preload.dll");
        for (success, stdout) in [
            (true, b"".as_slice()),
            (true, b"??\n??:0:0\n\n".as_slice()),
            (true, b"function_without_source\n??:0:0\n\n".as_slice()),
            (false, b"function\nsource.rs:42:7\n".as_slice()),
            (true, b"\xff".as_slice()),
        ] {
            let mut calls = 0;
            assert_eq!(
                symbolize(image, 0x456, |_| {
                    calls += 1;
                    Ok((success, stdout.to_vec()))
                }),
                Symbols::Unavailable
            );
            assert_eq!(calls, 1);
        }
        assert_eq!(
            symbolize(image, 0x456, |_| Err(io::ErrorKind::NotFound.into())),
            Symbols::Unavailable
        );
    }
}
