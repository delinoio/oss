#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/syscall.h>
#include <sys/stat.h>
#include <unistd.h>

#define CHECK(expression) do { if (!(expression)) { \
    fprintf(stderr, "readlink conformance line=%d errno=%d\n", __LINE__, errno); \
    return 1; \
} } while (0)

static int check_link(const char *path) {
    char target[4096], actual[4096], absolute[4096], parent[4096];
    memset(target, 0x5a, sizeof(target));
    ssize_t length = readlink(path, target, sizeof(target) - 1);
    if (!(length > 0 && length < (ssize_t)sizeof(target) - 1 && target[length] == 0x5a)) {
        fprintf(stderr, "readlink result=%zd errno=%d\n", length, errno);
        return 1;
    }
    CHECK(getcwd(absolute, sizeof(absolute)));
    size_t cwd_length = strlen(absolute);
    CHECK(snprintf(absolute + cwd_length, sizeof(absolute) - cwd_length, "/%s", path) > 0);
    CHECK(strlen(path) < sizeof(parent));
    strcpy(parent, path);
    char *name = strrchr(parent, '/');
    CHECK(name);
    *name++ = 0;
    int root = open(".", O_RDONLY | O_DIRECTORY);
    int directory = open(parent, O_RDONLY | O_DIRECTORY);
    CHECK(root >= 0 && directory >= 0);
    int duplicated = dup(directory);
    // fcntl bypasses pnport's dup hook; ordinary untracked directory handles
    // must still resolve through F_GETPATH, including non-interposed opens.
    int untracked = fcntl(root, F_DUPFD_CLOEXEC, 0);
    // This test intentionally bypasses interposition to exercise F_GETPATH.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
    int raw_root = syscall(SYS_open, ".", O_RDONLY | O_DIRECTORY, 0);
#pragma clang diagnostic pop
    int untracked_view = fcntl(directory, F_DUPFD_CLOEXEC, 0);
    CHECK(duplicated >= 0 && untracked >= 0 && raw_root >= 0 && untracked_view >= 0);
    int descriptors[] = {AT_FDCWD, root, untracked, raw_root, directory, duplicated, untracked_view, -1};
    const char *paths[] = {path, path, path, path, name, name, name, absolute};
    for (int i = 0; i < 8; i++) {
        memset(actual, 0x5a, sizeof(actual));
        ssize_t at_length = readlinkat(descriptors[i], paths[i], actual, sizeof(actual) - 1);
        if (at_length != length) {
            fprintf(stderr, "readlink descriptor_case=%d actual=%zd expected=%zd errno=%d\n", i, at_length, length, errno);
            return 1;
        }
        CHECK(!memcmp(target, actual, length) && actual[length] == 0x5a);
        memset(actual, 0x5a, sizeof(actual));
        CHECK(readlinkat(descriptors[i], paths[i], actual, 3) == 3);
        CHECK(!memcmp(target, actual, 3) && actual[3] == 0x5a);
        CHECK(readlinkat(descriptors[i], paths[i], NULL, 0) == 0);
        CHECK(readlinkat(descriptors[i], paths[i], (char *)1, 0) == 0);
        errno = 0;
        CHECK(readlinkat(descriptors[i], paths[i], NULL, 16) == -1 && errno == EFAULT);
        errno = 0;
        CHECK(readlinkat(descriptors[i], paths[i], (char *)1, 16) == -1 && errno == EFAULT);
        errno = 0;
        CHECK(readlinkat(descriptors[i], paths[i], actual, (size_t)INT_MAX + 1) == -1 && errno == EINVAL);
    }
    CHECK(readlink(path, NULL, 0) == 0);
    errno = 0;
    CHECK(readlink(path, (char *)1, 16) == -1 && errno == EFAULT);
    char *readonly = mmap(NULL, 4096, PROT_READ, MAP_PRIVATE | MAP_ANON, -1, 0);
    CHECK(readonly != MAP_FAILED);
    errno = 0;
    CHECK(readlinkat(AT_FDCWD, path, readonly, 16) == -1 && errno == EFAULT);
    CHECK(munmap(readonly, 4096) == 0);
    close(root); close(directory); close(duplicated); close(untracked); close(raw_root); close(untracked_view);
    return 0;
}

static int check_renamed_native_directory(void) {
    char actual[64];
    CHECK(mkdir("renamed-old", 0700) == 0);
    CHECK(symlink("renamed-target", "renamed-old/link") == 0);
    int directory = open("renamed-old", O_RDONLY | O_DIRECTORY);
    CHECK(directory >= 0);
    CHECK(rename("renamed-old", "renamed-new") == 0);
    ssize_t length = readlinkat(directory, "link", actual, sizeof(actual));
    CHECK(length == (ssize_t)strlen("renamed-target"));
    CHECK(!memcmp(actual, "renamed-target", length));
    CHECK(close(directory) == 0);
    CHECK(unlink("renamed-new/link") == 0);
    CHECK(rmdir("renamed-new") == 0);
    return 0;
}

int main(int argc, char **argv) {
    CHECK(argc > 1);
    // These real symlinks and files provide kernel controls for the same
    // buffer and descriptor cases used for virtual dependencies below.
    CHECK(symlink("ordinary-target", "source-link") == 0);
    CHECK(check_link("./source-link") == 0);
    CHECK(check_renamed_native_directory() == 0);
    int file = open("ordinary-file", O_CREAT | O_RDONLY, 0600);
    CHECK(file >= 0);
    char output[4096];
    const char *paths[] = {"ordinary-file", "node_modules/dep/package.json", "missing", "node_modules/missing"};
    for (int i = 0; i < 4; i++) {
        errno = 0;
        CHECK(readlink(paths[i], output, sizeof(output)) == -1);
        int native_error = errno;
        CHECK(native_error == (i < 2 ? EINVAL : ENOENT));
        errno = 0;
        CHECK(readlinkat(AT_FDCWD, paths[i], output, sizeof(output)) == -1 && errno == native_error);
    }
    errno = 0;
    CHECK(readlinkat(-1, "node_modules/dep", output, sizeof(output)) == -1 && errno == EBADF);
    errno = 0;
    CHECK(readlinkat(AT_FDCWD, (char *)1, output, sizeof(output)) == -1 && errno == EFAULT);
    errno = 0;
    CHECK(readlinkat(file, "../node_modules/dep", output, sizeof(output)) == -1 && errno == ENOTDIR);
    close(file);
    for (int i = 1; i < argc; i++) {
        if (check_link(argv[i])) {
            fprintf(stderr, "readlink link_case=%d\n", i);
            return 1;
        }
    }
    ssize_t one = readlink("node_modules/one", output, sizeof(output));
    char second[4096];
    ssize_t two = readlink("node_modules/two", second, sizeof(second));
    CHECK(one > 0 && two > 0 && (one != two || memcmp(output, second, one)));
    errno = 0;
    CHECK(open("node_modules/unplugged/package.json", O_WRONLY) == -1 && errno == EROFS);
    CHECK(unlink("source-link") == 0 && unlink("ordinary-file") == 0);
    puts("readlinkat-conformance-ok");
    return 0;
}
