/* A static Linux child must be observed by seccomp, without LD_PRELOAD. */
#define _GNU_SOURCE
#include <stdio.h>
#include <fcntl.h>
#include <string.h>
#include <unistd.h>
#include <sys/syscall.h>
int main(int argc, char **argv) {
    if (argc == 3 && strstr(argv[1], "xattr")) {
        char buffer[4096];
        if (!strcmp(argv[1], "getxattr")) syscall(SYS_getxattr, argv[2], "user.runlens", buffer, sizeof(buffer));
        else if (!strcmp(argv[1], "lgetxattr")) syscall(SYS_lgetxattr, argv[2], "user.runlens", buffer, sizeof(buffer));
        else if (!strcmp(argv[1], "listxattr")) syscall(SYS_listxattr, argv[2], buffer, sizeof(buffer));
        else if (!strcmp(argv[1], "llistxattr")) syscall(SYS_llistxattr, argv[2], buffer, sizeof(buffer));
        else {
            int fd = open(argv[2], O_WRONLY);
            if (!strcmp(argv[1], "fgetxattr")) syscall(SYS_fgetxattr, fd, "user.runlens", buffer, sizeof(buffer));
            else syscall(SYS_flistxattr, fd, buffer, sizeof(buffer));
            close(fd);
        }
        return 0;
    }
    if (argc == 3 && (!strcmp(argv[1], "execve-script") || !strcmp(argv[1], "execveat-script") || !strcmp(argv[1], "execveat-empty-script"))) {
        extern char **environ;
        char *args[] = {argv[2], NULL};
        if (!strcmp(argv[1], "execve-script")) syscall(SYS_execve, argv[2], args, environ);
        else {
            int empty = !strcmp(argv[1], "execveat-empty-script");
            int fd = empty ? open(argv[2], O_RDONLY) : AT_FDCWD;
            syscall(SYS_execveat, fd, empty ? "" : argv[2], args, environ, empty ? AT_EMPTY_PATH : 0);
        }
        return 93;
    }
    if (argc == 3 && !strncmp(argv[1], "readlink", 8)) {
        char buffer[4096];
#ifdef SYS_readlink
        if (!strcmp(argv[1], "readlink")) {
            syscall(SYS_readlink, argv[2], buffer, sizeof(buffer));
            return 0;
        }
#endif
        int empty = !strcmp(argv[1], "readlinkat-empty");
        int fd = empty ? open(argv[2], O_PATH | O_NOFOLLOW) : AT_FDCWD;
        syscall(SYS_readlinkat, fd, empty ? "" : argv[2], buffer, sizeof(buffer));
        if (fd >= 0) close(fd);
        return 0;
    }
    if (argc == 4 && !strncmp(argv[1], "mutate-", 7)) {
        int fd = open(argv[3], O_RDONLY | O_DIRECTORY);
        if (fd < 0) return 26;
        const char *relative = argv[2] + strlen(argv[3]) + 1;
        if (!strcmp(argv[1], "mutate-mkdirat")) syscall(SYS_mkdirat, fd, relative, 0700);
        else if (!strcmp(argv[1], "mutate-chmodat")) syscall(SYS_fchmodat, fd, relative, 0600);
        else if (!strcmp(argv[1], "mutate-chownat")) syscall(SYS_fchownat, fd, relative, getuid(), getgid(), 0);
        else if (!strcmp(argv[1], "mutate-truncate")) syscall(SYS_truncate, argv[2], 0);
        else if (!strcmp(argv[1], "mutate-utimensat")) syscall(SYS_utimensat, fd, relative, NULL, 0);
        else if (!strcmp(argv[1], "mutate-linkat")) syscall(SYS_linkat, AT_FDCWD, "input.txt", fd, relative, 0);
        else if (!strcmp(argv[1], "mutate-symlinkat")) syscall(SYS_symlinkat, "opaque-target", fd, relative);
        else return 27;
        close(fd);
        return 0;
    }
    if (argc == 3 && !strncmp(argv[1], "open-", 5)) {
        int flags = O_RDONLY;
        if (!strcmp(argv[1], "open-create")) flags |= O_CREAT;
        if (!strcmp(argv[1], "open-truncate")) flags |= O_TRUNC;
        long fd = syscall(SYS_openat, AT_FDCWD, argv[2], flags, 0600);
        if (fd >= 0) close((int)fd);
        return 0;
    }
    if (argc == 5 && !strncmp(argv[1], "delete-", 7)) {
        int directory = open(argv[3], O_RDONLY | O_DIRECTORY);
        if (directory < 0) return 24;
        const char *relative = strrchr(argv[2], '/');
        if (!relative) return 25;
        long result;
#ifdef SYS_unlink
        if (!strcmp(argv[1], "delete-unlink")) {
            result = syscall(SYS_unlink, argv[2]);
        } else if (!strcmp(argv[1], "delete-rmdir")) {
            result = syscall(SYS_rmdir, argv[2]);
        } else
#endif
        if (!strcmp(argv[1], "delete-unlinkat")) {
            result = syscall(SYS_unlinkat, AT_FDCWD, argv[2], 0);
        } else {
            int flags = !strcmp(argv[1], "delete-directory-at") ? AT_REMOVEDIR : 0;
            result = syscall(SYS_unlinkat, directory, relative + 1, flags);
        }
        close(directory);
        return (result == 0) != (!strcmp(argv[4], "present"));
    }
    if (argc == 5) {
        int destination = open(argv[4], O_RDONLY | O_DIRECTORY);
        if (destination < 0) return 23;
        long result;
#ifdef SYS_rename
        if (!strcmp(argv[1], "rename")) {
            char path[4096];
            snprintf(path, sizeof(path), "%s/%s", argv[4], argv[3]);
            result = syscall(SYS_rename, argv[2], path);
        } else
#endif
        if (!strcmp(argv[1], "renameat")) {
            result = syscall(SYS_renameat, AT_FDCWD, argv[2], destination, argv[3]);
        } else {
            result = syscall(SYS_renameat2, AT_FDCWD, argv[2], destination, argv[3], 0);
        }
        close(destination);
        return (result == 0) != (!strcmp(argv[2], "input.txt"));
    }
    FILE *input = fopen("input.txt", "rb");
    if (!input) return 21;
    int value = fgetc(input);
    fclose(input);
    FILE *output = fopen("static-output.txt", "wb");
    if (!output) return 22;
    fputc(value, output);
    return fclose(output) != 0;
}
