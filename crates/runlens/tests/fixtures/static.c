/* A static Linux child must be observed by seccomp, without LD_PRELOAD. */
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <errno.h>
#include <fcntl.h>
#include <string.h>
#include <unistd.h>
#include <sys/syscall.h>
#include <sys/vfs.h>
#include <sys/stat.h>
#include <sys/inotify.h>
#include <sys/fanotify.h>
#include <sched.h>
#include <sys/mount.h>
#include <sys/wait.h>
/* Ubuntu 22.04 musl headers predate mount_setattr; Linux UAPI assigns 442
 * on both supported x86_64/aarch64 ABIs. Remove when minimum headers expose it. */
#ifndef SYS_mount_setattr
#define SYS_mount_setattr 442
#endif
int main(int argc, char **argv) {
    if (argc == 4 && !strcmp(argv[1], "handle-open")) {
        struct { unsigned int bytes; int kind; unsigned char data[128]; } handle = {128, 0, {0}};
        int mount_id = 0, mount = -1;
        if (strcmp(argv[3], "invalid") && syscall(SYS_name_to_handle_at, AT_FDCWD, argv[2], &handle, &mount_id, 0) == 0)
            mount = open(argv[2], O_PATH);
        else handle.bytes = 0;
        int fd = syscall(SYS_open_by_handle_at, mount, &handle, !strcmp(argv[3], "write") ? O_WRONLY : O_RDONLY);
        if (fd < 0) printf("error=%d\n", errno);
        else {
            if (!strcmp(argv[3], "write") && write(fd, "HANDLE-WRITE-CANARY", 18) != 18) return 90;
            puts("opened"); close(fd);
        }
        if (mount >= 0) close(mount);
        return 0;
    }

    if (argc == 4 && !strcmp(argv[1], "fd-stat")) {
        int pipes[2] = {-1, -1}, fd;
        if (!strcmp(argv[3], "invalid")) fd = -1;
        else if (!strcmp(argv[3], "pipe")) { if (pipe(pipes)) return 90; fd = pipes[0]; }
        else fd = open(argv[2], O_WRONLY);
        struct stat stats;
        long result = syscall(SYS_fstat, fd, !strcmp(argv[3], "bad-buffer") ? NULL : &stats);
        if (result == 0) printf("size=%lld kind=%u\n", (long long)stats.st_size, stats.st_mode & S_IFMT);
        else printf("error=%d\n", errno);
        if (fd >= 0) close(fd);
        if (pipes[1] >= 0) close(pipes[1]);
        return 0;
    }

    if (argc == 2 && !strncmp(argv[1], "namespace-", 10)) {
        long result;
        uint64_t clone_args[11] = {0}; clone_args[0] = CLONE_NEWNS; clone_args[4] = SIGCHLD;
        const char *mode = argv[1];
        if (!strcmp(mode, "namespace-unshare")) result = syscall(SYS_unshare, CLONE_NEWNS);
        else if (!strcmp(mode, "namespace-setns")) result = syscall(SYS_setns, -1, CLONE_NEWNS);
        else if (!strcmp(mode, "namespace-mount")) result = syscall(SYS_mount, "", "", NULL, MS_BIND, NULL);
        else if (!strcmp(mode, "namespace-umount")) result = syscall(SYS_umount2, "", 0);
        else if (!strcmp(mode, "namespace-move")) result = syscall(SYS_move_mount, -1, "", -1, "", 0);
        else if (!strcmp(mode, "namespace-mount-setattr")) result = syscall(SYS_mount_setattr, -1, "", 0, NULL, 0);
        else if (!strcmp(mode, "namespace-chroot")) result = syscall(SYS_chroot, "");
        else if (!strcmp(mode, "namespace-pivot")) result = syscall(SYS_pivot_root, "", "");
        else if (!strcmp(mode, "namespace-fsopen")) result = syscall(SYS_fsopen, "runlens-missing-filesystem", 0);
        else if (!strcmp(mode, "namespace-fsconfig")) result = syscall(SYS_fsconfig, -1, 0, NULL, NULL, 0);
        else if (!strcmp(mode, "namespace-fsmount")) result = syscall(SYS_fsmount, -1, 0, 0);
        else if (!strcmp(mode, "namespace-open-tree")) result = syscall(SYS_open_tree, -1, "", 0);
        else if (!strcmp(mode, "namespace-fspick")) result = syscall(SYS_fspick, -1, "", 0);
        else if (!strcmp(mode, "namespace-clone")) result = syscall(SYS_clone, CLONE_NEWNS | SIGCHLD, 0, 0, 0, 0);
        else if (!strcmp(mode, "namespace-clone3")) result = syscall(SYS_clone3, clone_args, sizeof(clone_args));
        else return 90;
        if (result < 0) printf("error=%d\n", errno);
        else {
            if (!strcmp(mode, "namespace-clone") || !strcmp(mode, "namespace-clone3")) {
                if (!result) _exit(0);
                int status = 0;
                if (waitpid(result, &status, 0) != result || status) return 91;
            }
            puts("success");
        }
        return 0;
    }
    if (argc == 3 && !strcmp(argv[1], "preload-child")) {
        const char *preload = getenv("LD_PRELOAD");
        printf("%s\n", preload ? preload : "absent");
        fflush(stdout);
        execl(argv[2], argv[2], (char *)NULL);
        return 90;
    }
    if (argc == 4 && !strcmp(argv[1], "file-handle")) {
        struct file_handle handle = {0};
        int mount = 0, dir = AT_FDCWD, flags = 0;
        char *parent = strdup(argv[2]);
        const char *name = argv[2];
        if (!strcmp(argv[3], "relative")) {
            char *leaf = strrchr(parent, '/');
            if (!leaf) return 90;
            *leaf = 0; name = leaf + 1;
            dir = open(parent, O_RDONLY | O_DIRECTORY);
        } else if (!strcmp(argv[3], "descriptor")) {
            dir = open(argv[2], O_PATH); name = ""; flags = AT_EMPTY_PATH;
        }
        long result = syscall(SYS_name_to_handle_at, dir, name, &handle, &mount, flags);
        if (result < 0) printf("error=%d\n", errno); else puts("resolved");
        if (dir >= 0) close(dir);
        free(parent); return 0;
    }
    if (argc == 4 && !strcmp(argv[1], "fanotify-watch")) {
        int fd = syscall(SYS_fanotify_init, FAN_CLOEXEC | FAN_NONBLOCK | FAN_REPORT_FID, O_RDONLY);
        int dir = AT_FDCWD;
        char *parent = strdup(argv[2]);
        const char *name = argv[2];
        if (!strcmp(argv[3], "relative")) {
            char *leaf = strrchr(parent, '/');
            if (!leaf) return 90;
            *leaf = 0;
            name = leaf + 1;
            dir = open(parent, O_RDONLY | O_DIRECTORY);
        } else if (!strcmp(argv[3], "descriptor")) {
            dir = open(argv[2], O_RDONLY);
            name = NULL;
        }
        long result = syscall(SYS_fanotify_mark, fd, FAN_MARK_ADD, (uint64_t)FAN_ACCESS, dir, name);
        if (result < 0) printf("error=%d\n", errno); else puts("registered");
        if (fd >= 0) close(fd);
        if (dir >= 0) close(dir);
        free(parent);
        return 0;
    }
    if (argc == 3 && !strcmp(argv[1], "inotify-watch")) {
        int fd = inotify_init1(IN_CLOEXEC | IN_NONBLOCK);
        if (fd < 0) return 90;
        long result = syscall(SYS_inotify_add_watch, fd, argv[2], IN_ACCESS);
        printf("%d\n", result >= 0 ? 0 : -1);
        close(fd);
        return 0;
    }
    if (argc == 2 && !strncmp(argv[1], "uring-", 6)) {
        /* Linux UAPI io_uring_params: 120 bytes, aligned to 8; flags at byte 8. */
        uint64_t params[15] = {0};
        if (!strcmp(argv[1], "uring-sqpoll")) params[1] = 2;
        long result;
        if (!strcmp(argv[1], "uring-enter")) result = syscall(SYS_io_uring_enter, -1, 0, 0, 0, 0, 0);
        else if (!strcmp(argv[1], "uring-register")) result = syscall(SYS_io_uring_register, -1, 0, 0, 0);
        else result = syscall(SYS_io_uring_setup, 2, params);
        if (result < 0) printf("error:%d\n", errno);
        else { close(result); puts("created"); }
        return 0;
    }
    if (argc == 4 && (!strcmp(argv[1], "chdir") || !strcmp(argv[1], "fchdir"))) {
        long result;
        if (!strcmp(argv[1], "chdir")) result = syscall(SYS_chdir, argv[2]);
        else {
            int fd = open(argv[3], O_RDONLY | O_DIRECTORY);
            if (fd < 0 || rename(argv[3], argv[2])) return 92;
            result = syscall(SYS_fchdir, fd);
            close(fd);
        }
        printf("%ld\n", result);
        return 0;
    }
    if (argc == 3 && (!strcmp(argv[1], "statfs") || !strcmp(argv[1], "fstatfs"))) {
        struct statfs stats;
        if (!strcmp(argv[1], "statfs")) syscall(SYS_statfs, argv[2], &stats);
        else { int fd = open(argv[2], O_WRONLY); syscall(SYS_fstatfs, fd, &stats); close(fd); }
        return 0;
    }
    if (argc == 3 && (!strcmp(argv[1], "detach-setsid") || !strcmp(argv[1], "detach-setpgid"))) {
        int output = open(argv[2], O_WRONLY | O_CREAT | O_TRUNC, 0600), ready[2];
        if (output < 0 || pipe(ready)) return 94;
        pid_t pid = fork();
        if (pid < 0) return 95;
        if (pid == 0) {
            close(ready[0]);
            long result = !strcmp(argv[1], "detach-setsid") ? syscall(SYS_setsid) : syscall(SYS_setpgid, 0, 0);
            if (result < 0) _exit(96);
            write(ready[1], "x", 1); close(ready[1]);
            close(0); close(1); close(2);
            usleep(200000); write(output, "done", 4); _exit(0);
        }
        close(ready[1]); char byte;
        return read(ready[0], &byte, 1) == 1 ? 0 : 97;
    }
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
