// SPDX-License-Identifier: Apache-2.0

pub(crate) const STATUS_SUCCESS: i32 = 0;
pub(crate) const STATUS_BUFFER_OVERFLOW: i32 = 0x8000_0005u32 as i32;
pub(crate) const STATUS_NO_MORE_FILES: i32 = 0x8000_0006u32 as i32;

/// The first query on a file object has a distinct partial-record rule.
/// Restarting a scan does not make a later query the first one again.
#[derive(Clone, Copy)]
pub(crate) struct QueryState {
    first: bool,
}

impl QueryState {
    pub(crate) fn new() -> Self {
        Self { first: true }
    }

    /// Return the number of name bytes that fit in the first partial record.
    /// A later query leaves the pending record untouched when it cannot fit.
    pub(crate) fn partial_name_bytes(
        self,
        header: usize,
        name_bytes: usize,
        capacity: usize,
        used: usize,
    ) -> Option<usize> {
        (self.first && used == 0 && header + name_bytes > capacity)
            .then(|| (capacity - header).min(name_bytes) & !1)
    }

    pub(crate) fn finish(&mut self, used: usize, pending: bool, partial: bool) -> i32 {
        let first = self.first;
        self.first = false;
        if partial && first {
            STATUS_BUFFER_OVERFLOW
        } else if used != 0 || pending {
            STATUS_SUCCESS
        } else {
            STATUS_NO_MORE_FILES
        }
    }
}
