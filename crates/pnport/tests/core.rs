use std::{fs, io::Write, path::Path};

use pnport::{
    cache::{Cache, Operation, State},
    diagnostic::Code,
    graph::{Graph, Input},
    view::View,
};
use serde_json::{json, Value};

fn data() -> Value {
    json!({
        "enableTopLevelFallback": true,
        "ignorePatternData": null,
        "dependencyTreeRoots": [{"name":"root","reference":"workspace:."}],
        "fallbackPool": [], "fallbackExclusionList": [],
        "packageRegistryData": [
            [null, [[null,{"packageLocation":"./","packageDependencies":[["dep","npm:1"],["alias",["dep","npm:1"]],["@scope/pkg","npm:1"]],"linkType":"SOFT","discardFromLookup":true}]]],
            ["root", [["workspace:.",{"packageLocation":"./","packageDependencies":[["dep","npm:1"],["alias",["dep","npm:1"]],["@scope/pkg","npm:1"]],"linkType":"SOFT"}]]],
            ["dep",[["npm:1",{"packageLocation":"./cache.zip/node_modules/dep/","packageDependencies":[],"linkType":"HARD"}]]],
            ["@scope/pkg",[["npm:1",{"packageLocation":"./cache.zip/node_modules/@scope/pkg/","packageDependencies":[],"linkType":"HARD"}]]]
        ]
    })
}
fn inline(root: &Path, data: &Value) {
    let value = serde_json::to_string_pretty(data)
        .unwrap()
        .replace('\\', "\\\\")
        .replace('\'', "\\'")
        .replace('\n', "\\\n");
    fs::write(
        root.join(".pnp.cjs"),
        format!(
            "// UTF-8 π before the payload\nconst RAW_RUNTIME_STATE =\n'{value}';\nthrow new \
             Error('must never execute');\n"
        ),
    )
    .unwrap();
}
fn archive(path: &Path, files: &[(&str, &[u8])]) {
    let mut zip = zip::ZipWriter::new(fs::File::create(path).unwrap());
    for (name, bytes) in files {
        zip.start_file(
            *name,
            zip::write::SimpleFileOptions::default().unix_permissions(0o644),
        )
        .unwrap();
        zip.write_all(bytes).unwrap();
    }
    zip.finish().unwrap();
}
fn fixture() -> tempfile::TempDir {
    let root = tempfile::tempdir().unwrap();
    inline(root.path(), &data());
    archive(
        &root.path().join("cache.zip"),
        &[
            ("node_modules/dep/file.txt", b"package bytes"),
            ("node_modules/dep/package.json", br#"{"name":"dep"}"#),
            (
                "node_modules/@scope/pkg/package.json",
                br#"{"name":"@scope/pkg"}"#,
            ),
        ],
    );
    root
}

#[test]
fn inline_and_split_load_without_executing_javascript() {
    let root = fixture();
    let graph = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
    let root_path = fs::canonicalize(root.path()).unwrap();
    assert!(!graph.managed(&root_path.join("source.ts")));
    assert!(graph.managed(&root_path.join("cache.zip/node_modules/dep/file.txt")));
    assert!(!graph.managed(&root_path.parent().unwrap().join("unrelated.ts")));
    let restored = Graph::from_snapshot(graph.snapshot.clone()).unwrap();
    assert!(!restored.managed(&root_path));
    assert!(restored.managed(&root_path.join("cache.zip/node_modules/dep/file.txt")));
    assert_eq!(
        graph.resolve("dep", &root_path).unwrap(),
        graph.resolve("alias", &root_path).unwrap()
    );
    fs::write(
        root.path().join(".pnp.cjs"),
        "const pnpDataFilepath = path.resolve(__dirname, \".pnp.data.json\");\nthrow Error('do \
         not execute');",
    )
    .unwrap();
    fs::write(
        root.path().join(".pnp.data.json"),
        serde_json::to_vec(&data()).unwrap(),
    )
    .unwrap();
    let split = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
    assert_eq!(split.snapshot.inputs.len(), 2);
    assert_eq!(
        graph.resolve("dep", &root_path).unwrap(),
        split.resolve("dep", &root_path).unwrap()
    );
    fs::write(root.path().join(".pnp.data.json"), b"{}").unwrap();
    assert_eq!(
        split.unchanged().unwrap_err().code,
        Code::PnportGraphChanged
    );
}
#[test]
fn malformed_graphs_fail_without_panic_or_input_disclosure() {
    let root = fixture();
    for bad in [
        json!({}),
        {
            let mut d = data();
            d["packageRegistryData"][0][1] = json!([]);
            d
        },
        {
            let mut d = data();
            d["packageRegistryData"][1][1][0][1]["packageDependencies"] =
                json!([["canary-secret", "missing"]]);
            d
        },
    ] {
        inline(root.path(), &bad);
        let error = Graph::load(&root.path().join(".pnp.cjs")).err().unwrap();
        assert_eq!(error.code, Code::PnportManifestInvalid);
        assert!(!error.to_string().contains("canary-secret"));
    }
}
#[test]
fn view_reads_zip_scopes_aliases_and_preserves_project_writes() {
    let root = fixture();
    let graph = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
    let root_path = fs::canonicalize(root.path()).unwrap();
    let cache_root = tempfile::tempdir().unwrap();
    let session = tempfile::tempdir().unwrap();
    let mut view = View::new(
        graph,
        Cache::open(cache_root.path().join("cache")).unwrap(),
        session.path().to_owned(),
    );
    let dep = view
        .translate(&root_path.join("node_modules/dep/file.txt"))
        .unwrap();
    assert_eq!(fs::read(&dep.physical).unwrap(), b"package bytes");
    assert!(dep.readonly);
    let alias = view
        .translate(&root_path.join("node_modules/alias/file.txt"))
        .unwrap();
    assert_eq!(alias.logical, dep.logical);
    let dir = view.translate(&root_path.join("node_modules")).unwrap();
    let mut names: Vec<_> = fs::read_dir(dir.physical)
        .unwrap()
        .map(|e| e.unwrap().file_name())
        .collect();
    names.sort();
    assert_eq!(names, vec!["@scope", "alias", "dep"]);
    assert!(
        view.translate(&root_path.join("node_modules/@scope/pkg/package.json"))
            .unwrap()
            .readonly
    );
    assert!(
        !view
            .translate(&root_path.join("output.txt"))
            .unwrap()
            .readonly
    );
    assert!(!root_path.join("node_modules").exists());
    fs::create_dir(root_path.join("node_modules")).unwrap();
    assert_eq!(
        view.translate(&root_path.join("node_modules/dep/file.txt"))
            .unwrap_err()
            .code,
        Code::PnportFilesystemConflict
    );
}
#[test]
fn leases_survive_cleanup_and_cache_corruption_is_not_reused() {
    let root = fixture();
    let cache_root = tempfile::tempdir().unwrap();
    let cache = Cache::open(cache_root.path().join("cache")).unwrap();
    let lease = cache.materialize(&root.path().join("cache.zip")).unwrap();
    let second = cache.materialize(&root.path().join("cache.zip")).unwrap();
    assert_eq!(lease.content, second.content);
    assert!(matches!(
        cache.entries(Operation::Clean).unwrap()[0].state,
        State::Active
    ));
    let changed = lease.content.join("node_modules/dep/file.txt");
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&changed, fs::Permissions::from_mode(0o600)).unwrap();
    }
    #[cfg(windows)]
    #[allow(
        clippy::permissions_set_readonly_false,
        reason = "Windows-only code clears FILE_ATTRIBUTE_READONLY; Unix mode changes use \
                  PermissionsExt"
    )]
    {
        let mut permissions = fs::metadata(&changed).unwrap().permissions();
        permissions.set_readonly(false);
        fs::set_permissions(&changed, permissions).unwrap();
    }
    fs::write(&changed, b"corrupt").unwrap();
    assert!(cache.materialize(&root.path().join("cache.zip")).is_err());
    assert!(matches!(
        cache.entries(Operation::Clean).unwrap()[0].state,
        State::Active
    ));
    drop(second);
    drop(lease);
    assert!(matches!(
        cache.entries(Operation::Prune).unwrap()[0].state,
        State::Corrupt
    ));
    assert!(matches!(
        cache.entries(Operation::Clean).unwrap()[0].state,
        State::Removed
    ));
    assert!(cache.entries(Operation::List).unwrap().is_empty());
}
#[test]
fn archive_traversal_and_unknown_ownership_are_preserved_or_rejected() {
    let root = tempfile::tempdir().unwrap();
    let cache_root = tempfile::tempdir().unwrap();
    let cache = Cache::open(cache_root.path().join("cache")).unwrap();
    archive(
        &root.path().join("unsafe.zip"),
        &[("../outside", b"escape")],
    );
    assert!(cache.materialize(&root.path().join("unsafe.zip")).is_err());
    assert!(!cache_root.path().join("outside").exists());
    fs::create_dir(cache_root.path().join("cache/foreign")).unwrap();
    fs::write(cache_root.path().join("cache/foreign/keep"), "retained").unwrap();
    cache.entries(Operation::Clean).unwrap();
    assert!(cache_root.path().join("cache/foreign/keep").exists());
}

#[cfg(unix)]
#[test]
fn confined_dangling_archive_symlinks_remain_readable() {
    let root = tempfile::tempdir().unwrap();
    let path = root.path().join("links.zip");
    let mut writer = zip::ZipWriter::new(fs::File::create(&path).unwrap());
    let options = zip::write::SimpleFileOptions::default();
    writer
        .add_symlink("node_modules/dep/missing", "not-installed", options)
        .unwrap();
    writer
        .add_symlink("node_modules/dep/indirect", "missing", options)
        .unwrap();
    writer.finish().unwrap();
    let cache = Cache::open(root.path().join("cache")).unwrap();
    let lease = cache.materialize(&path).unwrap();
    for (name, target) in [("missing", "not-installed"), ("indirect", "missing")] {
        let link = lease.content.join("node_modules/dep").join(name);
        assert!(fs::symlink_metadata(&link)
            .unwrap()
            .file_type()
            .is_symlink());
        assert_eq!(fs::read_link(&link).unwrap(), Path::new(target));
        assert_eq!(
            fs::read(&link).unwrap_err().kind(),
            std::io::ErrorKind::NotFound
        );
    }
    assert!(cache.materialize(&path).is_ok());
}

#[cfg(any(target_os = "macos", target_os = "linux"))]
#[test]
fn pnp_unaware_native_process_reads_virtual_dependencies() {
    use std::process::Command;
    let root = fixture();
    let cache = tempfile::tempdir().unwrap();
    let executable = root.path().join("fixture");
    assert!(Command::new("cc")
        .arg(concat!(env!("CARGO_MANIFEST_DIR"), "/tests/native.c"))
        .arg("-o")
        .arg(&executable)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--"])
        .arg(&executable)
        .args(["", "literal;$() argument"])
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "stdout={} stderr={}",
        String::from_utf8_lossy(&result.stdout),
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"protocol-output\nprotocol-output\n");
    let no_path = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .env_remove("PATH")
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--", "fixture"])
        .output()
        .unwrap();
    assert_eq!(no_path.status.code(), Some(127));
    assert_eq!(fs::read(root.path().join("output.txt")).unwrap(), b"native");
    assert!(!root.path().join("node_modules").exists());
    // PATH lookup must skip a regular, non-executable file with the same name.
    use std::os::unix::fs::PermissionsExt;
    let shadow = root.path().join("shadow");
    fs::create_dir(&shadow).unwrap();
    fs::write(shadow.join("fixture"), "not executable").unwrap();
    fs::set_permissions(shadow.join("fixture"), fs::Permissions::from_mode(0o644)).unwrap();
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .env(
            "PATH",
            std::env::join_paths([shadow.as_path(), root.path()]).unwrap(),
        )
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--", "fixture", "", "literal;$() argument"])
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"protocol-output\nprotocol-output\n");
}

