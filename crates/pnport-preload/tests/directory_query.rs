// SPDX-License-Identifier: Apache-2.0

#[path = "../src/directory_query.rs"]
mod directory_query;

use directory_query::{QueryState, STATUS_BUFFER_OVERFLOW, STATUS_NO_MORE_FILES, STATUS_SUCCESS};

#[test]
fn first_partial_record_and_later_no_fit_keep_distinct_statuses() {
    let mut state = QueryState::new();
    assert_eq!(state.partial_name_bytes(12, 20, 17, 0), Some(4));
    assert_eq!(state.finish(16, true, true), STATUS_BUFFER_OVERFLOW);

    assert_eq!(state.partial_name_bytes(12, 20, 17, 0), None);
    assert_eq!(state.finish(0, true, false), STATUS_SUCCESS);
    assert_eq!(state.finish(32, false, false), STATUS_SUCCESS);
    assert_eq!(state.finish(0, false, false), STATUS_NO_MORE_FILES);
}

#[test]
fn restart_does_not_restore_first_query_partial_output() {
    let mut state = QueryState::new();
    assert_eq!(state.finish(14, true, false), STATUS_SUCCESS);
    assert_eq!(state.partial_name_bytes(12, 20, 17, 0), None);
    assert_eq!(state.finish(0, true, false), STATUS_SUCCESS);
}
