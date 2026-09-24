//! Fast mpsc IPC channel implementation based on shared memory.
//!
//! The channel is crash-tolerant and nonblocking on both ends: any sender
//! process may die (or keep running) at any point without preventing the
//! receiver from closing the channel and reading every committed frame. See
//! the `shm_io` module for the underlying protocol.

mod shm_io;

use std::{env::temp_dir, ffi::OsStr, io, num::NonZeroUsize, path::PathBuf};

use allocator_api2::alloc::Allocator;
use fspy_nostd::Fat;
use fspy_nostd_alloc::OsCString;
use fspy_shm::Mapping;
use shm_io::{SealError, ShmReader, ShmWriter};

/// Reads the committed frames of a sealed channel; borrows the shared
/// mapping, which stays alive (and mapped) until this value drops.
pub type FrameReader = shm_io::ShmReader<Mapping>;
use uuid::Uuid;
use wincode::{SchemaRead, SchemaWrite, Serialize as _, config::DefaultConfig};

use super::IpcStr;

/// Prefix of shared-memory backing file names inside the system temporary
/// directory.
///
/// The files sit directly in the temporary directory. A shared subdirectory
/// would belong to whichever user created it first and block everyone else;
/// uniquely named `0o600` files in the sticky-bit temp directory avoid that.
const SHM_BACKING_PREFIX: &str = "vite-task-fspy-";

/// Descriptor slots in every channel, one per record.
///
/// Fixed here so both ends agree on it without carrying it between them.
/// At the 4 GiB a tracked run gets it is one 8-byte slot per 56 payload
/// bytes, and records run a few hundred bytes each, so payload space runs
/// out long before slots do. The table is sparse address space until the
/// slots are claimed, so a channel pays only for the ones it uses.
const SLOTS: usize = 1 << 26;

/// Serializable configuration to create channel senders.
///
/// A conf is a view: it borrows the path the [`Receiver`] owns (or, in a
/// receiving process, the payload bytes it was deserialized from), so
/// materializing and serializing one allocates nothing.
#[derive(SchemaWrite, SchemaRead, Clone, Copy, Debug)]
pub struct ChannelConf<'a> {
    shm_id: &'a IpcStr,
}

/// Creates a mpsc IPC channel and returns its receiver. [`Receiver::conf`]
/// derives the serializable configuration that other processes use to create
/// senders.
#[expect(
    clippy::missing_errors_doc,
    reason = "non-vt crate: cannot use vt_str/vt_path types"
)]
pub fn channel<A: Allocator>(capacity: usize, allocator: A) -> io::Result<Receiver<A>> {
    let shm_c_path = os_c_string(shm_backing_path()?.as_os_str(), allocator)?;
    let handle =
        fspy_shm::create(shm_c_path.as_c_str().as_thin(), capacity).map_err(shm_error_to_io)?;
    // The keeper exists from here on, so every error path below cleans up.
    let keeper = ShmKeeper { path: shm_c_path };
    let mapping = handle.map().map_err(shm_error_to_io)?;

    // Prove the region can host the protocol — the same fallible attach
    // senders perform — so a size the two halves do not fit in fails the
    // task now, not at its first record.
    // SAFETY: the region was just created zero-initialized and is only
    // accessed through the `shm_io` protocol.
    if unsafe { ShmWriter::new(&mapping, SLOTS) }.is_none() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "the shared-memory capacity has no room for the descriptor table",
        ));
    }

    Ok(Receiver { keeper, mapping })
}

/// Encodes `path` as an owned NUL-terminated platform C string.
fn os_c_string<A: Allocator>(path: &OsStr, allocator: A) -> io::Result<OsCString<Fat, A>> {
    let mut units = os_units(path, allocator);
    units.push(0);
    OsCString::from_vec_with_nul(units)
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "path contains NUL"))
}

#[cfg(unix)]
fn os_units<A: Allocator>(path: &OsStr, allocator: A) -> allocator_api2::vec::Vec<u8, A> {
    use std::os::unix::ffi::OsStrExt as _;

    let mut units = allocator_api2::vec::Vec::with_capacity_in(path.len() + 1, allocator);
    units.extend_from_slice(path.as_bytes());
    units
}

#[cfg(windows)]
fn os_units<A: Allocator>(path: &OsStr, allocator: A) -> allocator_api2::vec::Vec<u16, A> {
    use std::os::windows::ffi::OsStrExt as _;

    let mut units = allocator_api2::vec::Vec::with_capacity_in(path.len() + 1, allocator);
    for unit in path.encode_wide() {
        units.push(unit);
    }
    units
}

#[cfg(unix)]
fn shm_error_to_io(error: fspy_nostd::Error) -> io::Error {
    io::Error::from_raw_os_error(error.raw_os_error())
}

