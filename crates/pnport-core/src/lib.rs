//! Private graph, cache, and virtual filesystem shared by pnport hosts and
//! injection libraries.
pub mod application;
pub mod cache;
pub mod diagnostic;
pub mod executable;
pub mod graph;
pub mod launch;
#[cfg(unix)]
pub mod native_path;
pub mod node;
pub mod pe;
pub mod view;

#[cfg(windows)]
pub mod windows;

#[cfg(target_os = "macos")]
pub mod macos_process;
