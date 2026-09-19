//! Runlens patch: a per-execution, bounded, private collector for preload and
//! Linux static-executable observations. No unbounded per-process arenas.
#[cfg(target_os="linux")]
mod syscall_handler;
use std::{io, path::Path, sync::{Arc,Mutex}};
use fspy_shared::ipc::{IpcStr,PathAccess,channel::channel};
use fspy_shared_unix::{exec::ExecResolveConfig,payload::{Payload,encode_payload},spawn::handle_exec};
use futures_util::FutureExt;
use tokio_util::sync::CancellationToken;
use crate::{ChildTermination,Command,TrackedChild,error::SpawnError,ipc::ChannelAccesses};

pub struct SpyImpl { preload_path: Box<IpcStr> }
impl SpyImpl {
    pub fn init_in(directory:&Path)->io::Result<Self> {
        use materialized_artifact::{Artifact,artifact};
        const PRELOAD:Artifact=artifact!("fspy_preload","CARGO_CDYLIB_FILE_FSPY_PRELOAD_UNIX");
        let path=PRELOAD.materialize().suffix(".dylib").at(directory)?;
        Ok(Self {preload_path:path.as_path().into()})
    }
    pub(crate) async fn spawn(&self,mut command:Command,directory:&Path,capacity:usize,max_paths:usize,cancel:CancellationToken)->Result<TrackedChild,SpawnError> {
        let receiver=channel(directory,capacity,max_paths,allocator_api2::alloc::Global).map_err(SpawnError::ChannelCreation)?;
        let sender=receiver.conf().sender(allocator_api2::alloc::Global).ok_or_else(|| SpawnError::ChannelCreation(io::Error::other("collector closed during initialization")))?;
        let sender=Arc::new(Mutex::new(sender));
        #[cfg(target_os="linux")]
        let supervisor={
            // SAFETY: Runlens adopts orphaned descendants; reaping is restricted
            // further to this execution's owned process group.
            if unsafe {libc::prctl(libc::PR_SET_CHILD_SUBREAPER,1,0,0,0)} != 0 {return Err(SpawnError::OsSpawn(io::Error::last_os_error()));}
            let sender=Arc::clone(&sender);
            fspy_seccomp_unotify::supervisor::supervise(directory,move || syscall_handler::SyscallHandler::new(Arc::clone(&sender))).map_err(SpawnError::Supervisor)?
        };
        let payload=Payload {ipc_channel_conf:receiver.conf(),preload_path:&self.preload_path,
            #[cfg(target_os="linux")] seccomp_payload:supervisor.payload().clone()};
        let bump=bumpalo::Bump::new();
        let encoded=encode_payload(payload,&bump);
        let mut exec=command.get_exec();
        let pre_exec=handle_exec(&mut exec,ExecResolveConfig::search_path_enabled(None),&encoded,|mode,path| {
            if let Ok(sender)=sender.lock() {sender.send(&PathAccess {mode,path:path.into()});}
        }).map_err(|e|SpawnError::Injection(e.into()))?;
        let uses_seccomp=pre_exec.is_some();
        command.set_exec(exec);
        let mut native=command.into_tokio_command();
        native.process_group(0).kill_on_drop(true);
        // SAFETY: only upstream async-signal-safe seccomp installation runs
        // in the forked process, without touching the Rust allocator.
        unsafe {native.pre_exec(move || {if let Some(pre_exec)=&pre_exec {pre_exec.run()?;}Ok(())});}
        let mut child=tokio::task::spawn_blocking(move || native.spawn()).await.map_err(|e|SpawnError::OsSpawn(e.into()))?.map_err(SpawnError::OsSpawn)?;
        Ok(TrackedChild {
            stdin:child.stdin.take(),stdout:child.stdout.take(),stderr:child.stderr.take(),
            wait_handle:tokio::spawn(async move {
                let (status,lifecycle_incomplete)=crate::lifecycle::wait_unix(&mut child,cancel).await?;
                #[cfg(target_os="linux")]
                let supervisor_failed=match supervisor.stop().await {Ok((_,failed))=>failed,Err(_)=>true};
                #[cfg(not(target_os="linux"))]
                let supervisor_failed=false;
                let accesses=ChannelAccesses::try_from(receiver).map(|ipc_accesses|PathAccessIterable {ipc_accesses,uses_seccomp,supervisor_failed});
                Ok(ChildTermination {status,path_accesses:accesses,lifecycle_incomplete})
            }).map(|result|result?).boxed(),
        })
    }
}
pub struct PathAccessIterable {ipc_accesses:ChannelAccesses,uses_seccomp:bool,supervisor_failed:bool}
impl PathAccessIterable {
    pub fn incomplete(&self)->bool {self.supervisor_failed||self.ipc_accesses.incomplete()}
    pub fn attached(&self)->bool {self.uses_seccomp||self.iter().any(|a|a.mode.contains(fspy_shared::ipc::AccessMode::ATTACHED))}
    pub fn iter(&self)->impl Iterator<Item=PathAccess<'_>> {self.ipc_accesses.iter_path_accesses()}
}
