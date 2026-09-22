#include <dirent.h>
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <spawn.h>
#include <unistd.h>

int main(int argc, char **argv) {
    if (argc != 3 || strcmp(argv[1], "") || strcmp(argv[2], "literal;$() argument")) return 20;
    void *main_program = dlopen(NULL, RTLD_LAZY);
    if (!main_program) return 41;
    if (!dlsym(main_program, "malloc")) return 42;
    if (dlclose(main_program)) return 43;
    struct stat info;
    if (stat("node_modules/dep/file.txt", &info) || info.st_size != 13) return 21;
    int fd = open("node_modules/dep/file.txt", O_RDONLY);
    if (fd < 0) { perror("open"); return 22; }
    char *bytes = mmap(NULL, 13, PROT_READ, MAP_PRIVATE, fd, 0);
    if (bytes == MAP_FAILED || memcmp(bytes, "package bytes", 13)) return 23;
    munmap(bytes, 13); close(fd);
    DIR *dir = opendir("node_modules");
    if (!dir) { perror("opendir"); return 24; }
    int found = 0; struct dirent *entry;
    while ((entry = readdir(dir))) if (!strcmp(entry->d_name, "dep")) found = 1;
    closedir(dir); if (!found) return 25;
    errno = 0;
    if (open("node_modules/dep/file.txt", O_WRONLY) != -1 || errno != EROFS) return 26;
    fd = open("output.txt", O_CREAT | O_WRONLY, 0600);
    if (fd < 0 || write(fd, "native", 6) != 6) return 27;
    close(fd);
    char *canonical = realpath("node_modules/dep/file.txt", NULL);
    if (!canonical || !strstr(canonical, "cache.zip/node_modules/dep/file.txt")) return 28;
    free(canonical);
    dir = opendir("node_modules/dep");
    if (!dir) return 29;
    fd = openat(dirfd(dir), "file.txt", O_RDONLY);
    if (fd < 0) return 30;
    close(fd); closedir(dir);
    if (lstat("node_modules/dep", &info) || !S_ISLNK(info.st_mode)) return 31;
    struct stat link_info = info;
    char target[4096];
    if (readlink("node_modules/dep", target, sizeof(target)) != link_info.st_size) return 32;
    dir = opendir("node_modules");
    if (!dir) return 35;
    if (fstatat(dirfd(dir), "dep", &info, AT_SYMLINK_NOFOLLOW)
        || !S_ISLNK(info.st_mode) || info.st_size != link_info.st_size) return 36;
    if (fstatat(AT_FDCWD, "node_modules/dep", &info, AT_SYMLINK_NOFOLLOW)
        || !S_ISLNK(info.st_mode) || info.st_size != link_info.st_size) return 37;
    if (fstatat(dirfd(dir), "dep", &info, 0) || !S_ISDIR(info.st_mode)) return 38;
    if (fstatat(dirfd(dir), "dep/file.txt", &info, AT_SYMLINK_NOFOLLOW)
        || !S_ISREG(info.st_mode) || info.st_size != 13) return 39;
    errno = 0;
    if (fstatat(dirfd(dir), "dep/missing", &info, AT_SYMLINK_NOFOLLOW) != -1
        || errno != ENOENT) return 40;
    closedir(dir);
    if (!getenv("PNPORT_TEST_CHILD")) {
        pid_t child;
        char *environment[] = {"PNPORT_TEST_CHILD=1", NULL};
        if (posix_spawn(&child, argv[0], NULL, NULL, argv, environment)) return 33;
        int status;
        if (waitpid(child, &status, 0) != child || !WIFEXITED(status) || WEXITSTATUS(status)) return 34;
    }
    puts("protocol-output");
    return 0;
}
