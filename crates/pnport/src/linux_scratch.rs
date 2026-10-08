//! Private Linux scratch inheritance captured before child creation.

#[derive(Default)]
pub(crate) struct ScratchSpace {
    pub(crate) all: Vec<u64>,
    pub(crate) free: Vec<u64>,
}

pub(crate) struct SpawnScratch {
    pub(crate) shares_vm: bool,
    pub(crate) parent_space: u64,
    pub(crate) inheritance: Option<ScratchSpace>,
}

impl SpawnScratch {
    pub(crate) fn capture(parent_space: u64, shares_vm: bool, space: &ScratchSpace) -> Self {
        Self {
            shares_vm,
            parent_space,
            // A private child can use each completed mapping independently.
            // Freeze this list at syscall admission, not at the later ptrace
            // creation event, which can follow other threads' mmap results.
            inheritance: (!shares_vm).then(|| ScratchSpace {
                all: space.all.clone(),
                free: space.all.clone(),
            }),
        }
    }
}

// An unknown or mismatched private creation never adopts a live parent pool.
pub(crate) fn private_fork_space(
    parent_space: u64,
    spawn: Option<SpawnScratch>,
) -> Option<ScratchSpace> {
    let spawn = spawn?;
    if spawn.shares_vm || spawn.parent_space != parent_space {
        return None;
    }
    spawn.inheritance
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn private_fork_excludes_mappings_published_after_creation_admission() {
        let mut parent = ScratchSpace {
            all: vec![0x1000, 0x2000],
            free: vec![0x1000],
        };
        let admitted = SpawnScratch::capture(7, false, &parent);
        // The kernel forks before the supervisor consumes a sibling's mmap
        // result. That new mapping exists only in the parent's address space.
        parent.all.push(0x3000);
        parent.free.push(0x3000);
        assert!(!admitted.shares_vm);
        assert_eq!(admitted.parent_space, 7);
        let mut child = private_fork_space(7, Some(admitted)).unwrap();
        assert_eq!(child.all, [0x1000, 0x2000]);
        assert_eq!(child.free.pop(), Some(0x2000));
        assert_eq!(child.free.pop(), Some(0x1000));
        assert_eq!(child.free.pop(), None);
        assert_eq!(parent.free, [0x1000, 0x3000]);
    }

    #[test]
    fn private_fork_requires_the_original_admitted_address_space() {
        let parent = ScratchSpace {
            all: vec![0x1000],
            free: vec![],
        };
        assert!(private_fork_space(7, None).is_none());
        assert!(private_fork_space(8, Some(SpawnScratch::capture(7, false, &parent))).is_none());
        assert!(private_fork_space(7, Some(SpawnScratch::capture(7, true, &parent))).is_none());
        // Empty but positively admitted inventory requests a fresh child slot.
        let empty = private_fork_space(
            7,
            Some(SpawnScratch::capture(7, false, &ScratchSpace::default())),
        )
        .unwrap();
        assert!(empty.all.is_empty());
        assert!(empty.free.is_empty());
    }

    #[test]
    fn shared_vm_clone_retains_the_live_pool_instead_of_copying_slots() {
        let space = ScratchSpace {
            all: vec![0x1000],
            free: vec![],
        };
        let admitted = SpawnScratch::capture(8, true, &space);
        assert!(admitted.shares_vm);
        assert_eq!(admitted.parent_space, 8);
        assert!(admitted.inheritance.is_none());
    }
}
