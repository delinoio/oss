// SPDX-License-Identifier: Apache-2.0
#define _GNU_SOURCE
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>
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
int main(int argc, char **argv) {
    if (argc > 1 && !strcmp(argv[1], "conflict")) {
        int fd = open("entered", O_CREAT | O_WRONLY, 0600);
        CHECK(fd >= 0); close(fd);
        while (access("go", F_OK)) usleep(1000);
        struct stat metadata;
        (void)stat(argc > 2 ? argv[2] : "node_modules/dep/file.txt", &metadata);
        return 2;
    }
    CHECK(make("node_modules"));
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
    int link = openat(copy, "dep", O_PATH | O_NOFOLLOW);
    CHECK(link >= 0 && fstat(link, &link_metadata) == 0 && S_ISLNK(link_metadata.st_mode));
    CHECK(readlinkat(link, "", bytes, sizeof(bytes)) > 0); close(link);
    int watcher = inotify_init1(IN_CLOEXEC); CHECK(watcher >= 0);
    CHECK(inotify_add_watch(watcher, "node_modules/dep", IN_ATTRIB | IN_DONT_FOLLOW) >= 0);
    close(watcher);
#endif
    errno = 0; CHECK(mkdirat(copy, ".bin", 0700) == -1 && errno == EROFS);
    errno = 0; CHECK(rmdir("node_modules") == -1 && errno == EROFS);
    errno = 0; CHECK(rename("node_modules", "moved-modules") == -1 && errno == EROFS);
    CHECK(symlink("../node_modules/dep", "node_modules/.cache-link") == 0 || errno == EEXIST);
    errno = 0; CHECK(open("node_modules/.cache-link/file.txt", O_WRONLY) == -1 && errno == EROFS);
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
    CHECK(lseek(root, 0, SEEK_SET) == 0);
    pid_t child = fork(); CHECK(child >= 0);
    if (!child) { DIR *shared = fdopendir(copy); _exit(shared && listings(shared) == 0 ? 0 : 1); }
    int status; CHECK(waitpid(child, &status, 0) == child && WIFEXITED(status) && WEXITSTATUS(status) == 0);
    close(copy); close(root);
    CHECK(unlink("node_modules/.cache-file") == 0);
    puts("cache-conformance-ok");
    return 0;
}
