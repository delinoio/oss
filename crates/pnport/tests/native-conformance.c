// SPDX-License-Identifier: Apache-2.0
// Synthetic ZIP library, package executable, mmap and native watch acceptance.
#define _GNU_SOURCE
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/wait.h>
#include <unistd.h>
#ifdef __APPLE__
#include <sys/event.h>
#define LIBRARY_SUFFIX ".dylib"
#else
#include <sys/inotify.h>
#define LIBRARY_SUFFIX ".so"
#endif
#ifndef PNPORT_WATCH_ONLY
#include <dlfcn.h>
#endif

static int dependency(void) {
    int fd = open("node_modules/dep/file.txt", O_RDONLY | O_CLOEXEC);
    if (fd < 0) return 10;
    struct stat info;
    if (fstat(fd, &info) || info.st_size != 13) return 11;
    void *bytes = mmap(NULL, 13, PROT_READ, MAP_PRIVATE, fd, 0);
    if (bytes == MAP_FAILED || memcmp(bytes, "package bytes", 13)) return 12;
    if (munmap(bytes, 13) || close(fd)) return 13;
    errno = 0;
    fd = open("node_modules/dep/file.txt", O_WRONLY);
    if (fd >= 0 || errno != EROFS) return 14;
    DIR *directory = opendir("node_modules/dep");
    if (!directory) return 15;
    int found = 0;
    struct dirent *entry;
    while ((entry = readdir(directory)))
        if (!strcmp(entry->d_name, "file.txt")) found++;
    if (closedir(directory) || found != 1) return 16;
    return 0;
}

#ifdef PNPORT_LIBRARY
static int initialization;
__attribute__((constructor)) static void initialize_library(void) {
    initialization = dependency();
    if (initialization) return;
    // Constructor callbacks must retain interception before and after fork.
    pid_t child = fork();
    if (child < 0) { initialization = 17; return; }
    if (!child) _exit(dependency());
    int status;
    if (waitpid(child, &status, 0) != child || !WIFEXITED(status) || WEXITSTATUS(status)) {
        initialization = 18;
        return;
    }
#ifndef PNPORT_NESTED_LIBRARY
    void *nested = dlopen("node_modules/dep/libnested" LIBRARY_SUFFIX, RTLD_NOW | RTLD_LOCAL);
    if (!nested) { initialization = 19; return; }
    int (*probe)(void) = (int (*)(void))dlsym(nested, "pnport_library_probe");
    if (!probe || probe() != 42 || dlclose(nested)) initialization = 20;
#endif
}
int pnport_library_probe(void) {
    int result = dependency();
    return initialization ? initialization : result ? result : 42;
}
#else

static int descriptor_mutations(void) {
    int fd = open("node_modules/dep/file.txt", O_RDONLY | O_CLOEXEC);
    if (fd < 0) return 70;
    int duplicate = fcntl(fd, F_DUPFD_CLOEXEC, 64);
    if (duplicate < 0) return 71;
    struct timeval times[2] = {{0, 0}, {0, 0}};
#ifdef __APPLE__
    int extent_output = open("output/extents.txt", O_RDWR | O_CREAT | O_EXCL, 0600);
    if (extent_output < 0) return 84;
#endif
    for (int index = 0; index < 2; index++) {
        int dependency_fd = index ? duplicate : fd;
        errno = 0;
        if (fchmod(dependency_fd, 0600) != -1 || errno != EROFS) return 72;
        errno = 0;
        if (fchown(dependency_fd, getuid(), getgid()) != -1 || errno != EROFS) return 73;
        errno = 0;
        if (futimes(dependency_fd, times) != -1 || errno != EROFS) return 74;
        errno = 0;
        if (ftruncate(dependency_fd, 0) != -1 || errno != EROFS) return 75;
#ifdef __APPLE__
        errno = 0;
        if (fchflags(dependency_fd, 0) != -1 || errno != EROFS) return 76;
        fstore_t allocation = {.fst_flags = F_ALLOCATECONTIG, .fst_posmode = F_PEOFPOSMODE,
                              .fst_offset = 0, .fst_length = 4096};
        errno = 0;
        if (fcntl(dependency_fd, F_PREALLOCATE, &allocation) != -1 || errno != EROFS) return 83;
        // F_TRANSFEREXTENTS has two integer descriptors. Either managed
        // operand is read-only; invalid operands retain native errno ordering.
        errno = 0;
        if (fcntl(dependency_fd, F_TRANSFEREXTENTS, extent_output) != -1 || errno != EROFS) return 85;
        errno = 0;
        if (fcntl(extent_output, F_TRANSFEREXTENTS, dependency_fd) != -1 || errno != EROFS) return 86;
        errno = 0;
        if (fcntl(dependency_fd, F_TRANSFEREXTENTS, -1) != -1 || errno != EINVAL) return 87;
        errno = 0;
        if (fcntl(-1, F_TRANSFEREXTENTS, dependency_fd) != -1 || errno != EBADF) return 88;
#endif
    }
#ifdef __APPLE__
    if (close(extent_output)) return 89;
#endif
    if (close(fd) || close(duplicate)) return 77;
    // Reuse a tracked descriptor number for a normal output file. Clearing
    // dependency provenance must preserve ordinary output mutations.
    int output = open("output/mutations.txt", O_RDWR | O_CREAT | O_EXCL, 0600);
    if (output < 0 || dup2(output, duplicate) != duplicate) return 78;
    if (fchmod(duplicate, 0640) || fchown(duplicate, getuid(), getgid()) ||
        futimes(duplicate, times) || ftruncate(duplicate, 7)) return 79;
#ifdef __APPLE__
    if (fchflags(duplicate, 0)) return 80;
#endif
    if (close(output) || close(duplicate)) return 81;
    errno = 0;
    if (fchmod(duplicate, 0600) != -1 || errno != EBADF) return 82;
    return dependency();
}