#[cfg(target_os = "linux")]
#[test]
fn linux_rewrites_paths_from_a_guard_adjacent_stack() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("guard-stack.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>
extern long tiny_stack_open(const char *path, void *stack);
int main(void) {
    char *pages = mmap(0, 8192, PROT_READ | PROT_WRITE,
                       MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (pages == MAP_FAILED || mprotect(pages, 4096, PROT_NONE)) return 40;
    long fd = tiny_stack_open("node_modules/dep/file.txt", pages + 4096 + 128);
    if (fd < 0) return 41;
    char bytes[14] = {0};
    if (read(fd, bytes, 13) != 13 || strcmp(bytes, "package bytes")) return 42;
    close(fd);
    return 0;
}
"#,
    )
    .unwrap();
    let assembly = root.path().join("guard-stack.S");
    fs::write(
        &assembly,
        if cfg!(target_arch = "aarch64") {
            ".text\n.global tiny_stack_open\ntiny_stack_open:\nmov x9, sp\nmov sp, x1\nmov x1, \
             x0\nmov x0, #-100\nmov x2, #0\nmov x8, #56\nsvc #0\nmov sp, x9\nret\n"
        } else {
            ".text\n.global tiny_stack_open\ntiny_stack_open:\npush %r12\nmov %rsp, %r12\nmov \
             %rsi, %rsp\nmov %rdi, %rsi\nmov $-100, %rdi\nxor %rdx, %rdx\nxor %r10, %r10\nmov \
             $257, %rax\nsyscall\nmov %r12, %rsp\npop %r12\nret\n.section \
             .note.GNU-stack,\"\",@progbits\n"
        },
    )
    .unwrap();
    let executable = root.path().join("guard-stack");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .arg(&assembly)
        .status()
        .unwrap()
        .success());
    assert_eq!(
        Command::new(&executable)
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(41)
    );
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
}

