// SPDX-License-Identifier: Apache-2.0
#define _GNU_SOURCE
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/wait.h>
#include <unistd.h>
#include <utime.h>
#ifdef __APPLE__
#include <sys/xattr.h>
#endif
#ifdef __linux__
#include <sys/inotify.h>
#endif

#define CHECK(expr) do { if (!(expr)) { fprintf(stderr, "cache probe line=%d errno=%d\n", __LINE__, errno); return 1; } } while (0)
static int make(const char *path) { return mkdir(path, 0700) == 0 || errno == EEXIST; }
static int listings(DIR *dir) {
    const char *names[] = {".vite", ".new-tool", ".cache-file", "dep", "one", "two", "@scope", "workspace", "unplugged"};
    int counts[9] = {0};
    struct dirent *entry;
    long cookie = -1;
    char next[256] = {0};
    while ((entry = readdir(dir))) {
        if (cookie != -1 && !next[0]) snprintf(next, sizeof(next), "%s", entry->d_name);
        if (!strcmp(entry->d_name, "dep")) cookie = telldir(dir);
        for (int i = 0; i < 9; i++) {
            if (!strcmp(entry->d_name, names[i])) {
                counts[i]++;
                if (i >= 3 && i != 6) CHECK(entry->d_type == DT_LNK);
                if (i == 6) CHECK(entry->d_type == DT_DIR);
            }
        }
    }
    for (int i = 0; i < 9; i++) CHECK(counts[i] == 1);
    CHECK(cookie != -1 && next[0]);
    seekdir(dir, cookie);
    entry = readdir(dir);
    CHECK(entry && !strcmp(entry->d_name, next));
    return 0;
}
static int selected(const struct dirent *entry) {
    // Exercise callbacks without the interception/runtime lock.
    struct stat metadata;
    if (stat("node_modules/dep/file.txt", &metadata)) return 0;
    return entry->d_name[0] != '.' || !strcmp(entry->d_name, ".vite");
}
#ifdef __APPLE__
// The supported Darwin ABI exports the 64-bit syscall entry point directly;
// the public getdirentries declaration intentionally refuses 64-bit dirents.
extern ssize_t __getdirentries64(int, void *, size_t, off_t *);
extern int legacy_getdirentries(int, void *, int, long *) __asm("_getdirentries");
struct legacy_dirent {
    uint32_t ino;
    uint16_t reclen;
    uint8_t type;
    uint8_t namlen;
    char name[256];
};
static int buffer_listings(int fd, int legacy) {
    const char *names[] = {".vite", ".new-tool", ".cache-file", "dep", "one", "two", "@scope", "workspace", "unplugged"};
    int counts[9] = {0};
    // Reserve large buffers too, exercising Darwin's out-of-band EOF flag.
    union { struct dirent align; char bytes[4096]; } buffer;
    off_t base = -1;
    long legacy_base = -1;
    ssize_t size;
    CHECK(lseek(fd, 0, SEEK_SET) == 0);
    for (;;) {
        size = legacy ? legacy_getdirentries(fd, buffer.bytes, sizeof(buffer.bytes), &legacy_base)
                      : __getdirentries64(fd, buffer.bytes, sizeof(buffer.bytes), &base);
        CHECK(size >= 0);
        if (!size) break;
        for (ssize_t offset = 0; offset < size;) {
            struct dirent *entry = (struct dirent *)(buffer.bytes + offset);
            struct legacy_dirent *old = (struct legacy_dirent *)(buffer.bytes + offset);
            const char *name = legacy ? old->name : entry->d_name;
            unsigned short length = legacy ? old->reclen : entry->d_reclen;
            unsigned char type = legacy ? old->type : entry->d_type;
            CHECK(length && offset + length <= size);
            for (int i = 0; i < 9; i++) if (!strcmp(name, names[i])) {
                counts[i]++;
                if (i >= 3) CHECK(type == (i == 6 ? DT_DIR : DT_LNK));
            }
            offset += length;
        }
        if (!legacy) {
            unsigned flags;
            memcpy(&flags, buffer.bytes + sizeof(buffer.bytes) - sizeof(flags), sizeof(flags));
            // A caller may stop immediately when the native EOF bit is set.
            if (flags & 1) break;
        }
    }
    for (int i = 0; i < 9; i++) CHECK(counts[i] == 1);
    size = legacy ? legacy_getdirentries(fd, buffer.bytes, sizeof(buffer.bytes), &legacy_base)
                  : __getdirentries64(fd, buffer.bytes, sizeof(buffer.bytes), &base);
    CHECK(size == 0);
    if (!legacy) {
        errno = 0; CHECK(__getdirentries64(fd, buffer.bytes, 0, &base) == -1 && errno == EINVAL);
    }
    return 0;
}
static int buffer_cookies(int fd) {
    union { struct dirent align; char bytes[64]; } buffer;
    off_t base = -1;
    ssize_t size;
    CHECK(lseek(fd, 0, SEEK_SET) == 0);
    // Small buffers split the native and appended records across calls.
    do { size = __getdirentries64(fd, buffer.bytes, sizeof(buffer.bytes), &base); CHECK(size > 0); }
    while (strcmp(((struct dirent *)buffer.bytes)->d_name, "dep"));
    off_t cookie = lseek(fd, 0, SEEK_CUR); CHECK(cookie > 0);
    int copy = dup(fd); CHECK(copy >= 0);
    size = __getdirentries64(copy, buffer.bytes, sizeof(buffer.bytes), &base); CHECK(size > 0);
    char next[256]; snprintf(next, sizeof(next), "%s", ((struct dirent *)buffer.bytes)->d_name);
    CHECK(lseek(copy, cookie, SEEK_SET) == cookie);
    CHECK(__getdirentries64(fd, buffer.bytes, sizeof(buffer.bytes), &base) == size);
    CHECK(!strcmp(((struct dirent *)buffer.bytes)->d_name, next));
    CHECK(lseek(copy, base, SEEK_SET) == base);
    CHECK(__getdirentries64(copy, buffer.bytes, sizeof(buffer.bytes), &base) == size);
    CHECK(!strcmp(((struct dirent *)buffer.bytes)->d_name, next));
    // Failed copyout must leave the overlay position available for retry.
    CHECK(lseek(copy, cookie, SEEK_SET) == cookie);
    errno = 0; CHECK(__getdirentries64(copy, (void *)1, sizeof(buffer.bytes), &base) == -1 && errno == EFAULT);
    CHECK(lseek(fd, 0, SEEK_CUR) == cookie);
    errno = 0; CHECK(__getdirentries64(copy, buffer.bytes, sizeof(buffer.bytes), NULL) == -1 && errno == EFAULT);
    CHECK(lseek(fd, 0, SEEK_CUR) == cookie);
    errno = 0; CHECK(__getdirentries64(copy, buffer.bytes, 1, &base) == -1 && errno == EINVAL);
    CHECK(lseek(fd, 0, SEEK_CUR) == cookie);
    pid_t child = fork(); CHECK(child >= 0);
    if (!child) _exit(__getdirentries64(copy, buffer.bytes, sizeof(buffer.bytes), &base) == size
                     && !strcmp(((struct dirent *)buffer.bytes)->d_name, next) ? 0 : 1);
    int status; CHECK(waitpid(child, &status, 0) == child && WIFEXITED(status) && WEXITSTATUS(status) == 0);
    CHECK(lseek(fd, 0, SEEK_CUR) > cookie);
    close(copy);
    return 0;
}
#endif
int main(int argc, char **argv) {
    if (argc > 1 && !strcmp(argv[1], "mkdir-alias")) {
        const char *paths[] = {"node_modules/.", "node_modules/./", "node_modules/././/",
                               "node_modules/@scope/..", "node_modules/@scope/../",
                               "node_modules/@scope/../@scope/..", "node_modules/dep/.."};
        int parent = open(".", O_RDONLY | O_DIRECTORY); CHECK(parent >= 0);
        for (size_t i = 0; i < sizeof(paths) / sizeof(paths[0]); i++) {
            errno = 0; CHECK(mkdir(paths[i], 0700) == -1 && errno == EEXIST);
            errno = 0; CHECK(mkdirat(parent, paths[i], 0700) == -1 && errno == EEXIST);
        }
        int root = open("node_modules", O_RDONLY | O_DIRECTORY); CHECK(root >= 0);
        errno = 0; CHECK(mkdirat(root, ".", 0700) == -1 && errno == EEXIST);
        errno = 0; CHECK(mkdirat(root, "@scope/..", 0700) == -1 && errno == EEXIST);
        int scope = open("node_modules/@scope", O_RDONLY | O_DIRECTORY); CHECK(scope >= 0);
        errno = 0; CHECK(mkdirat(scope, "..", 0700) == -1 && errno == EEXIST);
        CHECK(symlink("node_modules", "cache-root-link") == 0);
        errno = 0; CHECK(mkdir("cache-root-link/", 0700) == -1 && errno == EEXIST);
        CHECK(unlink("cache-root-link") == 0);
        close(scope); close(root); close(parent);
        puts("cache-mkdir-alias-ok"); return 0;
    }
    if (argc > 1 && !strcmp(argv[1], "conflict")) {
        int fd = open("entered", O_CREAT | O_WRONLY, 0600);
        CHECK(fd >= 0); close(fd);
        while (access("go", F_OK)) usleep(1000);
        struct stat metadata;
        (void)stat(argc > 2 ? argv[2] : "node_modules/dep/file.txt", &metadata);
        return 2;
    }
    // Exercise native prefix errors while the namespace remains valid. The
    // shared resolver tests later conflicts without racing supervisor shutdown.
    struct stat parent_metadata;
    errno = 0; CHECK(stat("missing/../node_modules/dep/file.txt", &parent_metadata) == -1 && errno == ENOENT);
    int prefix = open("file-prefix", O_CREAT | O_WRONLY, 0600); CHECK(prefix >= 0); close(prefix);
    errno = 0; CHECK(stat("file-prefix/../node_modules/dep/file.txt", &parent_metadata) == -1 && errno == ENOTDIR);
    CHECK(symlink("missing", "missing-parent-link") == 0);
    errno = 0; CHECK(stat("missing-parent-link/../node_modules/dep/file.txt", &parent_metadata) == -1 && errno == ENOENT);
    CHECK(unlink("missing-parent-link") == 0 && unlink("file-prefix") == 0);
    CHECK(make("node_modules/"));
    CHECK(make("node_modules/.vite"));
    CHECK(make("node_modules/.new-tool"));
    int root = open("node_modules", O_RDONLY | O_DIRECTORY);
    CHECK(root >= 0);
    int copy = dup(root); CHECK(copy >= 0);
    CHECK(mkdirat(copy, ".relative", 0700) == 0 || errno == EEXIST);
    int file = openat(copy, ".relative/results.json", O_CREAT | O_RDWR | O_TRUNC, 0600);
    CHECK(file >= 0 && write(file, "cache", 5) == 5 && fchmod(file, 0640) == 0);
    close(file);
    CHECK(rename("node_modules/.relative/results.json", "node_modules/.vite/results.json") == 0);
    CHECK(unlinkat(copy, ".relative", AT_REMOVEDIR) == 0);
    file = open("node_modules/.cache-file", O_CREAT | O_RDWR | O_TRUNC, 0600);
    CHECK(file >= 0 && write(file, "native", 6) == 6); close(file);
    file = openat(copy, "dep/file.txt", O_RDONLY);
    char bytes[32] = {0}; CHECK(file >= 0 && read(file, bytes, sizeof(bytes)) > 0);
    CHECK(!strcmp(bytes, "package bytes"));
    errno = 0; CHECK(fchmod(file, 0600) == -1 && errno == EROFS); close(file);
    errno = 0; CHECK(fchmod(copy, 0600) == -1 && errno == EROFS);
    errno = 0; CHECK(openat(copy, "dep/file.txt", O_WRONLY) == -1 && errno == EROFS);
    struct stat link_metadata;
    CHECK(fstatat(copy, "dep", &link_metadata, AT_SYMLINK_NOFOLLOW) == 0 && S_ISLNK(link_metadata.st_mode));
    CHECK(readlinkat(copy, "dep", bytes, sizeof(bytes)) > 0);
#ifdef __linux__
    int link_fd = openat(copy, "dep", O_PATH | O_NOFOLLOW);
    CHECK(link_fd >= 0 && fstat(link_fd, &link_metadata) == 0 && S_ISLNK(link_metadata.st_mode));
    CHECK(readlinkat(link_fd, "", bytes, sizeof(bytes)) > 0); close(link_fd);
    int watcher = inotify_init1(IN_CLOEXEC); CHECK(watcher >= 0);
    CHECK(inotify_add_watch(watcher, "node_modules/dep", IN_ATTRIB | IN_DONT_FOLLOW) >= 0);
    close(watcher);
#endif
    errno = 0; CHECK(mkdirat(copy, ".bin", 0700) == -1 && errno == EROFS);
    errno = 0; CHECK(rmdir("node_modules") == -1 && errno == EROFS);
    errno = 0; CHECK(rename("node_modules", "moved-modules") == -1 && errno == EROFS);
    int parent = open(".", O_RDONLY | O_DIRECTORY); CHECK(parent >= 0);
    errno = 0; CHECK(renameat(parent, "node_modules", parent, "moved-modules") == -1 && errno == EROFS);
    errno = 0; CHECK(renameat(parent, "node_modules/.vite", parent, "node_modules/dep") == -1 && errno == EROFS);
    errno = 0; CHECK(symlinkat("../node_modules/dep", copy, "dep") == -1 && errno == EROFS);
    errno = 0; CHECK(symlink("../node_modules/dep", "node_modules/dep") == -1 && errno == EROFS);
    errno = 0; CHECK(linkat(copy, "dep/file.txt", copy, ".hard-link", 0) == -1 && errno == EROFS);
    errno = 0; CHECK(link("node_modules/dep/file.txt", "node_modules/.hard-link") == -1 && errno == EROFS);
    errno = 0; CHECK(fchmodat(parent, "node_modules", 0700, 0) == -1 && errno == EROFS);
    errno = 0; CHECK(fchmodat(copy, "dep/file.txt", 0600, 0) == -1 && errno == EROFS);
    errno = 0; CHECK(fchownat(copy, "dep/file.txt", getuid(), getgid(), 0) == -1 && errno == EROFS);
    errno = 0; CHECK(utimensat(copy, "dep/file.txt", NULL, 0) == -1 && errno == EROFS);
    errno = 0; CHECK(utime("node_modules/dep/file.txt", NULL) == -1 && errno == EROFS);
    errno = 0; CHECK(utimes("node_modules", NULL) == -1 && errno == EROFS);
    errno = 0; CHECK(chown("node_modules", getuid(), getgid()) == -1 && errno == EROFS);
    errno = 0; CHECK(lchown("node_modules/dep", getuid(), getgid()) == -1 && errno == EROFS);
    errno = 0; CHECK(remove("node_modules") == -1 && errno == EROFS);
    errno = 0; CHECK(mkfifo("node_modules/dep", 0600) == -1 && errno == EROFS);
    errno = 0; CHECK(creat("node_modules/dep/file.txt", 0600) == -1 && errno == EROFS);
    CHECK(renameat(copy, ".vite/results.json", copy, ".new-tool/results.json") == 0);
    CHECK(renameat(copy, ".new-tool/results.json", copy, ".vite/results.json") == 0);
    CHECK(symlinkat(".vite/results.json", copy, ".relative-link") == 0);
    CHECK(fchmodat(copy, ".relative-link", 0640, 0) == 0);
    CHECK(fchownat(copy, ".relative-link", getuid(), getgid(), AT_SYMLINK_NOFOLLOW) == 0);
    CHECK(utimensat(copy, ".relative-link", NULL, AT_SYMLINK_NOFOLLOW) == 0);
    CHECK(linkat(copy, ".relative-link", copy, ".hard-link", AT_SYMLINK_FOLLOW) == 0);
    struct stat cache_metadata;
    CHECK(fstatat(copy, ".hard-link", &cache_metadata, 0) == 0 && S_ISREG(cache_metadata.st_mode));
    CHECK(unlinkat(copy, ".hard-link", 0) == 0);
    CHECK(link("node_modules/.relative-link", "node_modules/.hard-link") == 0);
    CHECK(lstat("node_modules/.hard-link", &cache_metadata) == 0);
#ifdef __APPLE__
    const char *attribute = "io.delino.pnport.cache-conformance";
    errno = 0; CHECK(setxattr("node_modules", attribute, "cache", 5, 0, 0) == -1 && errno == EROFS);
    errno = 0; CHECK(removexattr("node_modules", attribute, 0) == -1 && errno == EROFS);
    errno = 0; CHECK(fsetxattr(copy, attribute, "cache", 5, 0, 0) == -1 && errno == EROFS);
    errno = 0; CHECK(fremovexattr(copy, attribute, 0) == -1 && errno == EROFS);
    file = openat(copy, "dep/file.txt", O_RDONLY); CHECK(file >= 0);
    int dependency_copy = dup(file); CHECK(dependency_copy >= 0);
    struct stat before_timestamps, after_timestamps;
    CHECK(fstat(dependency_copy, &before_timestamps) == 0);
    errno = 0; CHECK(futimens(dependency_copy, NULL) == -1 && errno == EROFS);
    CHECK(fstat(dependency_copy, &after_timestamps) == 0);
    CHECK(before_timestamps.st_atimespec.tv_sec == after_timestamps.st_atimespec.tv_sec);
    CHECK(before_timestamps.st_atimespec.tv_nsec == after_timestamps.st_atimespec.tv_nsec);
    CHECK(before_timestamps.st_mtimespec.tv_sec == after_timestamps.st_mtimespec.tv_sec);
    CHECK(before_timestamps.st_mtimespec.tv_nsec == after_timestamps.st_mtimespec.tv_nsec);
    errno = 0; CHECK(futimens(copy, NULL) == -1 && errno == EROFS);
    errno = 0; CHECK(lchflags(".yarn/unplugged/cachedep/node_modules/cachedep/file.txt", 0) == -1 && errno == EROFS);
    errno = 0; CHECK(setxattr("node_modules/dep/file.txt", attribute, "cache", 5, 0, 0) == -1 && errno == EROFS);
    errno = 0; CHECK(removexattr("node_modules/dep/file.txt", attribute, 0) == -1 && errno == EROFS);
    errno = 0; CHECK(fsetxattr(dependency_copy, attribute, "cache", 5, 0, 0) == -1 && errno == EROFS);
    errno = 0; CHECK(fremovexattr(dependency_copy, attribute, 0) == -1 && errno == EROFS);
    close(dependency_copy); close(file);
    CHECK(setxattr("node_modules/.vite/results.json", attribute, "cache", 5, 0, XATTR_CREATE) == 0);
    char attribute_bytes[8] = {0};
    CHECK(getxattr("node_modules/.vite/results.json", attribute, attribute_bytes, sizeof(attribute_bytes), 0, 0) == 5);
    CHECK(!strcmp(attribute_bytes, "cache"));
    CHECK(removexattr("node_modules/.vite/results.json", attribute, 0) == 0);
    file = openat(copy, ".vite/results.json", O_RDONLY); CHECK(file >= 0);
    CHECK(futimens(file, NULL) == 0);
    CHECK(lchflags("node_modules/.vite/results.json", UF_NODUMP) == 0);
    CHECK(fstat(file, &after_timestamps) == 0 && (after_timestamps.st_flags & UF_NODUMP));
    CHECK(lchflags("node_modules/.vite/results.json", 0) == 0);
    CHECK(fsetxattr(file, attribute, "native", 6, 0, XATTR_CREATE) == 0);
    CHECK(fremovexattr(file, attribute, 0) == 0); close(file);
    errno = 0; CHECK(fsetxattr(-1, attribute, "cache", 5, 0, 0) == -1 && errno == EBADF);
    errno = 0; CHECK(fremovexattr(-1, attribute, 0) == -1 && errno == EBADF);
    errno = 0; CHECK(futimens(-1, NULL) == -1 && errno == EBADF);
    CHECK(S_ISREG(cache_metadata.st_mode));
#else
    CHECK(S_ISLNK(cache_metadata.st_mode));
#endif
    CHECK(unlinkat(copy, ".hard-link", 0) == 0);
    CHECK(unlinkat(copy, ".relative-link", 0) == 0);
    errno = 0; CHECK(renameat(-1, ".vite", copy, ".moved") == -1 && errno == EBADF);
    errno = 0; CHECK(symlinkat("target", -1, ".link") == -1 && errno == EBADF);
    errno = 0; CHECK(fchmodat(-1, ".vite", 0600, 0) == -1 && errno == EBADF);
#ifdef __APPLE__
    errno = 0; CHECK(renamex_np("node_modules", "moved-modules", 0) == -1 && errno == EROFS);
    errno = 0; CHECK(renameatx_np(parent, "node_modules", parent, "moved-modules", 0) == -1 && errno == EROFS);
    errno = 0; CHECK(renameatx_np(copy, ".vite", copy, "dep", RENAME_SWAP) == -1 && errno == EROFS);
    errno = 0; CHECK(chflags("node_modules", 0) == -1 && errno == EROFS);
    errno = 0; CHECK(lutimes("node_modules/dep", NULL) == -1 && errno == EROFS);
    CHECK(renamex_np("node_modules/.vite/results.json", "node_modules/.new-tool/results.json", RENAME_EXCL) == 0);
    CHECK(renameatx_np(copy, ".new-tool/results.json", copy, ".vite/results.json", RENAME_EXCL) == 0);
#endif
    close(parent);
    CHECK(symlink("../node_modules/dep", "node_modules/.cache-link") == 0 || errno == EEXIST);
    errno = 0; CHECK(open("node_modules/.cache-link/file.txt", O_WRONLY) == -1 && errno == EROFS);
    errno = 0; CHECK(linkat(copy, ".cache-link/file.txt", copy, ".hard-link", AT_SYMLINK_FOLLOW) == -1 && errno == EROFS);
#ifdef __APPLE__
    errno = 0; CHECK(link("node_modules/.cache-link/file.txt", "node_modules/.hard-link") == -1 && errno == EROFS);
    errno = 0; CHECK(setxattr("node_modules/.cache-link", attribute, "cache", 5, 0, 0) == -1 && errno == EROFS);
    errno = 0; CHECK(removexattr("node_modules/.cache-link", attribute, 0) == -1 && errno == EROFS);
    // No-follow applies to the caller-owned link, never its managed target.
    CHECK(lchflags("node_modules/.cache-link", 0) == 0);
    errno = 0; CHECK(removexattr("node_modules/.cache-link", attribute, XATTR_NOFOLLOW) == -1 && errno == ENOATTR);
#endif
    CHECK(unlink("node_modules/.cache-link") == 0);
    CHECK(argc > 1 && symlink(argv[1], "node_modules/.internal-cache") == 0);
    errno = 0; CHECK(open("node_modules/.internal-cache/forbidden", O_CREAT | O_WRONLY, 0600) == -1 && errno == EROFS);
    CHECK(unlink("node_modules/.internal-cache") == 0);
    file = openat(copy, "unplugged/file.txt", O_RDONLY);
    CHECK(file >= 0 && read(file, bytes, sizeof(bytes)) > 0);
    errno = 0; CHECK(fchmod(file, 0600) == -1 && errno == EROFS); close(file);
    CHECK(symlink("../node_modules/unplugged", "node_modules/.unplugged-link") == 0);
    errno = 0; CHECK(open("node_modules/.unplugged-link/file.txt", O_WRONLY) == -1 && errno == EROFS);
    CHECK(unlink("node_modules/.unplugged-link") == 0);
    CHECK(make("packages/app/node_modules"));
    CHECK(make("packages/app/node_modules/.workspace-cache"));
    file = open("packages/app/node_modules/.workspace-cache/results.json", O_CREAT | O_WRONLY, 0600);
    CHECK(file >= 0); close(file);
    file = open("node_modules/one/node_modules/peer/file.txt", O_RDONLY); CHECK(file >= 0); close(file);
    file = open("node_modules/two/node_modules/peer/package.json", O_RDONLY); CHECK(file >= 0); close(file);
    DIR *dir = opendir("node_modules"); CHECK(dir && listings(dir) == 0);
    rewinddir(dir); CHECK(listings(dir) == 0); CHECK(closedir(dir) == 0);
    dir = fdopendir(dup(root)); CHECK(dir && listings(dir) == 0); CHECK(closedir(dir) == 0);
    struct dirent **entries = NULL;
    int count = scandir("node_modules", &entries, selected, alphasort); CHECK(count == 7);
    for (int i = 0; i < count; i++) { free(entries[i]); }
    free(entries);
#ifdef __APPLE__
    __block int selections = 0, comparisons = 0;
    count = scandir_b("node_modules", &entries, ^int(const struct dirent *entry) {
        selections++; return selected(entry);
    }, ^int(const struct dirent **left, const struct dirent **right) {
        struct stat metadata;
        if (stat("node_modules/dep/file.txt", &metadata)) return 0;
        comparisons++; return strcmp((*left)->d_name, (*right)->d_name);
    });
    CHECK(count == 7 && selections >= 9 && comparisons > 0);
    for (int i = 0; i < count; i++) {
        if (i) CHECK(strcmp(entries[i - 1]->d_name, entries[i]->d_name) < 0);
    }
    for (int i = 0; i < count; i++) free(entries[i]);
    free(entries);
    count = scandir_b("node_modules/.vite", &entries, ^int(const struct dirent *entry) {
        return selected(entry);
    }, NULL);
    CHECK(count == 1 && !strcmp(entries[0]->d_name, "results.json"));
    free(entries[0]); free(entries);
    count = scandir_b("node_modules", &entries, NULL, NULL); CHECK(count >= 9);
    for (int i = 0; i < count; i++) free(entries[i]);
    free(entries);
    CHECK(buffer_listings(root, 0) == 0);
    CHECK(buffer_listings(root, 1) == 0);
    CHECK(buffer_cookies(root) == 0);
    errno = 0; CHECK(__getdirentries64(-1, bytes, sizeof(bytes), NULL) == -1 && errno == EBADF);
#endif
    CHECK(lseek(root, 0, SEEK_SET) == 0);
    pid_t child = fork(); CHECK(child >= 0);
    if (!child) { DIR *shared = fdopendir(copy); _exit(shared && listings(shared) == 0 ? 0 : 1); }
    int status; CHECK(waitpid(child, &status, 0) == child && WIFEXITED(status) && WEXITSTATUS(status) == 0);
    close(copy); close(root);
    CHECK(unlink("node_modules/.cache-file") == 0);
    puts("cache-conformance-ok");
    return 0;
}
