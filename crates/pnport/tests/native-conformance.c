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