static int native_aliases(void) {
    if (symlink("node_modules/dep", "logical-alias")) return 90;
    int fd = open("logical-alias/file.txt", O_RDONLY | O_CLOEXEC);
    char bytes[13];
    if (fd < 0 || read(fd, bytes, 13) != 13 || memcmp(bytes, "package bytes", 13)) return 91;
    errno = 0;
    if (chmod("logical-alias/file.txt", 0600) != -1 || errno != EROFS) return 92;
    errno = 0;
    if (truncate("logical-alias/file.txt", 0) != -1 || errno != EROFS) return 93;
    errno = 0;
    if (open("logical-alias/file.txt", O_RDWR) != -1 || errno != EROFS) return 94;
    struct stat info;
    if (lstat("logical-alias", &info) || !S_ISLNK(info.st_mode) ||
        stat("logical-alias", &info) || !S_ISDIR(info.st_mode)) return 95;
    char link[64];
    if (readlink("logical-alias", link, sizeof(link)) != 16 ||
        memcmp(link, "node_modules/dep", 16)) return 96;
    errno = 0;
    if (open("logical-alias", O_RDONLY | O_NOFOLLOW) != -1 || errno != ELOOP) return 97;
#ifdef __APPLE__
    char backing[4096];
    if (fcntl(fd, F_GETPATH, backing) || symlink(backing, "backing-alias")) return 98;
    errno = 0;
    if (chmod("backing-alias", 0600) != -1 || errno != EROFS) return 99;
    errno = 0;
    if (truncate("backing-alias", 0) != -1 || errno != EROFS) return 100;
    // Removing the caller-owned link must not mutate or remove its target.
    if (unlink("backing-alias")) return 101;
#endif
    if (close(fd) || rename("logical-alias", "moved-alias")) return 102;
    fd = open("moved-alias/file.txt", O_RDONLY | O_CLOEXEC);
    if (fd < 0 || read(fd, bytes, 13) != 13 || memcmp(bytes, "package bytes", 13) || close(fd)) return 103;
    if (unlink("moved-alias") || mkdir("output/inside", 0700) ||
        symlink("output/inside", "native-alias")) return 104;
    fd = open("native-alias/../native.txt", O_WRONLY | O_CREAT | O_EXCL, 0600);
    if (fd < 0 || write(fd, "native", 6) != 6 || close(fd) || unlink("native-alias")) return 105;
    errno = 0;
    if (open("source.txt/", O_RDONLY) != -1 || errno != ENOTDIR) return 106;
    errno = 0;
    if (open("missing-native/../source.txt", O_RDONLY) != -1 || errno != ENOENT) return 107;
#ifdef __APPLE__
    // Synthetic directory backing lives in the private session, separately
    // from extracted cache content, and has the same read-only boundary.
    fd = open("node_modules", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    if (fd < 0) return 108;
    errno = 0;
    if (fchmod(fd, 0700) != -1 || errno != EROFS) return 109;
    if (fcntl(fd, F_GETPATH, backing)) return 110;
    errno = 0;
    if (chmod(backing, 0700) != -1 || errno != EROFS) return 111;
    if (close(fd)) return 112;
#endif
    return dependency();
}

static int watch(void) {
    int result = dependency();
    if (result) return result;
    int queue;
#ifdef __APPLE__
    queue = kqueue();
    if (queue < 0) return 21;
    int files[] = {
        open("source.txt", O_RDONLY | O_CLOEXEC),
        open("output", O_RDONLY | O_DIRECTORY | O_CLOEXEC),
        open("node_modules/dep/file.txt", O_RDONLY | O_CLOEXEC),
        open("node_modules/dep", O_RDONLY | O_DIRECTORY | O_CLOEXEC)
    };
    struct kevent changes[4];
    for (int index = 0; index < 4; index++) {
        if (files[index] < 0) return 22;
        EV_SET(&changes[index], files[index], EVFILT_VNODE, EV_ADD | EV_CLEAR,
               NOTE_WRITE | NOTE_DELETE | NOTE_RENAME, 0, NULL);
    }
    if (kevent(queue, changes, 4, NULL, 0, NULL)) return 23;
#else
    queue = inotify_init1(IN_CLOEXEC | IN_NONBLOCK);
    if (queue < 0) return 21;
    int file_watch = inotify_add_watch(queue, "source.txt", IN_MODIFY);
    int directory_watch = inotify_add_watch(queue, "output", IN_CREATE);
    if (file_watch < 0 || directory_watch < 0 ||
        inotify_add_watch(queue, "node_modules/dep/file.txt", IN_MODIFY) < 0 ||
        inotify_add_watch(queue, "node_modules/dep", IN_CREATE) < 0) return 22;
#endif
    // Registration precedes the child mutation; no scheduling sleep is needed.
    pid_t child = fork();
    if (child < 0) return 24;
    if (!child) {
        if (dependency()) _exit(25);
        int fd = open("source.txt", O_WRONLY | O_TRUNC | O_CLOEXEC);
        if (fd < 0 || write(fd, "edited", 6) != 6 || close(fd)) _exit(26);
        fd = open("output/new.txt", O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
        if (fd < 0 || write(fd, "output", 6) != 6 || close(fd)) _exit(27);
        _exit(0);
    }
    int file_event = 0, directory_event = 0;
    for (int attempt = 0; attempt < 25 && !(file_event && directory_event); attempt++) {
#ifdef __APPLE__
        struct kevent events[8];
        struct timespec timeout = { .tv_sec = 0, .tv_nsec = 200000000 };
        int count = kevent(queue, NULL, 0, events, 8, &timeout);
        if (count < 0 && errno == EINTR) continue;
        if (count < 0) return 28;
        for (int index = 0; index < count; index++) {
            if (events[index].flags & EV_ERROR) return 29;
            if (!(events[index].fflags & NOTE_WRITE)) continue;
            if (events[index].ident == (uintptr_t)files[0]) file_event = 1;
            if (events[index].ident == (uintptr_t)files[1]) directory_event = 1;
        }
#else
        struct pollfd descriptor = { .fd = queue, .events = POLLIN };
        int observed = poll(&descriptor, 1, 200);
        if (observed < 0 && errno == EINTR) continue;
        if (observed < 0) return 28;
        if (!observed) continue;
        union { struct inotify_event alignment; char bytes[4096]; } buffer;
        ssize_t count = read(queue, buffer.bytes, sizeof(buffer.bytes));
        if (count < 0 && errno == EAGAIN) continue;
        if (count < 0) return 29;
        for (ssize_t offset = 0; offset < count;) {
            struct inotify_event *event = (struct inotify_event *)(buffer.bytes + offset);
            if (event->wd == file_watch && (event->mask & IN_MODIFY)) file_event = 1;
            if (event->wd == directory_watch && (event->mask & IN_CREATE) && !strcmp(event->name, "new.txt")) directory_event = 1;
            offset += (ssize_t)sizeof(*event) + event->len;
        }
#endif
    }
    int status;
    if (waitpid(child, &status, 0) != child || !WIFEXITED(status) || WEXITSTATUS(status)) return 30;
    if (!file_event || !directory_event) return 31;
#ifdef __APPLE__
    for (int index = 0; index < 4; index++) if (close(files[index])) return 32;
#endif
    if (close(queue)) return 33;
    return dependency();
}

int main(int argc, char **argv) {
    alarm(15); // Test-only bound: a failed callback must not hang native CI.
    if (argc != 2) return 59;
    int result;
    if (!strcmp(argv[1], "mutations")) {
        result = descriptor_mutations();
        if (result) return result;
    } else if (!strcmp(argv[1], "aliases")) {
        result = native_aliases();
        if (result) return result;
    } else
#ifndef PNPORT_WATCH_ONLY
    if (!strcmp(argv[1], "library")) {
        void *library = dlopen("node_modules/dep/libprobe" LIBRARY_SUFFIX, RTLD_NOW | RTLD_LOCAL);
        if (!library) return 60;
        int (*probe)(void) = (int (*)(void))dlsym(library, "pnport_library_probe");
        result = !probe ? 61 : probe();
        if (dlclose(library)) return 62;
        if (result != 42) return result;
    } else
#endif
    {
        if (strcmp(argv[1], "watch")) return 63;
        result = watch();
        if (result) return result;
    }
    puts("pnport native conformance");
    return 0;
}
#endif
