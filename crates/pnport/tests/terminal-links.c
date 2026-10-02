#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#define CHECK(expression) do { if (!(expression)) { \
    fprintf(stderr, "terminal-link check failed at line %d: %s (errno=%d)\n", \
            __LINE__, #expression, errno); return 1; } } while (0)

static int check_link(int dirfd, const char *path, char *target) {
    struct stat info;
    CHECK(fstatat(dirfd, path, &info, AT_SYMLINK_NOFOLLOW) == 0 && S_ISLNK(info.st_mode));
    ssize_t length;
    char short_target[3] = {0};
#ifdef __APPLE__
    // macOS readlinkat mediation is tracked separately. Exercise the admitted
    // readlink entrypoint through the same directory handle's logical cwd.
    int previous = open(".", O_RDONLY | O_DIRECTORY);
    CHECK(previous >= 0 && (dirfd == AT_FDCWD || fchdir(dirfd) == 0));
    length = readlink(path, target, 4095);
    CHECK(length > 3 && readlink(path, short_target, sizeof(short_target)) == 3);
    CHECK(fchdir(previous) == 0 && close(previous) == 0);
#else
    length = readlinkat(dirfd, path, target, 4095);
    CHECK(length > 3 && readlinkat(dirfd, path, short_target, sizeof(short_target)) == 3);
#endif
    target[length] = '\0';
    CHECK(info.st_size == length && memcmp(target, short_target, 3) == 0);
    CHECK(fstatat(dirfd, path, &info, 0) == 0 && S_ISDIR(info.st_mode));
    return 0;
}

static int check_descendants(const char *alias) {
    char path[4096], target[4096];
    struct stat info;
    const char *suffixes[] = {"file.txt", "subdir", "node_modules"};
    for (int i = 0; i < 3; i++) {
        CHECK(snprintf(path, sizeof(path), "%s/%s", alias, suffixes[i]) > 0);
        CHECK(lstat(path, &info) == 0);
        CHECK(i == 0 ? S_ISREG(info.st_mode) : S_ISDIR(info.st_mode));
        errno = 0;
        CHECK(readlink(path, target, sizeof(target)) == -1 && errno == EINVAL);
    }
    CHECK(snprintf(path, sizeof(path), "%s/missing.txt", alias) > 0);
    errno = 0;
    CHECK(lstat(path, &info) == -1 && errno == ENOENT);
    errno = 0;
    CHECK(readlink(path, target, sizeof(target)) == -1 && errno == ENOENT);
    const char *links[] = {"file-link", "broken-link"};
    const char *targets[] = {"file.txt", "missing.txt"};
    for (int i = 0; i < 2; i++) {
        CHECK(snprintf(path, sizeof(path), "%s/%s", alias, links[i]) > 0);
        CHECK(lstat(path, &info) == 0 && S_ISLNK(info.st_mode));
        ssize_t length = readlink(path, target, sizeof(target));
        CHECK(length == (ssize_t)strlen(targets[i]) && memcmp(target, targets[i], length) == 0);
        if (i == 0) CHECK(stat(path, &info) == 0 && S_ISREG(info.st_mode));
        else { errno = 0; CHECK(stat(path, &info) == -1 && errno == ENOENT); }
    }
    CHECK(snprintf(path, sizeof(path), "%s/new.txt", alias) > 0);
    errno = 0;
    CHECK(open(path, O_WRONLY | O_CREAT, 0600) == -1 && errno == EROFS);
    CHECK(snprintf(path, sizeof(path), "%s/file.txt", alias) > 0);
    errno = 0;
    CHECK(open(path, O_WRONLY | O_TRUNC) == -1 && errno == EROFS);
    return 0;
}

int main(void) {
    char peers[2][4096], direct[4096], nested[4096], path[4096];
    struct stat info;
    const char *contexts[] = {"one", "two"};
    CHECK(check_link(AT_FDCWD, "node_modules/unplugged", direct) == 0);
    for (int i = 0; i < 2; i++) {
        CHECK(snprintf(path, sizeof(path), "node_modules/@scope/inner-%s", contexts[i]) > 0);
        CHECK(check_link(AT_FDCWD, path, peers[i]) == 0);
        CHECK(lstat(path, &info) == 0 && S_ISLNK(info.st_mode));
        const char *formats[] = {
            "node_modules/outer-%s/node_modules/@scope/inner",
            "node_modules/outer-%s/node_modules/alias",
            "node_modules/wrapper/node_modules/outer-%s/node_modules/@scope/inner"
        };
        for (int j = 0; j < 3; j++) {
            CHECK(snprintf(path, sizeof(path), formats[j], contexts[i]) > 0);
            CHECK(check_link(AT_FDCWD, path, nested) == 0 && strcmp(nested, peers[i]) == 0);
            CHECK(lstat(path, &info) == 0 && S_ISLNK(info.st_mode));
            CHECK(check_descendants(path) == 0);
            int inner = open(path, O_RDONLY | O_DIRECTORY);
            CHECK(inner >= 0);
            CHECK(check_link(inner, "node_modules/unplugged", nested) == 0 && strcmp(nested, direct) == 0);
            errno = 0;
            CHECK(openat(inner, "file.txt", O_WRONLY) == -1 && errno == EROFS);
            CHECK(close(inner) == 0);
        }
        CHECK(snprintf(path, sizeof(path), "node_modules/wrapper/node_modules/outer-%s", contexts[i]) > 0);
        int outer = open(path, O_RDONLY | O_DIRECTORY);
        CHECK(outer >= 0);
        CHECK(check_link(outer, "node_modules/@scope/inner", nested) == 0 && strcmp(nested, peers[i]) == 0);
        CHECK(check_link(outer, "node_modules/alias", nested) == 0 && strcmp(nested, peers[i]) == 0);
        CHECK(fstatat(outer, "node_modules/alias/file.txt", &info, AT_SYMLINK_NOFOLLOW) == 0 && S_ISREG(info.st_mode));
        errno = 0;
        CHECK(fstatat(outer, "node_modules/alias/missing.txt", &info, AT_SYMLINK_NOFOLLOW) == -1 && errno == ENOENT);
        CHECK(close(outer) == 0);
    }
    CHECK(strcmp(peers[0], peers[1]) != 0);
    CHECK(lstat("source-link", &info) == 0 && S_ISLNK(info.st_mode));
    ssize_t length = readlink("source-link", nested, sizeof(nested));
    CHECK(length == 10 && memcmp(nested, "source.txt", 10) == 0);
    CHECK(stat("source-link", &info) == 0 && S_ISREG(info.st_mode));
    int output = open("output.txt", O_WRONLY | O_CREAT | O_TRUNC, 0600);
    CHECK(output >= 0 && write(output, "output", 6) == 6 && close(output) == 0);
    int source = open("source.txt", O_WRONLY | O_APPEND);
    CHECK(source >= 0 && write(source, "!", 1) == 1 && close(source) == 0);
    errno = 0;
    CHECK(open("node_modules/unplugged/file.txt", O_WRONLY) == -1 && errno == EROFS);
    puts("terminal-link conformance passed");
    return 0;
}