#[cfg(windows)]
fn shm_error_to_io(error: fspy_nostd::Error) -> io::Error {
    io::Error::from_raw_os_error(error.raw_os_error().cast_signed())
}

/// Returns a fresh absolute path for a shared-memory backing file.
fn shm_backing_path() -> io::Result<PathBuf> {
    // `temp_dir` reflects `TMPDIR` verbatim, which may be relative. The path
    // travels to processes with other working directories, so resolve it
    // against the creator's current directory first.
    let path = std::path::absolute(temp_dir())?.join(format!(
        "{SHM_BACKING_PREFIX}{}.shm",
        Uuid::new_v4().simple()
    ));
    #[cfg(windows)]
    let path = to_verbatim_if_long(path)?;
    Ok(path)
}

/// Converts long paths to verbatim (`\\?\`) form up front, so every later use
/// of the path — creation here, opening in any process, removal — stays clear
/// of the legacy `MAX_PATH` limit without relying on the system's long-path
/// opt-in, which the arbitrary processes opening shared memory could not
/// count on anyway.
#[cfg(windows)]
fn to_verbatim_if_long(path: PathBuf) -> io::Result<PathBuf> {
    use std::os::windows::ffi::OsStrExt as _;

    use omnipath::windows::WinPathExt as _;

    // The length at which std's own Windows path conversion switches to a
    // verbatim path.
    const VERBATIM_THRESHOLD: usize = 248;

    if path.as_os_str().encode_wide().count() >= VERBATIM_THRESHOLD {
        return path.to_verbatim();
    }
    Ok(path)
}

/// Keeps the shared memory's backing path alive and removes it on drop.
///
/// Removal is cleanup, not a stop signal: later opens fail, but existing
/// handles and mappings keep reading and writing; see [`fspy_shm::remove`].
struct ShmKeeper<A: Allocator> {
    path: OsCString<Fat, A>,
}

impl<A: Allocator> Drop for ShmKeeper<A> {
    fn drop(&mut self) {
        let _ = fspy_shm::remove(self.path.as_c_str().as_thin());
    }
}

impl ChannelConf<'_> {
    /// Creates a sender, or `None` when the channel is already over.
    ///
    /// Never blocks. `None` means the receiver removed the backing file,
    /// or sealed the region before removing it and this call caught the
    /// gate in between. Either way whatever the caller does next happens
    /// past the receiver's boundary, so recording nothing loses nothing.
    ///
    /// # Panics
    ///
    /// When the channel is there but cannot be attached to: its path is
    /// unreadable, the file refuses to open or map, or the region cannot
    /// hold the protocol. A process with no sender has no way to tell the
    /// receiver it recorded nothing, and a trace that silently omits every
    /// access a process made is worse than no trace, so it stops here.
    #[must_use]
    pub fn sender<A: Allocator>(&self, allocator: A) -> Option<Sender> {
        // The allocation is transient: the decoded path only has to outlive
        // the open call below, and dropping it hands the space back to a
        // bump allocator, whose most recent allocation it is.
        let shm_path = self
            .shm_id
            .to_os_c_string_in(allocator)
            .expect("the channel's shared-memory path is not a valid C string");
        let mapping = match fspy_shm::open(shm_path.as_c_str().as_thin()) {
            Ok(handle) => handle.map().expect("cannot map the shared-memory channel"),
            Err(error) => {
                let error = shm_error_to_io(error);
                // The receiver removed the backing file, so it has already
                // stopped collecting.
                if error.kind() == io::ErrorKind::NotFound {
                    return None;
                }
                panic!("cannot open the shared-memory channel: {error}");
            }
        };
        // SAFETY: `mapping` is a freshly mapped shared memory region created
        // zero-initialized by `channel` and accessed only through the
        // `shm_io` protocol by every attached process.
        let writer = unsafe { ShmWriter::new(mapping, SLOTS) }
            .expect("the shared-memory region cannot hold the channel");
        // The receiver sealed the region but has not removed it yet.
        if writer.is_closed() {
            return None;
        }
        Some(Sender { writer })
    }
}

pub struct Sender {
    writer: ShmWriter<Mapping>,
}

impl Sender {
    /// Fails a future seal when an intercepted action cannot be recorded.
    pub fn mark_incomplete(&self) {
        self.writer.mark_incomplete();
    }

