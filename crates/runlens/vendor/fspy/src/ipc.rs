use allocator_api2::alloc::Global;
use fspy_shared::ipc::{
    PathAccess,
    channel::{FrameReader, Receiver},
};

use crate::error::TrackingIncomplete;

/// The path accesses a run reported through the IPC channel.
pub struct ChannelAccesses {
    frames: FrameReader,
}

impl TryFrom<Receiver<Global>> for ChannelAccesses {
    type Error = TrackingIncomplete;

    /// Closes the channel and takes every record it collected.
    ///
    /// Never waits for tracked processes: closing reads one counter and
    /// shuts the channel's gate (see
    /// [`fspy_shared::ipc::channel::Receiver::close`]), so it runs inline
    /// however many records were reported.
    ///
    /// # Errors
    ///
    /// [`TrackingIncomplete`] when a tracked process could not record
    /// something it went on to do. What did arrive is then a subset of
    /// what the run really touched, so none of it is handed back.
    fn try_from(receiver: Receiver<Global>) -> Result<Self, TrackingIncomplete> {
        Ok(Self { frames: receiver.close().map_err(|_| TrackingIncomplete)? })
    }
}

impl ChannelAccesses {
    pub fn incomplete(&self) -> bool { self.frames.incomplete() }
    pub fn iter_path_accesses(&self) -> impl Iterator<Item = PathAccess<'_>> {
        self.frames.iter().map(|frame| {
            wincode::deserialize_exact(frame)
                .expect("committed frames are complete under the channel protocol")
        })
    }
}
