#define _GNU_SOURCE
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
#ifdef __linux__
#include <sys/syscall.h>
#endif

#define CHECK(condition) do { if (!(condition)) { fprintf(stderr, "directory check line=%d errno=%d\n", __LINE__, errno); exit(1); } } while (0)

static int drain(DIR *dir, int reentrant) {
    int count = 0;
    struct dirent storage, *entry;
    for (;;) {
        errno = EBUSY;
        if (reentrant) {
            CHECK(readdir_r(dir, &storage, &entry) == 0);
        } else {
            entry = readdir(dir);
            CHECK(errno == EBUSY);
        }
        if (!entry) break;
        if (!strcmp(entry->d_name, "node_modules")) {
            CHECK(entry->d_type == DT_DIR);
            CHECK(entry->d_ino != 0);
            count++;
        }
    }
    return count;
}

static _Thread_local const char *callback_directory;
static int select_without_dependencies(const struct dirent *entry) {
    if (strcmp(entry->d_name, "node_modules")) return 1;
    struct stat info;
    CHECK(stat(callback_directory, &info) == 0 && S_ISDIR(info.st_mode));
    return 0;
}

static void check_scan(const char *path, int filtered, int sorted) {
    struct dirent **entries = NULL;
    int count = scandir(path, &entries, filtered ? select_without_dependencies : NULL,
                        sorted ? alphasort : NULL);
    CHECK(count >= 0);
    int found = 0;
    for (int i = 0; i < count; i++) {
        if (sorted && i) CHECK(strcmp(entries[i - 1]->d_name, entries[i]->d_name) <= 0);
        if (!strcmp(entries[i]->d_name, "node_modules")) {
            CHECK(entries[i]->d_type == DT_DIR);
            found++;
        }
    }
    for (int i = 0; i < count; i++) free(entries[i]);
    free(entries);
    CHECK(found == (filtered ? 0 : 1));
}

static void check_parent(const char *path) {
    char dependencies[4096];
    CHECK(snprintf(dependencies, sizeof(dependencies), "%s/node_modules", path) < sizeof(dependencies));
    struct stat info;
    CHECK(stat(dependencies, &info) == 0 && S_ISDIR(info.st_mode));
    DIR *direct = opendir(dependencies);
    CHECK(direct != NULL);
    CHECK(closedir(direct) == 0);
    callback_directory = dependencies;
    check_scan(path, 0, 0);
    check_scan(path, 0, 1);
    check_scan(path, 1, 1);
    DIR *first = opendir(path), *second = opendir(path);
    CHECK(first && second);
    CHECK(drain(first, 0) == 1);
    CHECK(drain(first, 1) == 0);
    long end = telldir(first);
    rewinddir(first);
    seekdir(first, end);
    int remaining = drain(first, 0);
    // Darwin's native EOF cookie can be zero, which seeks to the beginning.
    // Preserve that behavior for ZIP directories with an existing native entry;
    // the appended entry uses a distinct nonzero end cookie.
    int expected_remaining = 0;
#ifdef __APPLE__
    if (end == 0) expected_remaining = 1;
#endif
    if (remaining != expected_remaining) fprintf(stderr, "seek parent=%s position=%ld remaining=%d\n", path, end, remaining);
    CHECK(remaining == expected_remaining);
    rewinddir(first);
    CHECK(drain(first, 1) == 1);
    CHECK(drain(second, 1) == 1);
    rewinddir(second);
    // Save the position immediately before each record, then restore the
    // position before the virtual entry after fully rewinding the stream.
    long before = 0;
    struct dirent *entry;
    for (;;) {
        before = telldir(second);
        entry = readdir(second);
        CHECK(entry != NULL);
        if (!strcmp(entry->d_name, "node_modules")) break;
    }
    rewinddir(second);
    seekdir(second, before);
    CHECK(drain(second, 0) == 1);
    CHECK(closedir(first) == 0 && closedir(second) == 0);
    for (int i = 0; i < 8; i++) {
        int fd = open(path, O_RDONLY | O_DIRECTORY);
        CHECK(fd >= 0);
        DIR *dir = fdopendir(fd);
        CHECK(dir != NULL);
        CHECK(drain(dir, i % 2) == 1);
        CHECK(closedir(dir) == 0);
    }
}

static void *parallel_stream(void *path) {
    for (int i = 0; i < 8; i++) check_parent(path);
    return NULL;
}

