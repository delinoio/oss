use std::{fs, io};

use super::{invalid_count, query_error};
use crate::error::{Code, Failure, Result};

const ONLINE: &str = "/sys/devices/system/cpu/online";

pub(super) fn logical() -> Result<usize> {
    logical_with(|| fs::read_to_string(ONLINE))
}

fn logical_with(read: impl FnOnce() -> io::Result<String>) -> Result<usize> {
    let list = read().map_err(|error| {
        let failure = query_error(&error);
        if failure.code == Code::BackendUnavailable {
            Failure::new(
                Code::BackendUnavailable,
                "Linux online CPU information is unavailable; check that this environment exposes \
                 it.",
            )
        } else {
            failure
        }
    })?;
    count_online(&list)
}

fn count_online(list: &str) -> Result<usize> {
    let list = list.trim_end_matches(['\n', '\r']);
    if list.is_empty() {
        return Err(invalid_count());
    }
    let mut count = 0usize;
    let mut previous_end = None;
    for entry in list.split(',') {
        let (first, last) = match entry.split_once('-') {
            Some((first, last)) => (parse_id(first)?, parse_id(last)?),
            None => {
                let id = parse_id(entry)?;
                (id, id)
            }
        };
        if first > last || previous_end.is_some_and(|end| first <= end) {
            return Err(invalid_count());
        }
        let length = last
            .checked_sub(first)
            .and_then(|span| span.checked_add(1))
            .ok_or_else(invalid_count)?;
        count = count.checked_add(length).ok_or_else(invalid_count)?;
        previous_end = Some(last);
    }
    if count == 0 {
        Err(invalid_count())
    } else {
        Ok(count)
    }
}

fn parse_id(value: &str) -> Result<usize> {
    if value.is_empty() || !value.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(invalid_count());
    }
    value.parse().map_err(|_| invalid_count())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn counts_sparse_and_ranged_online_cpus_without_allocating_entries() {
        for (list, count) in [
            ("0\n", 1),
            ("0-3\n", 4),
            ("0,2-4,8\n", 5),
            ("0-1000000\n", 1_000_001),
        ] {
            assert_eq!(count_online(list).unwrap(), count);
        }
    }

    #[test]
    fn rejects_invalid_and_overflowing_lists() {
        for list in [
            "",
            "\n",
            ",",
            "0,",
            "-1",
            "1-",
            "2-1",
            "0-1-2",
            "0,0",
            "0-2,2-4",
            "2,1",
            "0, 1",
            "0\n1",
            "0-a",
            "0-18446744073709551615",
        ] {
            assert_eq!(
                count_online(list).unwrap_err().code,
                Code::EnumerationFailed,
                "{list:?}"
            );
        }
    }

    #[test]
    fn classifies_read_failures_without_exposing_paths() {
        assert_eq!(
            logical_with(|| Err(io::ErrorKind::NotFound.into()))
                .unwrap_err()
                .code,
            Code::BackendUnavailable
        );
        assert_eq!(
            logical_with(|| Err(io::ErrorKind::PermissionDenied.into()))
                .unwrap_err()
                .code,
            Code::PermissionDenied
        );
        assert_eq!(logical_with(|| Ok("0-2\n".to_owned())).unwrap(), 3);
    }
}