#[cfg(target_os = "linux")]
#[test]
fn pnp_unaware_static_process_reads_virtual_dependencies() {
    use std::process::Command;
    let root = fixture();
    let cache = tempfile::tempdir().unwrap();
    let source = root.path().join("static.c");
    fs::write(&source, r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <dirent.h>
#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <sys/mman.h>
#include <sys/syscall.h>
#include <sys/inotify.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>
int main(int argc, char **argv) {
    struct stat info;
    if (stat("node_modules/dep/file.txt", &info) || info.st_size != 13) return 21;
    int fd = open("node_modules/dep/file.txt", O_RDONLY);
    if (fd < 0) return 22;
    char bytes[32] = {0};
    if (read(fd, bytes, 13) != 13 || strcmp(bytes, "package bytes")) return 23;
    void *mapped = mmap(NULL, 13, PROT_READ, MAP_PRIVATE, fd, 0);
    if (mapped == MAP_FAILED || memcmp(mapped, "package bytes", 13)) return 24;
    munmap(mapped, 13);
    char fd_path[64];
    snprintf(fd_path, sizeof(fd_path), "/proc/self/fd/%d", fd);
    errno = 0;
    if (open(fd_path, O_RDWR) != -1 || errno != EROFS) return 37;
    int reopened = open(fd_path, O_RDONLY);
    if (reopened < 0) return 38;
    errno = 0;
    if (fchmod(reopened, 0600) != -1 || errno != EROFS) return 39;
    close(reopened);
    errno = 0;
    if (futimens(fd, NULL) != -1 || errno != EROFS) return 58;
    close(fd);
    DIR *dir = opendir("node_modules/dep");
    if (!dir) return 25;
    fd = openat(dirfd(dir), "file.txt", O_RDONLY);
    if (fd < 0 || fstat(fd, &info) || info.st_size != 13) return 26;
    errno = 0;
    if (fchmod(fd, 0600) != -1 || errno != EROFS) return 34;
    errno = 0;
    if (syscall(452, dirfd(dir), "file.txt", 0600, 0) != -1 || errno != EROFS) return 56;
    errno = 0;
    if (syscall(452, fd, "", 0600, AT_EMPTY_PATH) != -1 || errno != EROFS) return 57;
    close(fd);
    snprintf(fd_path, sizeof(fd_path), "/proc/self/fd/%d/file.txt", dirfd(dir));
    errno = 0;
    if (open(fd_path, O_WRONLY) != -1 || errno != EROFS) return 54;
    fd = open(fd_path, O_RDONLY);
    if (fd < 0 || fchmod(fd, 0600) != -1 || errno != EROFS) return 55;
    close(fd);
    closedir(dir);
    if (lstat("node_modules/dep", &info) || !S_ISLNK(info.st_mode) || (info.st_mode & 07777) != 0777) return 27;
    if (fstatat(AT_FDCWD, "node_modules/dep", &info, AT_SYMLINK_NOFOLLOW) || (info.st_mode & 07777) != 0777) return 60;
    struct statx link_info;
    if (syscall(SYS_statx, AT_FDCWD, "node_modules/dep", AT_SYMLINK_NOFOLLOW, STATX_MODE, &link_info) ||
        (link_info.stx_mode & 07777) != 0777) return 61;
    errno = 0;
    if (open("node_modules/dep", O_RDONLY | O_NOFOLLOW) != -1 || errno != ELOOP) return 44;
    int link_fd = open("node_modules/dep", O_PATH | O_NOFOLLOW);
    if (link_fd < 0 || fstat(link_fd, &info) || !S_ISLNK(info.st_mode)) return 45;
    char target[4096], fd_target[4096];
    ssize_t target_len = readlink("node_modules/dep", target, sizeof(target));
    if (target_len <= 0) return 28;
    ssize_t fd_len = readlinkat(link_fd, "", fd_target, sizeof(fd_target));
    if (fd_len != target_len || memcmp(fd_target, target, target_len)) {
        fprintf(stderr, "empty readlinkat length=%zd path length=%zd errno=%d\n", fd_len, target_len, errno);
        return 62;
    }
    errno = 0;
    if (syscall(SYS_readlinkat, link_fd, "", (char *)1, 16) != -1 || errno != EFAULT) return 63;
    close(link_fd);
    errno = 0;
    if (syscall(SYS_readlinkat, AT_FDCWD, "node_modules/dep", (char *)1, 16) != -1 || errno != EFAULT) return 46;
    int ranged = open("node_modules/dep/file.txt", O_RDONLY);
    if (ranged < 0 || syscall(SYS_close_range, ranged, ranged, 0) != 0) return 47;
    int private_fd = memfd_create("pnport-range", 0);
    if (private_fd < 0 || fchmod(private_fd, 0600) != 0) return 48;
    close(private_fd);
    errno = 0;
    if (open("node_modules/dep/file.txt", O_WRONLY) != -1 || errno != EROFS) return 29;
    errno = 0;
    if (unlink("node_modules/dep/file.txt") != -1 || errno != EROFS) return 35;
    int watcher = inotify_init1(IN_CLOEXEC);
    if (watcher < 0 || inotify_add_watch(watcher, "node_modules/dep/file.txt", IN_MODIFY) < 0) return 36;
    close(watcher);
    fd = open("output.txt", O_WRONLY | O_CREAT, 0600);
    if (fd < 0 || write(fd, "native", 6) != 6) return 30;
    if (futimens(fd, NULL) != 0) return 59;
    close(fd);
    if (argc == 1) {
        if (mkdir("native-old", 0700) || symlink("native-old", "native-link")) return 49;
        link_fd = open("native-link", O_PATH | O_NOFOLLOW);
        if (link_fd < 0) return 64;
        ssize_t native_len = readlinkat(link_fd, "", target, sizeof(target));
        if (native_len != 10 || memcmp(target, "native-old", 10)) return 65;
        close(link_fd);
        fd = open("native-old/value", O_WRONLY | O_CREAT, 0600);
        if (fd < 0 || write(fd, "old", 3) != 3) return 50;
        close(fd);
        int native = open("native-link", O_RDONLY | O_DIRECTORY);
        if (native < 0 || rename("native-old", "native-moved") || mkdir("native-old", 0700)) return 51;
        fd = open("native-old/value", O_WRONLY | O_CREAT, 0600);
        if (fd < 0 || write(fd, "new", 3) != 3) return 52;
        close(fd);
        fd = openat(native, "value", O_RDONLY);
        char native_bytes[4] = {0};
        if (fd < 0 || read(fd, native_bytes, 3) != 3 || strcmp(native_bytes, "old")) return 53;
        close(fd);
        close(native);
        pid_t child = fork();
        if (child < 0) return 31;
        if (child == 0) {
            char *child_argv[] = {argv[0], "child", NULL};
            char *child_env[] = {NULL};
            execve(argv[0], child_argv, child_env);
            _exit(32);
        }
        int status = 0;
        if (waitpid(child, &status, 0) != child || !WIFEXITED(status) || WEXITSTATUS(status)) return 33;
    }
    puts("static-ok");
    return 0;
}

"#).unwrap();
    let executable = root.path().join("static-fixture");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    assert_eq!(
        Command::new(&executable)
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(21)
    );
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["--log-level", "debug", "run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "stdout={} stderr={}",
        String::from_utf8_lossy(&result.stdout),
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"static-ok\nstatic-ok\n");
    assert_eq!(fs::read(root.path().join("output.txt")).unwrap(), b"native");
    assert!(!root.path().join("node_modules").exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_getcwd_preserves_kernel_errors_for_virtual_directories() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("getcwd-fault.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(void) {
    if (chdir("node_modules/dep")) return 40;
    errno = 0;
    if (syscall(SYS_getcwd, (void *)1, 4096) != -1 || errno != EFAULT) return 41;
    errno = 0;
    char output[4096];
    if (syscall(SYS_getcwd, output, 0) != -1 || errno != ERANGE) return 42;
    if (syscall(SYS_getcwd, output, sizeof(output)) <= 0) return 43;
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("getcwd-fault");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_proc_cwd_aliases_preserve_virtual_dependency_ownership() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("proc-cwd.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
int main(void) {
    if (chdir("node_modules/dep")) return 40;
    errno = 0;
    if (chmod("/proc/self/cwd/file.txt", 0600) != -1 || errno != EROFS) return 41;
    char numeric[128];
    snprintf(numeric, sizeof(numeric), "/proc/%d/cwd/file.txt", getpid());
    errno = 0;
    if (open(numeric, O_WRONLY) != -1 || errno != EROFS) return 42;
    int fd = open("/proc/self/cwd/file.txt", O_RDONLY);
    if (fd < 0) return 43;
    char bytes[14] = {0};
    if (read(fd, bytes, 13) != 13 || strcmp(bytes, "package bytes")) return 44;
    close(fd);
    char cwd[4096] = {0};
    ssize_t length = readlink("/proc/self/cwd", cwd, sizeof(cwd) - 1);
    if (length <= 0 || !strstr(cwd, "/node_modules/dep")) return 45;
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("proc-cwd");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_chdir_through_descriptor_alias_updates_logical_cwd() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("proc-fd-chdir.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
int main(int argc, char **argv) {
    if (argc != 2) return 40;
    int native = open(".", O_RDONLY | O_DIRECTORY);
    int virtual = open("node_modules/dep", O_RDONLY | O_DIRECTORY);
    if (native < 0 || virtual < 0) return 41;
    char alias[128];
    snprintf(alias, sizeof(alias), "/proc/self/fd/%d", virtual);
    if (chdir(alias)) return 42;
    char cwd[4096];
    if (!getcwd(cwd, sizeof(cwd))) return 43;
    char expected[4096];
    snprintf(expected, sizeof(expected), "%s/cache.zip/node_modules/dep", argv[1]);
    if (strcmp(cwd, expected)) {
        fprintf(stderr, "cwd mismatch: got=%s expected=%s\n", cwd, expected);
        return 44;
    }
    int file = open("file.txt", O_RDONLY);
    if (file < 0) return 45;
    char bytes[14] = {0};
    if (read(file, bytes, 13) != 13 || strcmp(bytes, "package bytes")) return 46;
    snprintf(alias, sizeof(alias), "/proc/self/fd/%d", file);
    errno = 0;
    if (chdir(alias) != -1 || errno != ENOTDIR) return 47;
    if (!getcwd(cwd, sizeof(cwd)) || strcmp(cwd, expected)) return 48;
    snprintf(alias, sizeof(alias), "/dev/fd/%d", native);
    if (chdir(alias)) return 49;
    if (!getcwd(cwd, sizeof(cwd)) || strcmp(cwd, argv[1])) return 50;
    int output = open("output.txt", O_WRONLY | O_CREAT | O_TRUNC, 0600);
    if (output < 0 || write(output, "ok", 2) != 2) return 51;
    close(output);
    close(file);
    close(virtual);
    close(native);
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("proc-fd-chdir");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let direct = Command::new(&executable)
        .current_dir(root.path())
        .arg(root.path())
        .output()
        .unwrap();
    assert_eq!(direct.status.code(), Some(41));
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .arg(root.path())
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(fs::read(root.path().join("output.txt")).unwrap(), b"ok");
}

#[cfg(target_os = "linux")]
#[test]
fn linux_nonleader_proc_fd_aliases_keep_dependency_ownership() {
    use std::process::Command;
    let root = fixture();
    let cache = tempfile::tempdir().unwrap();
    let source = root.path().join("proc-alias.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/syscall.h>
#include <unistd.h>
static int check(const char *path) {
    errno = 0;
    int writable = open(path, O_WRONLY);
    if (writable >= 0) close(writable);
    if (writable != -1 || errno != EROFS) return 1;
    int readable = open(path, O_RDONLY);
    if (readable < 0) return 2;
    char bytes[14] = {0};
    int count = read(readable, bytes, 13);
    close(readable);
    return count == 13 && !strcmp(bytes, "package bytes") ? 0 : 3;
}
static void *worker(void *arg) {
    int fd = (int)(intptr_t)arg;
    char path[128];
    snprintf(path, sizeof(path), "/proc/%d/fd/%d/file.txt", getpid(), fd);
    if (check(path)) return (void *)(intptr_t)21;
    snprintf(path, sizeof(path), "/proc/%d/task/%ld/fd/%d/file.txt", getpid(), syscall(SYS_gettid), fd);
    if (check(path)) return (void *)(intptr_t)22;
    snprintf(path, sizeof(path), "/proc/self/task/%ld/fd/%d/file.txt", syscall(SYS_gettid), fd);
    if (check(path)) return (void *)(intptr_t)23;
    return 0;
}
int main(void) {
    int fd = open("node_modules/dep", O_RDONLY | O_DIRECTORY);
    if (fd < 0) return 20;
    pthread_t thread;
    if (pthread_create(&thread, 0, worker, (void *)(intptr_t)fd)) return 24;
    void *result;
    if (pthread_join(thread, &result)) return 25;
    close(fd);
    return (int)(intptr_t)result;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("proc-alias");
    assert!(Command::new("cc")
        .args(["-static", "-pthread", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(!root.path().join("node_modules").exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_reuses_scratch_after_threads_and_vfork_spawns() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("scratch-reuse.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <pthread.h>
#include <spawn.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>
extern char **environ;
static int probe(void) {
    int fd = open("node_modules/dep/file.txt", O_RDONLY);
    if (fd < 0) return 1;
    close(fd);
    return 0;
}
static size_t vm_size_kb(void) {
    FILE *status = fopen("/proc/self/status", "r");
    if (!status) return 0;
    char line[256];
    size_t size = 0;
    while (fgets(line, sizeof(line), status)) {
        if (sscanf(line, "VmSize: %zu kB", &size) == 1) break;
    }
    fclose(status);
    return size;
}
static void *worker(void *unused) {
    (void)unused;
    return (void *)(intptr_t)probe();
}
static int thread_once(void) {
    pthread_t thread;
    void *result = 0;
    if (pthread_create(&thread, 0, worker, 0) || pthread_join(thread, &result)) return 1;
    return (int)(intptr_t)result;
}
static int thread_wave(void) {
    pthread_t threads[32];
    for (int i = 0; i < 32; ++i) {
        if (pthread_create(&threads[i], 0, worker, 0)) return 1;
    }
    for (int i = 0; i < 32; ++i) {
        void *result = 0;
        if (pthread_join(threads[i], &result) || result) return 1;
    }
    return 0;
}
static int spawn_once(char *path) {
    pid_t child;
    char *args[] = {path, "child", 0};
    if (posix_spawn(&child, path, 0, 0, args, environ)) return 1;
    int status = 0;
    return waitpid(child, &status, 0) != child || !WIFEXITED(status) || WEXITSTATUS(status);
}
int main(int argc, char **argv) {
    if (argc > 1) return probe();
    if (probe()) return 20;
    if (thread_once()) return 21;
    size_t before = vm_size_kb();
    if (!before) return 22;
    for (int i = 0; i < 48; ++i) if (thread_once()) return 23;
    size_t after = vm_size_kb();
    if (after > before + 32768) {
        fprintf(stderr, "thread scratch VmSize grew from %zu to %zu kB\n", before, after);
        return 24;
    }
    if (spawn_once(argv[0])) return 25;
    before = vm_size_kb();
    if (!before) return 26;
    for (int i = 0; i < 48; ++i) if (spawn_once(argv[0])) return 27;
    after = vm_size_kb();
    if (after > before + 32768) {
        fprintf(stderr, "spawn scratch VmSize grew from %zu to %zu kB\n", before, after);
        return 28;
    }
    for (int i = 0; i < 4; ++i) if (thread_wave()) return 29;
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("scratch-reuse");
    assert!(Command::new("cc")
        .args(["-static", "-pthread", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    assert_ne!(
        Command::new(&executable)
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(0)
    );
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(!root.path().join("node_modules").exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_rejects_cross_group_shared_fd_tables_before_clone() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("shared-files.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <sched.h>
#include <signal.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>
int main(int argc, char **argv) {
    if (argc != 2) return 20;
    pid_t child = syscall(SYS_clone, CLONE_FILES | SIGCHLD, 0, 0, 0, 0);
    if (child < 0) return 21;
    if (child == 0) {
        int marker = open(argv[1], O_CREAT | O_WRONLY, 0600);
        if (marker >= 0) close(marker);
        _exit(marker >= 0 ? 0 : 22);
    }
    int status = 0;
    return waitpid(child, &status, 0) == child && WIFEXITED(status) && WEXITSTATUS(status) == 0 ? 0 : 23;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("shared-files");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let marker = root.path().join("cloned.txt");
    assert_eq!(
        Command::new(&executable)
            .arg(&marker)
            .status()
            .unwrap()
            .code(),
        Some(0)
    );
    fs::remove_file(&marker).unwrap();
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .arg(&marker)
        .output()
        .unwrap();
    assert_eq!(result.status.code(), Some(125));
    assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
    assert!(!marker.exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_rejects_cross_group_shared_cwd_before_clone() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("shared-cwd.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <sched.h>
#include <signal.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>
int main(int argc, char **argv) {
    if (argc != 2) return 20;
    pid_t child = syscall(SYS_clone, CLONE_FS | SIGCHLD, 0, 0, 0, 0);
    if (child < 0) return 21;
    if (child == 0) {
        int marker = open(argv[1], O_CREAT | O_WRONLY, 0600);
        if (marker >= 0) close(marker);
        _exit(marker >= 0 ? 0 : 22);
    }
    int status = 0;
    return waitpid(child, &status, 0) == child && WIFEXITED(status) && WEXITSTATUS(status) == 0 ? 0 : 23;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("shared-cwd");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let marker = root.path().join("cloned-cwd.txt");
    assert_eq!(
        Command::new(&executable)
            .arg(&marker)
            .status()
            .unwrap()
            .code(),
        Some(0)
    );
    fs::remove_file(&marker).unwrap();
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .arg(&marker)
        .output()
        .unwrap();
    assert_eq!(result.status.code(), Some(125));
    assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
    assert!(!marker.exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_rejects_threads_with_private_fd_or_cwd_contexts() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("private-thread-context.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <sched.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <unistd.h>
static _Atomic int done;
static int worker(void *value) {
    int marker = open((const char *)value, O_CREAT | O_WRONLY, 0600);
    if (marker >= 0) close(marker);
    atomic_store(&done, marker >= 0 ? 1 : -1);
    return marker >= 0 ? 0 : 22;
}
int main(int argc, char **argv) {
    if (argc != 3) return 20;
    void *stack = malloc(1024 * 1024);
    if (!stack) return 21;
    int shared = argv[2][0] == 'f' ? CLONE_FS : CLONE_FILES;
    int flags = CLONE_VM | CLONE_SIGHAND | CLONE_THREAD | shared;
    if (clone(worker, (char *)stack + 1024 * 1024, flags, argv[1]) < 0) return 23;
    for (int attempt = 0; attempt < 1000 && !atomic_load(&done); ++attempt) usleep(1000);
    return atomic_load(&done) == 1 ? 0 : 24;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("private-thread-context");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let marker = root.path().join("thread-marker");
    for mode in ["files", "cwd"] {
        assert_eq!(
            Command::new(&executable)
                .current_dir(root.path())
                .arg(&marker)
                .arg(mode)
                .status()
                .unwrap()
                .code(),
            Some(0),
            "native mode={mode}"
        );
        fs::remove_file(&marker).unwrap();
        let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
            .current_dir(root.path())
            .args(["run", "--"])
            .arg(&executable)
            .arg(&marker)
            .arg(mode)
            .output()
            .unwrap();
        assert_eq!(
            result.status.code(),
            Some(125),
            "mode={mode} stderr={}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
        assert!(!marker.exists());
    }
}

#[cfg(target_os = "linux")]
#[test]
fn linux_inherited_cache_descriptors_remain_readonly() {
    use std::{
        os::{
            fd::AsRawFd,
            unix::{fs::PermissionsExt, process::CommandExt},
        },
        process::Command,
    };
    let root = fixture();
    let cache_path = root.path().join("owned-cache");
    let cache = Cache::open(cache_path.clone()).unwrap();
    let lease = cache.materialize(&root.path().join("cache.zip")).unwrap();
    let content = lease.content.join("node_modules/dep/file.txt");
    let original_mode = fs::metadata(&content).unwrap().permissions().mode() & 0o777;
    let inherited = fs::File::open(&content).unwrap();
    let source = root.path().join("inherited-fd.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
int main(void) {
    if (fcntl(9, F_GETFD) < 0) return 40;
    errno = 0;
    if (fchmod(9, 0600) != -1 || errno != EROFS) return 41;
    errno = 0;
    if (fchown(9, getuid(), getgid()) != -1 || errno != EROFS) return 42;
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("inherited-fd");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let mut command = Command::new(env!("CARGO_BIN_EXE_pnport"));
    command
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(&cache_path)
        .args(["run", "--"])
        .arg(&executable);
    let source_fd = inherited.as_raw_fd();
    unsafe {
        command.pre_exec(move || {
            if libc::dup2(source_fd, 9) < 0 || libc::fcntl(9, libc::F_SETFD, 0) < 0 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let result = command.output().unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(
        fs::metadata(content).unwrap().permissions().mode() & 0o777,
        original_mode
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_proc_root_aliases_retain_managed_ownership() {
    use std::{os::unix::fs::PermissionsExt, process::Command};
    let root = fixture();
    let cache_path = root.path().join("owned-cache");
    let cache = Cache::open(cache_path.clone()).unwrap();
    let lease = cache.materialize(&root.path().join("cache.zip")).unwrap();
    let content = lease.content.join("node_modules/dep/file.txt");
    let original_mode = fs::metadata(&content).unwrap().permissions().mode() & 0o777;
    let output = root.path().join("output.txt");
    let source = root.path().join("proc-root.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(int argc, char **argv) {
    if (argc != 3) return 40;
    char alias[8192];
    snprintf(alias, sizeof(alias), "/proc/self/root%s", argv[1]);
    errno = 0;
    if (chmod(alias, 0600) != -1 || errno != EROFS) return 41;
    errno = 0;
    if (open(alias, O_WRONLY) != -1 || errno != EROFS) return 42;
    snprintf(alias, sizeof(alias), "/proc/%d/task/%ld/root%s", getpid(), syscall(SYS_gettid), argv[1]);
    errno = 0;
    if (chmod(alias, 0600) != -1 || errno != EROFS) return 43;
    snprintf(alias, sizeof(alias), "/proc/self/root%s", argv[2]);
    int fd = open(alias, O_WRONLY | O_CREAT | O_TRUNC, 0600);
    if (fd < 0 || write(fd, "ok", 2) != 2) return 44;
    close(fd);
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("proc-root");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(&cache_path)
        .args(["run", "--"])
        .arg(&executable)
        .arg(&content)
        .arg(&output)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(fs::read(output).unwrap(), b"ok");
    assert_eq!(
        fs::metadata(content).unwrap().permissions().mode() & 0o777,
        original_mode
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_rejects_pidfd_descriptor_duplication_before_installation() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("pidfd-getfd.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(void) {
    int dependency = open("node_modules/dep/file.txt", O_RDONLY);
    if (dependency < 0) return 40;
    int owner = syscall(SYS_pidfd_open, getpid(), 0);
    if (owner < 0) return 41;
    int copy = syscall(SYS_pidfd_getfd, owner, dependency, 0);
    return copy < 0 ? 42 : 43;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("pidfd-getfd");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    if result.status.code() == Some(42)
        && fs::read_to_string("/proc/self/status")
            .unwrap()
            .lines()
            .any(|line| line.trim() == "Seccomp:\t2")
    {
        // Docker's existing ERRNO filter takes precedence over our TRACE
        // action. The unconfined focused run exercises pnport's own denial.
        return;
    }
    assert_eq!(result.status.code(), Some(125));
    assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
}

#[cfg(target_os = "linux")]
#[test]
fn linux_rejects_ancillary_descriptor_receives_before_fd_mutation() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("received-fd.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <unistd.h>
int main(int argc, char **argv) {
    if (argc != 3) return 39;
    if (argv[2][0] == 'p') {
        int plain[2];
        if (socketpair(AF_UNIX, SOCK_DGRAM, 0, plain)) return 48;
        char byte = 'p';
        if (send(plain[0], &byte, 1, 0) != 1) return 49;
        char received_byte;
        struct iovec data = {.iov_base = &received_byte, .iov_len = 1};
        struct msghdr message = {.msg_iov = &data, .msg_iovlen = 1};
        if (recvmsg(plain[1], &message, 0) != 1 || received_byte != 'p') return 50;
        int marker = open(argv[1], O_CREAT | O_WRONLY, 0600);
        if (marker < 0) return 51;
        close(marker);
        return 0;
    }
    int fd = open("node_modules/dep/file.txt", O_RDONLY);
    if (fd < 0) return 40;
    int sockets[2];
    if (socketpair(AF_UNIX, SOCK_DGRAM, 0, sockets)) return 41;
    char byte = 'x';
    struct iovec sent_data = {.iov_base = &byte, .iov_len = 1};
    union { struct cmsghdr align; char bytes[CMSG_SPACE(sizeof(int))]; } sent_control = {0};
    struct msghdr sent = {.msg_iov = &sent_data, .msg_iovlen = 1,
        .msg_control = sent_control.bytes, .msg_controllen = sizeof(sent_control.bytes)};
    struct cmsghdr *control = CMSG_FIRSTHDR(&sent);
    control->cmsg_level = SOL_SOCKET;
    control->cmsg_type = SCM_RIGHTS;
    control->cmsg_len = CMSG_LEN(sizeof(int));
    memcpy(CMSG_DATA(control), &fd, sizeof(fd));
    if (sendmsg(sockets[0], &sent, 0) != 1) return 42;
    char received_byte;
    struct iovec received_data = {.iov_base = &received_byte, .iov_len = 1};
    union { struct cmsghdr align; char bytes[CMSG_SPACE(sizeof(int))]; } received_control = {0};
    struct msghdr received = {.msg_iov = &received_data, .msg_iovlen = 1,
        .msg_control = received_control.bytes, .msg_controllen = sizeof(received_control.bytes)};
    if (argv[2][0] == 'm') {
        struct mmsghdr batch = {.msg_hdr = received};
        if (recvmmsg(sockets[1], &batch, 1, 0, 0) != 1) return 43;
        received = batch.msg_hdr;
    } else if (recvmsg(sockets[1], &received, 0) != 1) {
        return 44;
    }
    control = CMSG_FIRSTHDR(&received);
    if (!control || control->cmsg_type != SCM_RIGHTS) return 45;
    int transferred;
    memcpy(&transferred, CMSG_DATA(control), sizeof(transferred));
    if (fchmod(transferred, 0600)) return 46;
    int marker = open(argv[1], O_CREAT | O_WRONLY, 0600);
    if (marker < 0) return 47;
    close(marker);
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("received-fd");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let marker = root.path().join("received-fd-marker");
    assert_eq!(
        Command::new(&executable)
            .arg(&marker)
            .arg("recvmsg")
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(40)
    );
    for mode in ["recvmsg", "mmsg"] {
        let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
            .current_dir(root.path())
            .args(["run", "--"])
            .arg(&executable)
            .arg(&marker)
            .arg(mode)
            .output()
            .unwrap();
        assert_eq!(
            result.status.code(),
            Some(125),
            "mode={mode} stderr={}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
        assert!(!marker.exists());
    }
    let plain = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .arg(&marker)
        .arg("plain")
        .output()
        .unwrap();
    assert_eq!(
        plain.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&plain.stderr)
    );
    assert!(marker.exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_native_relative_paths_keep_kernel_lookup_semantics() {
    use std::process::Command;
    let root = fixture();
    fs::create_dir_all(root.path().join("native/sub")).unwrap();
    fs::write(root.path().join("native/value"), b"right").unwrap();
    fs::write(root.path().join("value"), b"wrong").unwrap();
    std::os::unix::fs::symlink("native/sub", root.path().join("link")).unwrap();
    let source = root.path().join("native-paths.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
int main(void) {
    int fd = open("link/../value", O_RDONLY);
    if (fd < 0) return 40;
    char bytes[6] = {0};
    if (read(fd, bytes, 5) != 5 || strcmp(bytes, "right")) return 41;
    close(fd);
    errno = 0;
    if (open("native/value/", O_RDONLY) != -1 || errno != ENOTDIR) return 42;
    struct stat info;
    errno = 0;
    if (stat("native/value/", &info) != -1 || errno != ENOTDIR) return 43;
    if (rename("value", "link/../renamed")) return 44;
    if (access("native/renamed", F_OK) || access("renamed", F_OK) != -1) return 45;
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("native-paths");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(
        fs::read(root.path().join("native/renamed")).unwrap(),
        b"wrong"
    );
    assert!(!root.path().join("renamed").exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_native_cwd_uses_live_directory_after_symlink_and_rename() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("native-cwd.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
static int check(const char *directory, const char *expected) {
    char cwd[4096];
    if (!getcwd(cwd, sizeof(cwd))) return 1;
    if (strcmp(cwd, directory)) return 2;
    int fd = open("value", O_RDONLY);
    if (fd < 0) return 3;
    char bytes[4] = {0};
    int count = read(fd, bytes, 3);
    close(fd);
    return count == 3 && !strcmp(bytes, expected) ? 0 : 4;
}
int main(int argc, char **argv) {
    if (argc != 2) return 20;
    char old[4096], moved[4096];
    snprintf(old, sizeof(old), "%s/old", argv[1]);
    snprintf(moved, sizeof(moved), "%s/moved", argv[1]);
    if (mkdir("old", 0700) || mkdir("new", 0700) || symlink("old", "alias")) return 21;
    int fd = open("old/value", O_CREAT | O_WRONLY, 0600);
    if (fd < 0 || write(fd, "old", 3) != 3) return 22;
    close(fd);
    fd = open("new/value", O_CREAT | O_WRONLY, 0600);
    if (fd < 0 || write(fd, "new", 3) != 3) return 23;
    close(fd);
    if (chdir("alias") || unlink("../alias") || symlink("new", "../alias")) return 24;
    if (check(old, "old")) return 25;
    if (rename(old, moved)) return 26;
    if (check(moved, "old")) return 27;
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("native-cwd");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .arg(root.path())
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_empty_path_descriptor_forms_preserve_native_and_readonly_behavior() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("empty-path.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <sys/syscall.h>
#include <sys/stat.h>
#include <unistd.h>
int main(void) {
    int dependency = open("node_modules/dep/file.txt", O_RDONLY);
    if (dependency < 0) return 20;
    int output = open("output", O_CREAT | O_RDWR, 0600);
    if (output < 0) return 21;
    if (syscall(SYS_faccessat2, output, "", R_OK, AT_EMPTY_PATH)) return 22;
    if (fchownat(output, "", getuid(), getgid(), AT_EMPTY_PATH)) return 23;
    if (utimensat(output, "", 0, AT_EMPTY_PATH)) return 24;
    if (syscall(SYS_faccessat2, dependency, "", R_OK, AT_EMPTY_PATH)) return 25;
    errno = 0;
    if (syscall(SYS_faccessat2, dependency, "", W_OK, AT_EMPTY_PATH) != -1 || errno != EROFS) return 26;
    errno = 0;
    if (fchownat(dependency, "", getuid(), getgid(), AT_EMPTY_PATH) != -1 || errno != EROFS) return 27;
    errno = 0;
    if (utimensat(dependency, "", 0, AT_EMPTY_PATH) != -1 || errno != EROFS) return 28;
    errno = 0;
    if (linkat(dependency, "", AT_FDCWD, "linked-dependency", AT_EMPTY_PATH) != -1 || errno != EROFS) return 29;
    errno = 0;
    if (linkat(output, "", AT_FDCWD, "node_modules/dep/linked", AT_EMPTY_PATH) != -1 || errno != EROFS) return 30;
    close(output);
    close(dependency);
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("empty-path");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(!root.path().join("linked-dependency").exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_rejects_mutating_ioctls_on_dependency_descriptors() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("managed-ioctl.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/fs.h>
#include <sys/ioctl.h>
#include <unistd.h>
int main(void) {
    int fd = open("node_modules/dep/file.txt", O_RDONLY);
    if (fd < 0) return 40;
    unsigned long flags = 0;
    errno = 0;
    if (ioctl(fd, FS_IOC_GETFLAGS, &flags) < 0 && errno == EROFS) return 41;
    errno = 0;
    if (ioctl(fd, FS_IOC_SETFLAGS, &flags) != -1 || errno != EROFS) return 42;
    struct fsxattr attrs = {0};
    errno = 0;
    if (ioctl(fd, FS_IOC_FSSETXATTR, &attrs) != -1 || errno != EROFS) return 43;
    close(fd);
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("managed-ioctl");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_static_xattr_reads_translate_virtual_paths() {
    use std::process::Command;
    let root = fixture();
    let cache = tempfile::tempdir().unwrap();
    let source = root.path().join("xattr.c");
    fs::write(
        &source,
        r#"
#include <errno.h>
#include <sys/xattr.h>
int main(void) {
    const char *path = "node_modules/dep/file.txt";
    char values[256];
    errno = 0;
    if (getxattr(path, "user.pnport.missing", values, sizeof(values)) != -1 || errno != ENODATA) return 21;
    errno = 0;
    if (lgetxattr(path, "user.pnport.missing", values, sizeof(values)) != -1 || errno != ENODATA) return 22;
    if (listxattr(path, values, sizeof(values)) < 0) return 23;
    if (llistxattr(path, values, sizeof(values)) < 0) return 24;
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("xattr-fixture");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    assert_eq!(
        Command::new(&executable)
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(21)
    );
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(!root.path().join("node_modules").exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_static_filesystem_stats_translate_virtual_paths() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("statfs.c");
    fs::write(
        &source,
        r#"
#include <errno.h>
#include <sys/statfs.h>
#include <sys/statvfs.h>
int main(void) {
    const char *path = "node_modules/dep/file.txt";
    struct statfs native;
    if (statfs(path, &native)) return errno == ENOENT ? 21 : 22;
    struct statvfs portable;
    if (statvfs(path, &portable)) return 23;
    return native.f_bsize > 0 && portable.f_bsize > 0 ? 0 : 24;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("statfs-fixture");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    assert_eq!(
        Command::new(&executable)
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(21)
    );
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(!root.path().join("node_modules").exists());
}

#[cfg(all(target_os = "linux", target_arch = "x86_64"))]
#[test]
fn linux_x64_futimesat_rejects_virtual_dependency_mutation() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("futimesat.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <sys/time.h>
#include <unistd.h>
int main(void) {
    int dir = open("node_modules/dep", O_RDONLY | O_DIRECTORY);
    if (dir < 0) return errno == ENOENT ? 40 : 41;
    errno = 0;
    if (futimesat(dir, "file.txt", NULL) != -1 || errno != EROFS) return 42;
    int output = open("output.txt", O_CREAT | O_WRONLY, 0600);
    if (output < 0) return 43;
    close(output);
    if (futimesat(AT_FDCWD, "output.txt", NULL)) return 44;
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("futimesat");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    assert_eq!(
        Command::new(&executable)
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(40)
    );
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(root.path().join("output.txt").exists());
    assert!(!root.path().join("node_modules").exists());
}

#[cfg(target_os = "linux")]
#[test]
fn pnp_unaware_static_go_process_reads_virtual_dependencies() {
    use std::process::Command;
    if Command::new("go").arg("version").output().is_err() {
        eprintln!("Go is unavailable; the pinned Linux Docker gate runs this fixture");
        return;
    }
    let root = fixture();
    let cache = tempfile::tempdir().unwrap();
    let executable = root.path().join("static-go-fixture");
    assert!(Command::new("go")
        .env("CGO_ENABLED", "0")
        .env("GO111MODULE", "off")
        .args(["build", "-o"])
        .arg(&executable)
        .arg(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/tests/static-go/main.go"
        ))
        .status()
        .unwrap()
        .success());
    assert_eq!(
        Command::new(&executable)
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(21)
    );
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["--log-level", "debug", "run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "stdout={} stderr={}",
        String::from_utf8_lossy(&result.stdout),
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"static-go-ok\nstatic-go-ok\n");
    assert_eq!(fs::read(root.path().join("output-go.txt")).unwrap(), b"go");
    assert!(!root.path().join("node_modules").exists());
}

#[cfg(all(target_os = "linux", target_arch = "x86_64"))]
#[test]
fn linux_rejects_a_compat_elf_at_the_descendant_exec_stop() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("compat.s");
    fs::write(
        &source,
        ".section .text\n.globl _start\n_start:\nmovl $1, %eax\nxorl %ebx, %ebx\nint $0x80\n",
    )
    .unwrap();
    let object = root.path().join("compat.o");
    let compat = root.path().join("compat");
    assert!(Command::new("as")
        .args(["--32", "-o"])
        .arg(&object)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    assert!(Command::new("ld")
        .args(["-m", "elf_i386", "-o"])
        .arg(&compat)
        .arg(&object)
        .status()
        .unwrap()
        .success());
    // Hosts without IA32 compatibility cannot execute the negative control.
    if !Command::new(&compat)
        .status()
        .is_ok_and(|status| status.code() == Some(0))
    {
        return;
    }
    let launcher_source = root.path().join("compat-launcher.c");
    fs::write(
        &launcher_source,
        r#"
#include <unistd.h>
int main(int argc, char **argv) {
    if (argc != 2) return 40;
    char *args[] = {argv[1], 0};
    execve(argv[1], args, 0);
    return 41;
}
"#,
    )
    .unwrap();
    let launcher = root.path().join("compat-launcher");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&launcher)
        .arg(&launcher_source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&launcher)
        .arg(&compat)
        .output()
        .unwrap();
    assert_eq!(result.status.code(), Some(125));
    assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
}

#[cfg(target_os = "linux")]
#[test]
fn linux_descendant_execve_enters_a_virtual_executable() {
    use std::process::Command;
    let root = fixture();
    let cache = tempfile::tempdir().unwrap();
    let worker_source = root.path().join("worker.c");
    fs::write(
        &worker_source,
        r#"
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <sys/stat.h>
#include <unistd.h>
int main(void) {
    int fd = open("/proc/self/exe", O_RDONLY);
    if (fd < 0) return 30;
    errno = 0;
    if (fchmod(fd, 0600) != -1 || errno != EROFS) return 31;
    errno = 0;
    if (fchmodat(AT_FDCWD, "/proc/self/exe", 0600, 0) != -1 || errno != EROFS) return 32;
    close(fd);
    puts("virtual-exec-ok");
    return 0;
}
"#,
    )
    .unwrap();
    let worker = root.path().join("worker");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&worker)
        .arg(&worker_source)
        .status()
        .unwrap()
        .success());
    let archive_path = root.path().join("cache.zip");
    let mut archive = zip::ZipWriter::new(fs::File::create(&archive_path).unwrap());
    archive
        .start_file(
            "node_modules/dep/worker",
            zip::write::SimpleFileOptions::default().unix_permissions(0o755),
        )
        .unwrap();
    archive.write_all(&fs::read(&worker).unwrap()).unwrap();
    archive.finish().unwrap();
    let launcher_source = root.path().join("launcher.c");
    fs::write(
        &launcher_source,
        r#"
#include <unistd.h>
int main(void) {
    char *args[] = {"node_modules/dep/worker", 0};
    char *env[] = {0};
    execve(args[0], args, env);
    return 42;
}

"#,
    )
    .unwrap();
    let launcher = root.path().join("launcher");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&launcher)
        .arg(&launcher_source)
        .status()
        .unwrap()
        .success());
    assert_eq!(
        Command::new(&launcher)
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(42)
    );
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--"])
        .arg(&launcher)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"virtual-exec-ok\n");
    assert!(!root.path().join("node_modules").exists());

    let fd_launcher_source = root.path().join("fd-launcher.c");
    fs::write(
        &fd_launcher_source,
        r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(void) {
    int fd = open("node_modules/dep/worker", O_RDONLY);
    if (fd < 0) return 40;
    char *args[] = {"worker", 0};
    char *env[] = {0};
    syscall(SYS_execveat, fd, "", args, env, AT_EMPTY_PATH);
    return 41;
}
"#,
    )
    .unwrap();
    let fd_launcher = root.path().join("fd-launcher");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&fd_launcher)
        .arg(&fd_launcher_source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--"])
        .arg(&fd_launcher)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"virtual-exec-ok\n");
}

#[cfg(target_os = "linux")]
#[test]
fn linux_descendant_virtual_script_keeps_its_logical_argument() {
    use std::process::Command;
    let root = fixture();
    let cache = tempfile::tempdir().unwrap();
    let mut archive = zip::ZipWriter::new(fs::File::create(root.path().join("cache.zip")).unwrap());
    archive
        .start_file(
            "node_modules/dep/script",
            zip::write::SimpleFileOptions::default().unix_permissions(0o755),
        )
        .unwrap();
    archive
        .write_all(b"#!/usr/bin/env -S sh\nIFS= read -r value < \"${0%/*}/file.txt\"\nprintf '%s|%s|%s\\n' \"$0\" \"$value\" \"$1\"\n")
        .unwrap();
    archive
        .start_file(
            "node_modules/dep/file.txt",
            zip::write::SimpleFileOptions::default(),
        )
        .unwrap();
    archive.write_all(b"package bytes").unwrap();
    archive.finish().unwrap();
    let source = root.path().join("script-launcher.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(int argc, char **argv) {
    char *args[] = {"node_modules/dep/script", "extra", 0};
    char *env_with_path[] = {"PATH=/bin", 0};
    char *env_without_path[] = {0};
    if (argc > 1 && argv[1][0] == 'f') {
        int fd = open(args[0], O_RDONLY);
        if (fd < 0) return 43;
        syscall(SYS_execveat, fd, "", args, env_with_path, AT_EMPTY_PATH);
    } else {
        execve(args[0], args, argc > 1 ? env_without_path : env_with_path);
    }
    return 42;
}
"#,
    )
    .unwrap();
    let launcher = root.path().join("script-launcher");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&launcher)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    assert_eq!(
        Command::new(&launcher)
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(42)
    );
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--"])
        .arg(&launcher)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    let logical = root.path().join("cache.zip/node_modules/dep/script");
    assert_eq!(
        result.stdout,
        format!("{}|package bytes|extra\n", logical.display()).as_bytes()
    );
    let without_path = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--"])
        .arg(&launcher)
        .arg("without-path")
        .output()
        .unwrap();
    assert_eq!(
        without_path.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&without_path.stderr)
    );
    assert_eq!(
        without_path.stdout,
        format!("{}|package bytes|extra\n", logical.display()).as_bytes()
    );
    assert_eq!(
        Command::new(&launcher)
            .current_dir(root.path())
            .arg("fd")
            .status()
            .unwrap()
            .code(),
        Some(43)
    );
    let by_fd = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--"])
        .arg(&launcher)
        .arg("fd")
        .output()
        .unwrap();
    assert_eq!(
        by_fd.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&by_fd.stderr)
    );
    assert_eq!(
        by_fd.stdout,
        format!("{}|package bytes|extra\n", logical.display()).as_bytes()
    );
    assert!(!root.path().join("node_modules").exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_virtual_script_accepts_long_exec_vectors() {
    use std::process::Command;
    let root = fixture();
    let cache = tempfile::tempdir().unwrap();
    let mut archive = zip::ZipWriter::new(fs::File::create(root.path().join("cache.zip")).unwrap());
    archive
        .start_file(
            "node_modules/dep/script",
            zip::write::SimpleFileOptions::default().unix_permissions(0o755),
        )
        .unwrap();
    archive
        .write_all(b"#!/usr/bin/env -S sh\nprintf '%s|%s|%s\\n' \"$0\" \"$#\" \"$1\"\n")
        .unwrap();
    archive.finish().unwrap();
    let source = root.path().join("long-exec.c");
    fs::write(
        &source,
        r#"
#include <unistd.h>
int main(void) {
    char *args[602];
    args[0] = "node_modules/dep/script";
    for (int i = 1; i <= 600; ++i) args[i] = "arg";
    args[601] = 0;
    char *env[322];
    for (int i = 0; i < 320; ++i) env[i] = "D=1";
    env[320] = "PATH=/bin";
    env[321] = 0;
    execve(args[0], args, env);
    return 42;
}
"#,
    )
    .unwrap();
    let launcher = root.path().join("long-exec");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&launcher)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    assert_eq!(
        Command::new(&launcher)
            .current_dir(root.path())
            .status()
            .unwrap()
            .code(),
        Some(42)
    );
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--"])
        .arg(&launcher)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    let logical = root.path().join("cache.zip/node_modules/dep/script");
    assert_eq!(
        result.stdout,
        format!("{}|600|arg\n", logical.display()).as_bytes()
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_nonleader_thread_exec_reaps_the_owned_tree() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("thread-exec.c");
    fs::write(
        &source,
        r#"
#include <pthread.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
static void *replace(void *path) {
    char *args[] = {(char *)path, "after", 0};
    char *env[] = {0};
    execve((char *)path, args, env);
    _exit(42);
}
int main(int argc, char **argv) {
    if (argc > 1 && !strcmp(argv[1], "after")) {
        puts("thread-exec-ok");
        return 0;
    }
    pthread_t thread;
    if (pthread_create(&thread, 0, replace, argv[0])) return 41;
    pause();
    return 43;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("thread-exec");
    assert!(Command::new("cc")
        .args(["-static", "-pthread", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["--log-level", "debug", "run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"thread-exec-ok\n");
}

#[cfg(target_os = "linux")]
#[test]
fn linux_waits_for_workers_after_the_main_thread_exits() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("thread-exit.c");
    fs::write(
        &source,
        r#"
#include <pthread.h>
#include <stdio.h>
#include <unistd.h>
static void *worker(void *path) {
    usleep(150000);
    FILE *file = fopen((char *)path, "w");
    if (!file) _exit(42);
    fputs("worker-finished", file);
    fclose(file);
    return 0;
}
int main(int argc, char **argv) {
    if (argc != 2) return 40;
    pthread_t thread;
    if (pthread_create(&thread, 0, worker, argv[1])) return 41;
    pthread_exit(0);
}
"#,
    )
    .unwrap();
    let executable = root.path().join("thread-exit");
    assert!(Command::new("cc")
        .args(["-static", "-pthread", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let marker = root.path().join("worker.txt");
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .arg(&marker)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(fs::read(&marker).unwrap(), b"worker-finished");
}

#[cfg(target_os = "linux")]
#[test]
fn linux_preserves_a_child_requested_sigstop_until_sigcont() {
    use std::{
        process::Command,
        time::{Duration, Instant},
    };
    let root = fixture();
    let source = root.path().join("stop.c");
    fs::write(
        &source,
        r#"
#include <signal.h>
#include <stdio.h>
#include <unistd.h>
int main(int argc, char **argv) {
    if (argc != 2) return 40;
    FILE *file = fopen(argv[1], "w");
    if (!file) return 41;
    fprintf(file, "%d", getpid());
    fclose(file);
    raise(SIGSTOP);
    puts("resumed");
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("stop");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let marker = root.path().join("stopped.pid");
    let mut child = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .arg(&marker)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    let tracee: i32 = loop {
        if let Some(pid) = fs::read_to_string(&marker)
            .ok()
            .and_then(|value| value.parse().ok())
        {
            break pid;
        }
        assert!(Instant::now() < deadline, "tracee PID was not published");
        std::thread::sleep(Duration::from_millis(10));
    };
    let supervisor_stopped = loop {
        let state = fs::read_to_string(format!("/proc/{}/status", child.id()))
            .unwrap_or_default()
            .lines()
            .any(|line| line.starts_with("State:\tT"));
        if state || child.try_wait().unwrap().is_some() || Instant::now() >= deadline {
            break state;
        }
        std::thread::sleep(Duration::from_millis(10));
    };
    if !supervisor_stopped {
        unsafe { libc::kill(tracee, libc::SIGKILL) };
        child.kill().unwrap();
        child.wait().unwrap();
        panic!("pnport did not expose the child's job-control stop");
    }
    assert!(
        child.try_wait().unwrap().is_none(),
        "SIGSTOP was suppressed"
    );
    assert_eq!(unsafe { libc::kill(child.id() as i32, libc::SIGCONT) }, 0);
    assert_eq!(unsafe { libc::kill(tracee, libc::SIGCONT) }, 0);
    while child.try_wait().unwrap().is_none() && Instant::now() < deadline {
        std::thread::sleep(Duration::from_millis(10));
    }
    if child.try_wait().unwrap().is_none() {
        unsafe { libc::kill(tracee, libc::SIGKILL) };
        child.kill().unwrap();
        panic!("SIGCONT did not resume the traced child");
    }
    let output = child.wait_with_output().unwrap();
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, b"resumed\n");
}

#[cfg(target_os = "linux")]
#[test]
fn linux_gracefully_resumes_an_unsupported_syscall_stop() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("unsupported.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <fcntl.h>
#include <signal.h>
#include <sys/syscall.h>
#include <unistd.h>
static const char *marker;
static void terminated(int signal) {
    (void)signal;
    int preserved = fcntl(100, F_GETFD) >= 0;
    int fd = open(marker, O_WRONLY | O_CREAT, 0600);
    if (fd >= 0) {
        if (preserved) write(fd, "handled", 7);
        else write(fd, "mutated", 7);
        close(fd);
    }
    _exit(0);
}
int main(int argc, char **argv) {
    if (argc != 2) return 40;
    marker = argv[1];
    int source = open("/dev/null", O_RDONLY);
    if (source < 0 || dup2(source, 100) != 100) return 42;
    if (source != 100) close(source);
    signal(SIGTERM, terminated);
    syscall(SYS_close_range, 100, 100, 2);
    return 41;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("unsupported");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let marker = root.path().join("terminated.txt");
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .arg(&marker)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(125),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(
        fs::read(&marker)
            .unwrap_or_else(|error| panic!("{error}: {}", String::from_utf8_lossy(&result.stderr))),
        b"handled"
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_cleanup_cancels_a_failed_seccomp_entry_before_signals() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("failed-entry.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/openat2.h>
#include <signal.h>
#include <stdio.h>
#include <sys/syscall.h>
#include <unistd.h>
static void ignored(int signal) { (void)signal; }
int main(int argc, char **argv) {
    if (argc != 2) return 40;
    int marker = open(argv[1], O_WRONLY | O_CREAT | O_TRUNC, 0600);
    if (marker < 0 || write(marker, "ready", 5) != 5) return 43;
    signal(SIGTERM, ignored);
    struct open_how how = { .flags = O_RDONLY, .resolve = RESOLVE_BENEATH };
    int result = syscall(SYS_openat2, AT_FDCWD,
                         "node_modules/dep/file.txt", &how, sizeof(how));
    if (result == -1 && errno == ENOSYS) write(marker, "denied", 6);
    else {
        char report[40];
        int size = snprintf(report, sizeof(report), "native:%d:%d", result, errno);
        write(marker, report, size);
    }
    close(marker);
    return 44;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("failed-entry");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let marker = root.path().join("failed-entry-started");
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .arg(&marker)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(125),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(
        fs::read(&marker).unwrap(),
        b"readydenied",
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
}

#[cfg(target_os = "linux")]
#[test]
fn linux_reports_a_missing_elf_interpreter_as_command_not_found() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("missing-loader.c");
    fs::write(&source, "int main(void) { return 0; }\n").unwrap();
    let executable = root.path().join("missing-loader");
    assert!(Command::new("cc")
        .arg("-Wl,--dynamic-linker=/pnport-missing-loader.so")
        .arg("-o")
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(result.status.code(), Some(127));
    assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_COMMAND_NOT_FOUND"));
}

#[cfg(target_os = "linux")]
#[test]
fn linux_openat2_preserves_dirfd_resolution_constraints() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("openat2.c");
    fs::write(
        &source,
        r#"
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/openat2.h>
#include <stdio.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(void) {
    errno = 0;
    if (syscall(SYS_openat2, AT_FDCWD, "node_modules/dep/file.txt", NULL, 0) != -1 || errno != EINVAL) return 38;
    errno = 0;
    if (syscall(SYS_openat2, AT_FDCWD, "node_modules/dep/file.txt", NULL, 8) != -1 || errno != EINVAL) return 39;
    int dir = open("node_modules/dep", O_PATH | O_DIRECTORY);
    if (dir < 0) return 40;
    struct open_how how = {.flags = O_RDONLY, .resolve = RESOLVE_BENEATH};
    int fd = syscall(SYS_openat2, dir, "file.txt", &how, sizeof(how));
    if (fd < 0) return 41;
    char bytes[14] = {0};
    if (read(fd, bytes, 13) != 13) return 42;
    close(fd);
    errno = 0;
    if (syscall(SYS_openat2, dir, "../file.txt", &how, sizeof(how)) != -1 || errno != EXDEV) return 43;
    how.resolve = RESOLVE_IN_ROOT;
    fd = syscall(SYS_openat2, dir, "/file.txt", &how, sizeof(how));
    if (fd < 0) return 44;
    close(fd);
    puts("openat2-ok");
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("openat2");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["--log-level", "debug", "run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"openat2-ok\n");
    assert!(!root.path().join("node_modules").exists());
}

#[cfg(target_os = "linux")]
#[test]
fn linux_forwards_child_sigtrap() {
    use std::process::Command;
    let root = fixture();
    let source = root.path().join("trap.c");
    fs::write(
        &source,
        r#"
#include <signal.h>
#include <stdio.h>
#include <string.h>
static volatile sig_atomic_t handled = 0;
static void on_trap(int signal) { (void)signal; handled = 1; }
int main(int argc, char **argv) {
    if (argc == 1 || strcmp(argv[1], "default")) signal(SIGTRAP, on_trap);
    raise(SIGTRAP);
    if (!handled) return 51;
    puts("trap-handled");
    return 0;
}
"#,
    )
    .unwrap();
    let executable = root.path().join("trap");
    assert!(Command::new("cc")
        .args(["-static", "-o"])
        .arg(&executable)
        .arg(&source)
        .status()
        .unwrap()
        .success());
    let handled = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .output()
        .unwrap();
    assert_eq!(
        handled.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&handled.stderr)
    );
    assert_eq!(handled.stdout, b"trap-handled\n");
    let default = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["run", "--"])
        .arg(&executable)
        .arg("default")
        .output()
        .unwrap();
    assert_eq!(default.status.code(), Some(128 + libc::SIGTRAP));
}

#[cfg(target_os = "linux")]
#[test]
fn linux_private_helper_arguments_require_an_owner() {
    use std::{
        io::Read,
        process::{Command, Stdio},
        thread,
        time::{Duration, Instant},
    };

    for args in [
        vec!["__pnport_linux_probe"],
        vec!["__pnport_linux_launch", "/bin/true"],
    ] {
        let mut child = Command::new(env!("CARGO_BIN_EXE_pnport"))
            .args(args)
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let deadline = Instant::now() + Duration::from_secs(2);
        let status = loop {
            if let Some(status) = child.try_wait().unwrap() {
                break status;
            }
            if Instant::now() >= deadline {
                child.kill().unwrap();
                child.wait().unwrap();
                panic!("private helper command stopped without an owner");
            }
            thread::sleep(Duration::from_millis(10));
        };
        let mut stderr = String::new();
        child
            .stderr
            .take()
            .unwrap()
            .read_to_string(&mut stderr)
            .unwrap();
        assert_eq!(status.code(), Some(2), "{stderr}");
        assert!(stderr.contains("unrecognized subcommand"), "{stderr}");
    }
}

#[cfg(target_os = "linux")]
#[test]
fn linux_doctor_probes_a_mediated_pathname_syscall() {
    use std::process::Command;
    let root = fixture();
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["doctor", "--json"])
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    let report: Value = serde_json::from_slice(&result.stdout).unwrap();
    assert_eq!(report["ready"], true);
    let syscall = report["checks"]
        .as_array()
        .unwrap()
        .iter()
        .find(|entry| entry["id"] == "linux-syscall")
        .unwrap();
    assert_eq!(syscall["status"], "pass");
}

#[cfg(target_os = "linux")]
#[test]
fn linux_doctor_rejects_an_incompatible_companion_architecture() {
    use std::process::Command;
    let root = fixture();
    let native = Path::new(env!("CARGO_BIN_EXE_pnport"));
    let executable = root.path().join("pnport");
    fs::copy(native, &executable).unwrap();
    let companion = native.parent().unwrap().join("libpnport_preload.so");
    let mut bytes = fs::read(companion).unwrap();
    let other = if cfg!(target_arch = "aarch64") {
        62u16
    } else {
        183u16
    };
    bytes[18..20].copy_from_slice(&other.to_le_bytes());
    fs::write(root.path().join("libpnport_preload.so"), bytes).unwrap();
    let result = Command::new(executable)
        .current_dir(root.path())
        .args(["doctor", "--json"])
        .output()
        .unwrap();
    assert_eq!(result.status.code(), Some(125));
    let report: Value = serde_json::from_slice(&result.stdout).unwrap();
    let injection = report["checks"]
        .as_array()
        .unwrap()
        .iter()
        .find(|entry| entry["id"] == "injection")
        .unwrap();
    assert_eq!(injection["status"], "fail");
    assert_eq!(injection["code"], "PNPORT_INJECTION_FAILED");
}

#[cfg(target_os = "linux")]
#[test]
fn linux_cache_lock_wait_observes_signal_and_graph_change() {
    use std::{
        process::{Command, Stdio},
        thread,
        time::{Duration, Instant},
    };

    use fs2::FileExt;

    for mode in ["cancel", "graph"] {
        let root = fixture();
        let cache = tempfile::tempdir().unwrap();
        let source = root.path().join("wait-for-cache.c");
        fs::write(
            &source,
            r#"
#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>
int main(int argc, char **argv) {
    if (argc != 3) return 40;
    FILE *ready = fopen(argv[1], "w");
    if (!ready) return 41;
    fprintf(ready, "%d", getpid());
    fclose(ready);
    while (access(argv[2], F_OK) != 0) usleep(10000);
    int fd = open("node_modules/dep/file.txt", O_RDONLY);
    if (fd < 0) return 42;
    close(fd);
    return 0;
}
"#,
        )
        .unwrap();
        let executable = root.path().join("wait-for-cache");
        assert!(Command::new("cc")
            .args(["-static", "-o"])
            .arg(&executable)
            .arg(&source)
            .status()
            .unwrap()
            .success());
        let ready = root.path().join("ready.pid");
        let proceed = root.path().join("proceed");
        let cache_root = cache.path().join("cache");
        let mut process = Command::new(env!("CARGO_BIN_EXE_pnport"))
            .current_dir(root.path())
            .arg("--cache-dir")
            .arg(&cache_root)
            .args(["run", "--"])
            .arg(&executable)
            .arg(&ready)
            .arg(&proceed)
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let startup_deadline = Instant::now() + Duration::from_secs(5);
        while fs::read_to_string(&ready)
            .ok()
            .and_then(|value| value.parse::<i32>().ok())
            .is_none()
            && Instant::now() < startup_deadline
        {
            thread::sleep(Duration::from_millis(10));
        }
        let child: i32 = fs::read_to_string(&ready)
            .unwrap_or_default()
            .parse()
            .unwrap_or_else(|_| panic!("{mode} child did not reach the cache gate"));
        let lock = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(cache_root.join(".lock"))
            .unwrap();
        lock.lock_exclusive().unwrap();
        fs::write(&proceed, b"go").unwrap();
        thread::sleep(Duration::from_millis(150));
        assert!(
            process.try_wait().unwrap().is_none(),
            "{mode} did not wait for the held cache lock"
        );
        let started = Instant::now();
        match mode {
            "cancel" => assert_eq!(unsafe { libc::kill(process.id() as i32, libc::SIGINT) }, 0),
            "graph" => fs::write(root.path().join(".pnp.cjs"), b"changed").unwrap(),
            _ => unreachable!(),
        }
        let deadline = Instant::now() + Duration::from_secs(3);
        while process.try_wait().unwrap().is_none() && Instant::now() < deadline {
            thread::sleep(Duration::from_millis(10));
        }
        if process.try_wait().unwrap().is_none() {
            process.kill().unwrap();
        }
        let output = process.wait_with_output().unwrap();
        drop(lock);
        assert!(
            started.elapsed() < Duration::from_secs(3),
            "{mode} left the tracer blocked on the cache lock"
        );
        assert_eq!(
            output.status.code(),
            Some(if mode == "cancel" { 130 } else { 125 }),
            "{mode}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        if mode == "graph" {
            assert!(
                String::from_utf8_lossy(&output.stderr).contains("PNPORT_GRAPH_CHANGED"),
                "{}",
                String::from_utf8_lossy(&output.stderr)
            );
        }
        assert_eq!(
            unsafe { libc::kill(child, 0) },
            -1,
            "{mode} left its child alive"
        );
    }
}

#[cfg(target_os = "linux")]
#[test]
fn linux_graph_conflict_and_injection_failures_reap_detached_descendants() {
    use std::{
        process::{Command, Stdio},
        thread,
        time::{Duration, Instant},
    };
    for mode in ["graph", "conflict", "failure", "cancel"] {
        let root = fixture();
        let cache = tempfile::tempdir().unwrap();
        let executable = root.path().join("tree-fixture");
        assert!(Command::new("cc")
            .arg(concat!(env!("CARGO_MANIFEST_DIR"), "/tests/linux-tree.c"))
            .arg("-o")
            .arg(&executable)
            .status()
            .unwrap()
            .success());
        let mut command = Command::new(env!("CARGO_BIN_EXE_pnport"));
        command
            .current_dir(root.path())
            .arg("--cache-dir")
            .arg(cache.path().join("cache"))
            .args(["run", "--"])
            .arg(&executable)
            .stdout(Stdio::null())
            .stderr(Stdio::piped());
        if mode == "failure" {
            command.arg("failure");
        }
        let mut process = command.spawn().unwrap();
        let marker = root.path().join("detached.pid");
        let deadline = Instant::now() + Duration::from_secs(5);
        while fs::read_to_string(&marker)
            .ok()
            .and_then(|value| value.trim().parse::<i32>().ok())
            .is_none()
            && Instant::now() < deadline
        {
            thread::sleep(Duration::from_millis(10));
        }
        assert!(marker.exists(), "{mode} did not start a descendant");
        let child: i32 = fs::read_to_string(marker).unwrap().trim().parse().unwrap();
        match mode {
            "graph" => fs::write(root.path().join(".pnp.cjs"), "changed").unwrap(),
            "conflict" => fs::create_dir(root.path().join("node_modules")).unwrap(),
            "failure" => {}
            "cancel" => {
                assert_eq!(unsafe { libc::kill(process.id() as i32, libc::SIGINT) }, 0);
            }
            _ => unreachable!(),
        }
        let deadline = Instant::now() + Duration::from_secs(7);
        while process.try_wait().unwrap().is_none() && Instant::now() < deadline {
            thread::sleep(Duration::from_millis(10));
        }
        if process.try_wait().unwrap().is_none() {
            process.kill().unwrap();
        }
        let output = process.wait_with_output().unwrap();
        assert_eq!(
            output.status.code(),
            Some(if mode == "cancel" { 130 } else { 125 }),
            "{mode}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        if mode != "cancel" {
            let code = if mode == "graph" {
                "PNPORT_GRAPH_CHANGED"
            } else if mode == "conflict" {
                "PNPORT_FILESYSTEM_CONFLICT"
            } else {
                "PNPORT_INJECTION_FAILED"
            };
            assert!(
                String::from_utf8_lossy(&output.stderr).contains(code),
                "{mode}: {}",
                String::from_utf8_lossy(&output.stderr)
            );
        }
        let deadline = Instant::now() + Duration::from_secs(2);
        while unsafe { libc::kill(child, 0) } == 0 && Instant::now() < deadline {
            thread::sleep(Duration::from_millis(10));
        }
        assert_eq!(
            unsafe { libc::kill(child, 0) },
            -1,
            "{mode} left a descendant alive"
        );
    }
}

#[test]
fn peer_instances_keep_logical_identity_while_sharing_package_bytes() {
    let root = fixture();
    let mut d = data();
    for index in [0, 1] {
        d["packageRegistryData"][index][1][0][1]["packageDependencies"] = json!([
            ["one", ["dep", "virtual:one"]],
            ["two", ["dep", "virtual:two"]]
        ]);
    }
    d["packageRegistryData"][2][1] = json!([
        ["npm:1",{"packageLocation":"./cache.zip/node_modules/dep/","packageDependencies":[],"linkType":"HARD"}],
        ["virtual:one",{"packageLocation":"./.yarn/__virtual__/dep-one/1/cache.zip/node_modules/dep/","packageDependencies":[["peer",["dep","npm:1"]]],"linkType":"HARD"}],
        ["virtual:two",{"packageLocation":"./.yarn/__virtual__/dep-two/1/cache.zip/node_modules/dep/","packageDependencies":[["peer",["@scope/pkg","npm:1"]]],"linkType":"HARD"}]
    ]);
    inline(root.path(), &d);
    let graph = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
    let root_path = fs::canonicalize(root.path()).unwrap();
    let cache = tempfile::tempdir().unwrap();
    let session = tempfile::tempdir().unwrap();
    let mut view = View::new(
        graph,
        Cache::open(cache.path().join("cache")).unwrap(),
        session.path().to_owned(),
    );
    let one = view
        .translate(&root_path.join("node_modules/one/file.txt"))
        .unwrap();
    let two = view
        .translate(&root_path.join("node_modules/two/file.txt"))
        .unwrap();
    assert_eq!(one.physical, two.physical);
    assert_ne!(one.logical, two.logical);
    let peer_one = view
        .translate(&root_path.join("node_modules/one/node_modules/peer/package.json"))
        .unwrap();
    let peer_two = view
        .translate(&root_path.join("node_modules/two/node_modules/peer/package.json"))
        .unwrap();
    assert!(matches!(
        pnp::fs::VPath::from(&peer_one.logical),
        Ok(pnp::fs::VPath::Zip(_))
    ));
    assert!(matches!(
        pnp::fs::VPath::from(&peer_two.logical),
        Ok(pnp::fs::VPath::Zip(_))
    ));
    assert_ne!(peer_one.physical, peer_two.physical);
    assert_eq!(fs::read(&peer_one.physical).unwrap(), br#"{"name":"dep"}"#);
    assert_eq!(
        fs::read(&peer_two.physical).unwrap(),
        br#"{"name":"@scope/pkg"}"#
    );
}
#[test]
fn doctor_json_is_typed_ansi_free_and_does_not_leak_invalid_data() {
    use std::process::Command;
    let root = tempfile::tempdir().unwrap();
    fs::write(
        root.path().join(".pnp.cjs"),
        "const RAW_RUNTIME_STATE = 'canary-secret';",
    )
    .unwrap();
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["--color=always", "--cache-dir"])
        .arg(root.path().join("cache"))
        .args(["doctor", "--json"])
        .output()
        .unwrap();
    assert_eq!(result.status.code(), Some(125));
    assert!(result.stderr.is_empty());
    assert!(!result.stdout.contains(&0x1b));
    let data: Value = serde_json::from_slice(&result.stdout).unwrap();
    assert_eq!(data["schemaVersion"], 1);
    assert_eq!(data["ready"], false);
    assert_eq!(data["checks"][0]["code"], "PNPORT_MANIFEST_INVALID");
    assert!(!String::from_utf8_lossy(&result.stdout).contains("canary-secret"));
}
#[test]
fn concurrent_materializers_publish_one_entry() {
    let root = fixture();
    let cache_root = tempfile::tempdir().unwrap();
    let cache = Cache::open(cache_root.path().join("cache")).unwrap();
    let handles: Vec<_> = (0..4)
        .map(|_| {
            let cache = cache.clone();
            let path = root.path().join("cache.zip");
            std::thread::spawn(move || cache.materialize(&path).unwrap())
        })
        .collect();
    let leases: Vec<_> = handles.into_iter().map(|t| t.join().unwrap()).collect();
    assert!(leases
        .iter()
        .all(|lease| lease.content == leases[0].content));
    let entries = cache.entries(Operation::Clean).unwrap();
    assert_eq!(entries.len(), 1);
    assert!(matches!(entries[0].state, State::Active));
}

#[test]
fn active_markers_retain_distinct_archive_versions() {
    let root = fixture();
    let root_path = fs::canonicalize(root.path()).unwrap();
    let cache_root = tempfile::tempdir().unwrap();
    let session = tempfile::tempdir().unwrap();
    let make_view = || {
        View::new(
            Graph::load(&root.path().join(".pnp.cjs")).unwrap(),
            Cache::open(cache_root.path().join("cache")).unwrap(),
            session.path().to_owned(),
        )
    };
    let mut first = make_view();
    let first_path = first
        .translate(&root_path.join("node_modules/dep/file.txt"))
        .unwrap()
        .physical;
    archive(
        &root.path().join("cache.zip"),
        &[("node_modules/dep/file.txt", b"updated bytes")],
    );
    let mut second = make_view();
    let second_path = second
        .translate(&root_path.join("node_modules/dep/file.txt"))
        .unwrap()
        .physical;
    assert_ne!(first_path, second_path);
    assert_eq!(fs::read(first_path).unwrap(), b"package bytes");
    assert_eq!(fs::read(second_path).unwrap(), b"updated bytes");
    let markers: Vec<Input> = fs::read_dir(session.path().join("active"))
        .unwrap()
        .map(|entry| serde_json::from_slice(&fs::read(entry.unwrap().path()).unwrap()).unwrap())
        .collect();
    assert_eq!(markers.len(), 2);
    assert_eq!(markers[0].path, markers[1].path);
    assert_ne!(markers[0].sha256, markers[1].sha256);
}

#[cfg(target_os = "macos")]
#[test]
fn missing_executables_and_interpreters_return_not_found() {
    use std::{os::unix::fs::PermissionsExt, process::Command};
    let root = fixture();
    let missing = root.path().join("missing-command-canary");
    let script = root.path().join("script");
    fs::write(&script, format!("#!{}\n", missing.display())).unwrap();
    fs::set_permissions(&script, fs::Permissions::from_mode(0o700)).unwrap();
    let invalid = root.path().join("invalid");
    fs::write(&invalid, "not executable").unwrap();
    fs::set_permissions(&invalid, fs::Permissions::from_mode(0o600)).unwrap();
    for (command, status, code) in [
        (
            Path::new("./missing-command-canary"),
            127,
            "PNPORT_COMMAND_NOT_FOUND",
        ),
        (missing.as_path(), 127, "PNPORT_COMMAND_NOT_FOUND"),
        (script.as_path(), 127, "PNPORT_COMMAND_NOT_FOUND"),
        (invalid.as_path(), 126, "PNPORT_COMMAND_NOT_EXECUTABLE"),
    ] {
        let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
            .current_dir(root.path())
            .arg("--cache-dir")
            .arg(root.path().join("private-cache"))
            .args(["run", "--"])
            .arg(command)
            .output()
            .unwrap();
        assert_eq!(
            result.status.code(),
            Some(status),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert!(result.stdout.is_empty());
        let stderr = String::from_utf8_lossy(&result.stderr);
        assert!(stderr.contains(code));
        assert!(!stderr.contains("missing-command-canary"));
    }
}

#[cfg(target_os = "macos")]
#[test]
fn descendant_exec_and_spawn_prepare_script_interpreters() {
    use std::{os::unix::fs::PermissionsExt, process::Command};

    let root = fixture();
    let interpreter_source = root.path().join("child-interpreter.c");
    let interpreter = root.path().join("child-interpreter");
    fs::write(
        &interpreter_source,
        r#"#include <stdio.h>
#include <string.h>
int main(int argc, char **argv) {
  FILE *file = fopen("node_modules/dep/file.txt", "r");
  if (!file) return 70;
  fclose(file);
  if (argc != 4 || strcmp(argv[1], "option") || !strstr(argv[2], "child-script") || strcmp(argv[3], "literal")) return 71;
  puts("descendant-script-ok");
  return 0;
}
"#,
    )
    .unwrap();
    assert!(Command::new("cc")
        .arg(&interpreter_source)
        .arg("-o")
        .arg(&interpreter)
        .status()
        .unwrap()
        .success());
    let script = root.path().join("child-script");
    fs::write(&script, format!("#!{} option\n", interpreter.display())).unwrap();
    fs::set_permissions(&script, fs::Permissions::from_mode(0o700)).unwrap();

    let launcher_source = root.path().join("child-launcher.c");
    let launcher = root.path().join("child-launcher");
    fs::write(
        &launcher_source,
        r#"#include <errno.h>
#include <spawn.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>
extern char **environ;
int main(int argc, char **argv) {
  if (argc != 3) return 2;
  char *args[] = {"child-script", "literal", 0};
  if (strcmp(argv[1], "execve") == 0) {
    execve(argv[2], args, environ);
    return 30;
  }
  if (strcmp(argv[1], "posix_spawn_chdir") == 0) {
    posix_spawn_file_actions_t actions;
    if (posix_spawn_file_actions_init(&actions) != 0) return 34;
    if (posix_spawn_file_actions_addchdir_np(&actions, ".") != 0) return 35;
    int result = posix_spawn(0, "child-script", &actions, 0, args, environ);
    posix_spawn_file_actions_destroy(&actions);
    return result == ENOTSUP ? 0 : (result ? result : 36);
  }
  pid_t pid;
  if (posix_spawn(&pid, argv[2], 0, 0, args, environ) != 0) return 31;
  int status;
  if (waitpid(pid, &status, 0) != pid) return 32;
  return WIFEXITED(status) ? WEXITSTATUS(status) : 33;
}
"#,
    )
    .unwrap();
    assert!(Command::new("cc")
        .arg(&launcher_source)
        .arg("-o")
        .arg(&launcher)
        .status()
        .unwrap()
        .success());

    for method in ["execve", "posix_spawn"] {
        let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
            .current_dir(root.path())
            .arg("--cache-dir")
            .arg(root.path().join("private-cache"))
            .args(["run", "--"])
            .arg(&launcher)
            .arg(method)
            .arg(&script)
            .output()
            .unwrap();
        assert!(
            result.status.success(),
            "{method}: {}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert_eq!(result.stdout, b"descendant-script-ok\n", "{method}");
    }
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(root.path().join("private-cache"))
        .args(["run", "--"])
        .arg(&launcher)
        .arg("posix_spawn_chdir")
        .arg(&script)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(125),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
}

#[cfg(target_os = "macos")]
#[test]
fn script_interpreters_preserve_logical_arguments_and_reject_protection() {
    use std::{os::unix::fs::PermissionsExt, process::Command};
    let root = fixture();
    let source = root.path().join("interpreter.c");
    fs::write(&source, r#"
#include <stdio.h>
#include <string.h>
int main(int argc, char **argv) {
    FILE *file = fopen("node_modules/dep/file.txt", "r");
    if (!file) return 70;
    fclose(file);
    if (argc != 5 || strcmp(argv[1], "two words") || strcmp(argv[3], "") || strcmp(argv[4], "literal;$()")) return 71;
    puts("script-ok");
    return 0;
}
"#).unwrap();
    let interpreter = root.path().join("interpreter");
    assert!(Command::new("cc")
        .arg(&source)
        .arg("-o")
        .arg(&interpreter)
        .status()
        .unwrap()
        .success());
    let script = root.path().join("script with spaces");
    fs::write(
        &script,
        "#!/usr/bin/env -S interpreter 'two words'\nunused\n",
    )
    .unwrap();
    fs::set_permissions(&script, fs::Permissions::from_mode(0o700)).unwrap();
    let run = || {
        Command::new(env!("CARGO_BIN_EXE_pnport"))
            .current_dir(root.path())
            .env("PATH", root.path())
            .args(["--cache-dir"])
            .arg(root.path().join("private-cache"))
            .args(["run", "--"])
            .arg(&script)
            .args(["", "literal;$()"])
            .output()
            .unwrap()
    };
    let result = run();
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"script-ok\n");
    assert!(Command::new("codesign")
        .args(["--force", "--sign", "-", "--options", "runtime"])
        .arg(&interpreter)
        .status()
        .unwrap()
        .success());
    let result = run();
    assert_eq!(result.status.code(), Some(125));
    assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
    let entitlements = root.path().join("entitlements.plist");
    fs::write(&entitlements, r#"<?xml version="1.0"?><plist version="1.0"><dict><key>com.apple.security.cs.allow-dyld-environment-variables</key><true/><key>com.apple.security.cs.disable-library-validation</key><true/></dict></plist>"#).unwrap();
    assert!(Command::new("codesign")
        .args([
            "--force",
            "--sign",
            "-",
            "--options",
            "runtime",
            "--entitlements"
        ])
        .arg(&entitlements)
        .arg(&interpreter)
        .status()
        .unwrap()
        .success());
    let result = run();
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    fs::write(&script, "#!/bin/sh\nexit 0\n").unwrap();
    assert_eq!(run().status.code(), Some(125));
}

#[test]
fn unplugged_installation_containers_are_not_dependency_conflicts() {
    let root = fixture();
    let location = "./.yarn/unplugged/dep/node_modules/dep/";
    let mut value = data();
    value["packageRegistryData"][2][1][0][1]["packageLocation"] = json!(location);
    fs::create_dir_all(root.path().join(location)).unwrap();
    fs::write(
        root.path().join(location).join("package.json"),
        r#"{"name":"dep"}"#,
    )
    .unwrap();
    inline(root.path(), &value);
    let graph = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
    graph.check_conflicts().unwrap();
    let canonical = fs::canonicalize(root.path()).unwrap();
    let mut view = View::new(
        graph,
        Cache::open(root.path().join("private-cache")).unwrap(),
        root.path().join("session"),
    );
    let ancestor = canonical.join(".yarn/unplugged/dep/node_modules");
    assert_eq!(view.translate(&ancestor).unwrap().physical, ancestor);
    let file = view
        .translate(&canonical.join("node_modules/dep/package.json"))
        .unwrap();
    assert!(file.readonly);
    assert!(file.physical.is_file());
    #[cfg(target_os = "linux")]
    {
        let result = std::process::Command::new(env!("CARGO_BIN_EXE_pnport"))
            .current_dir(root.path())
            .args(["run", "--", "/bin/cat"])
            .arg(canonical.join("node_modules/dep/package.json"))
            .output()
            .unwrap();
        assert_eq!(
            result.status.code(),
            Some(0),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert_eq!(result.stdout, b"{\"name\":\"dep\"}");
    }
}