#ifdef __linux__
static void raw_directory(int call, int legacy) {
    int fd = open(".", O_RDONLY | O_DIRECTORY);
    CHECK(fd >= 0);
    char buffer[4096];
    ssize_t size;
    int count = 0;
    off_t native_end = 0;
    while ((size = syscall(call, fd, buffer, sizeof(buffer))) > 0) {
        for (size_t offset = 0; offset < size;) {
            uint16_t length;
            memcpy(&length, buffer + offset + 16, 2);
            CHECK(length > 19 && offset + length <= size);
            if (!strcmp(buffer + offset + (legacy ? 18 : 19), "node_modules")) {
                CHECK((unsigned char)buffer[offset + (legacy ? length - 1 : 18)] == DT_DIR);
                count++;
            } else {
                memcpy(&native_end, buffer + offset + 8, 8);
            }
            offset += length;
        }
    }
    CHECK(size == 0 && count == 1);
    CHECK(lseek(fd, native_end, SEEK_SET) == native_end);
    errno = 0;
    CHECK(syscall(call, fd, buffer, 1) == -1 && errno == EINVAL);
    errno = 0;
    CHECK(syscall(call, fd, NULL, sizeof(buffer)) == -1 && errno == EFAULT);
    CHECK(syscall(call, fd, buffer, sizeof(buffer)) == 32);
    CHECK(!strcmp(buffer + (legacy ? 18 : 19), "node_modules"));
    CHECK(syscall(call, fd, buffer, sizeof(buffer)) == 0);
    int copied = dup(fd);
    CHECK(copied >= 0 && syscall(call, copied, buffer, sizeof(buffer)) == 0);
    long end = lseek(fd, 0, SEEK_CUR);
    CHECK(end != -1);
    CHECK(lseek(copied, 0, SEEK_SET) == 0);
    CHECK(lseek(fd, end, SEEK_SET) == end);
    CHECK(syscall(call, copied, buffer, sizeof(buffer)) == 0);
    CHECK(lseek(fd, 0, SEEK_SET) == 0);
    count = 0;
    while ((size = syscall(call, copied, buffer, 32)) > 0) {
        for (size_t offset = 0; offset < size;) {
            uint16_t length;
            memcpy(&length, buffer + offset + 16, 2);
            if (!strcmp(buffer + offset + (legacy ? 18 : 19), "node_modules")) count++;
            offset += length;
        }
    }
    // A short native buffer can reject an ordinary long filename. Finish
    // with the larger buffer, preserving the unconsumed virtual record.
    CHECK(size == 0 || errno == EINVAL);
    while ((size = syscall(call, copied, buffer, sizeof(buffer))) > 0) {
        for (size_t offset = 0; offset < size;) {
            uint16_t length;
            memcpy(&length, buffer + offset + 16, 2);
            if (!strcmp(buffer + offset + (legacy ? 18 : 19), "node_modules")) count++;
            offset += length;
        }
    }
    CHECK(size == 0 && count == 1);
    CHECK(close(fd) == 0 && close(copied) == 0);
}
#endif

int main(int argc, char **argv) {
    if (argc > 1 && !strcmp(argv[1], "control")) {
        DIR *dir = opendir(".");
        CHECK(dir && drain(dir, 0) == 0 && closedir(dir) == 0);
        return 0;
    }
    if (argc > 1 && !strcmp(argv[1], "conflict")) {
        DIR *dir = opendir("nested");
        CHECK(dir != NULL);
        int marker = open("conflict-entered", O_WRONLY | O_CREAT, 0600);
        CHECK(marker >= 0 && close(marker) == 0);
        // An unvirtualized fixture owner introduces the conflict. The child
        // cannot create it through pnport's read-only dependency namespace.
        for (int i = 0; access("conflict-created", F_OK) && i < 1000; i++) usleep(10000);
        CHECK(access("conflict-created", F_OK) == 0);
        errno = 0;
        while (readdir(dir)) {}
        CHECK(errno == EEXIST);
        closedir(dir);
        return 0;
    }
    for (int i = 1; i < argc; i++) check_parent(argv[i]);
    pthread_t threads[4];
    for (int i = 0; i < 4; i++) CHECK(pthread_create(&threads[i], NULL, parallel_stream, ".") == 0);
    for (int i = 0; i < 4; i++) CHECK(pthread_join(threads[i], NULL) == 0);
    DIR *root = opendir(".");
    int native = 0;
    struct dirent *entry;
    while ((entry = readdir(root))) if (!strcmp(entry->d_name, "native-entry")) native++;
    CHECK(native == 1 && closedir(root) == 0);
    errno = 0;
    CHECK(opendir("absent-directory") == NULL && errno == ENOENT);
    int file = open("native-entry", O_RDONLY);
    CHECK(file >= 0);
    errno = 0;
    CHECK(fdopendir(file) == NULL && errno == ENOTDIR);
    CHECK(close(file) == 0);
    file = open("directory-output", O_CREAT | O_WRONLY, 0600);
    CHECK(file >= 0 && write(file, "native", 6) == 6 && close(file) == 0);
    errno = 0;
    CHECK(open("node_modules/one/file.txt", O_WRONLY) == -1 && errno == EROFS);
#ifdef __linux__
    raw_directory(SYS_getdents64, 0);
#ifdef SYS_getdents
    raw_directory(SYS_getdents, 1);
#endif
#endif
    puts("directory-listings-ok");
    return 0;
}
