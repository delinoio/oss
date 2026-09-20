use serde::Serialize;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum Code {
    Arguments,
    InvalidPattern,
    InvalidCapture,
    InvalidUtf8,
    NoMatch,
    InvalidTime,
    InvalidFormat,
    UnknownTimezone,
    AmbiguousTime,
    NonexistentTime,
    TimeRange,
    InvalidBase64,
    InvalidDigest,
    MalformedRecord,
    EmptyManifest,
    ReadFailed,
    WriteFailed,
    OutputExists,
    UnsafeDestination,
    Permissions,
    PublishFailed,
    Cancelled,
    Runtime,
}

#[derive(Clone, Copy, Debug)]
pub struct Error {
    pub code: Code,
    pub exit: u8,
}

pub type Result<T> = std::result::Result<T, Error>;

impl Error {
    pub const fn argument(code: Code) -> Self {
        Self { code, exit: 2 }
    }

    pub const fn runtime(code: Code) -> Self {
        Self { code, exit: 1 }
    }

    pub fn message(self) -> &'static str {
        match self.code {
            Code::Arguments => "Invalid or conflicting arguments; use the command's --help.",
            Code::InvalidPattern => "Provide a nonempty literal pattern or a valid Rust regex.",
            Code::InvalidCapture => {
                "Reference only capture groups declared in the regex; use $$ for a literal dollar."
            }
            Code::InvalidUtf8 => "Text input must be valid UTF-8.",
            Code::NoMatch => "No match found; omit --require-match to keep unchanged input.",
            Code::InvalidTime => {
                "Provide a valid date/time in the selected format, without leap seconds."
            }
            Code::InvalidFormat => {
                "Use a supported strftime directive; timezone abbreviations cannot be parsed."
            }
            Code::UnknownTimezone => "Select UTC or a bundled IANA timezone name.",
            Code::AmbiguousTime => {
                "The local time occurs twice; provide an explicit offset or choose another \
                 calendar time."
            }
            Code::NonexistentTime => "The local time does not exist; choose another calendar time.",
            Code::TimeRange => {
                "The operation exceeds years 1 through 9999 or the selected output's representable \
                 range."
            }
            Code::InvalidBase64 => {
                "Invalid Base64; check alphabet, padding mode, and canonical trailing bits."
            }
            Code::InvalidDigest => {
                "Provide a digest with the selected encoding and algorithm's exact length."
            }
            Code::MalformedRecord => {
                "Use GNU untagged checksum records for the selected algorithm."
            }
            Code::EmptyManifest => "The checksum manifest must contain at least one record.",
            Code::ReadFailed => {
                "Could not read input; check its availability and access permissions."
            }
            Code::WriteFailed => {
                "Could not write output; check the destination, available space, and downstream \
                 reader."
            }
            Code::OutputExists => "Output already exists; use --force to replace it.",
            Code::UnsafeDestination => {
                "Replacement requires a regular file without symbolic links or multiple hard links."
            }
            Code::Permissions => {
                "Could not preserve access permissions; use a destination where permissions can be \
                 preserved."
            }
            Code::PublishFailed => {
                "Could not publish output; check destination permissions and open file handles."
            }
            Code::Cancelled => "Operation interrupted; completed publication is not undone.",
            Code::Runtime => "Could not initialize or complete the operation.",
        }
    }
}

pub fn report(error: Error) {
    // Never format external errors: even parser and OS errors can contain input.
    tracing::error!(code = ?error.code, message = error.message(), "error: operation failed");
}