    /// Serializes one record into a committed frame.
    ///
    /// A claim the channel refuses is skipped, because that is all a sender
    /// inside an intercepted call can do: the channel has closed, so the
    /// record belongs past the receiver's boundary, or the region is full
    /// and the failed claim already set the CLOSED gate to say so.
    ///
    /// # Panics
    ///
    /// When the record's serialized size disagrees with the bytes it then
    /// writes. Nothing the caller passes can cause that, so it is a defect
    /// in this crate or its codec, and a trace built on it would be wrong
    /// in ways the receiver cannot see.
    pub fn send<T: SchemaWrite<DefaultConfig, Src = T>>(&self, value: &T) {
        let serialized_size =
            T::serialized_size(value).expect("a record cannot report its serialized size");
        let frame_size = usize::try_from(serialized_size)
            .ok()
            .and_then(NonZeroUsize::new)
            .expect("a record reports a serialized size of zero, or one no frame could hold");
        let Ok(mut frame) = self.writer.claim_frame(frame_size) else {
            return;
        };
        let mut buf: &mut [u8] = &mut frame;
        T::serialize_into(&mut buf, value).expect("a record will not serialize into its own frame");
        assert!(
            buf.is_empty(),
            "a record wrote fewer bytes than the size it reported"
        );
        frame.finish();
    }
}

// SAFETY: `Sender` only accesses the shared mapping through the `shm_io`
// protocol, which synchronizes concurrent writers and the receiver with
// atomic operations; the mapping's address is stable and independently owned.
unsafe impl Send for Sender {}

// SAFETY: see the `Send` impl; `ShmWriter`'s shared-reference API is
// internally synchronized by the protocol.
unsafe impl Sync for Sender {}

/// The unique receiver side of an IPC channel.
///
/// Holds the shared memory and its backing file alive for as long as senders
/// may attach; [`Receiver::close`] (or dropping) removes the backing file.
pub struct Receiver<A: Allocator> {
    /// Keeps the shared memory's backing file alive for as long as senders
    /// may attach.
    keeper: ShmKeeper<A>,
    mapping: Mapping,
}

// SAFETY: `Receiver` only holds the mapping; it accesses it exclusively
// through the `shm_io` protocol in `close`, which synchronizes with senders
// via atomic operations. The mapping's address is stable and independently
// owned.
unsafe impl<A: Allocator + Send> Send for Receiver<A> {}

// SAFETY: see the `Send` impl.
unsafe impl<A: Allocator + Sync> Sync for Receiver<A> {}

impl<A: Allocator> Receiver<A> {
    /// Returns the serializable configuration other processes pass to
    /// [`ChannelConf::sender`], borrowing this receiver's storage.
    #[must_use]
    pub fn conf(&self) -> ChannelConf<'_> {
        ChannelConf {
            shm_id: IpcStr::from_os_c_str(self.keeper.path.as_c_str()),
        }
    }

    /// Closes the channel and returns every committed frame, borrowed from
    /// the shared mapping that moves into the returned [`FrameReader`].
    ///
    /// Never blocks on senders: it reads the claim counter once, which
    /// fixes how far reading goes, and shuts the gate so no later claim
    /// succeeds. Committed frames become readable in place. A sender that
    /// was still filling a frame keeps running, and its frame may or may
    /// not appear depending on whether it commits before the read reaches
    /// that slot — either way the operation it describes happens after the
    /// receiver stopped collecting. The mapping is released when the
    /// returned [`FrameReader`] drops.
    ///
    /// # Errors
    ///
    /// [`RecordsLost`] when a sender could not record something it went on
    /// to do. There is no complete set of records then, so none are handed
    /// back, and a caller that needs the trace has to treat the run as
    /// untracked rather than as having reported nothing.
    ///
    /// # Panics
    ///
    /// When the region cannot hold the protocol, which [`channel`] proved
    /// it could before any sender saw it.
    pub fn close(self) -> Result<FrameReader, RecordsLost> {
        let Self { keeper, mapping } = self;
        // SAFETY: `mapping` was created zero-initialized by `channel`, its
        // address is stable and independently owned, and all attached
        // processes access it only through the `shm_io` protocol.
        let sealed = unsafe { ShmReader::seal(mapping, SLOTS) };
        // Remove the backing file only after the gate is shut. A process
        // that attaches in between finds a closed channel and gives up
        // cleanly; one that found the file already gone could not attach at
        // all, and so could not report whatever it then failed to record.
        drop(keeper);
        match sealed {
            Ok(reader) => Ok(reader),
            // This receiver is the only one that could have sealed, and it
            // is gone by now, so the gate can only be a sender's report.
            Err(SealError::Closed) => Err(RecordsLost),
            Err(SealError::UnsupportedRegion) => {
                panic!("the shared-memory region cannot hold the channel")
            }
        }
    }
}

/// A sender could not record something, so the receiver has no complete
/// set of records to hand back.
///
/// The region filled up, or a record came out longer than one frame can
/// hold. Both are the channel running out of room rather than anything the
/// senders did wrong, and both leave the run untracked.
#[derive(thiserror::Error, Clone, Copy, PartialEq, Eq, Debug)]
#[error("a sender ran out of room in the shared-memory channel")]
pub struct RecordsLost;
