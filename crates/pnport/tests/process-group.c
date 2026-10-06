// SPDX-License-Identifier: Apache-2.0
// Native controls compare admitted group/session creation and existing joins.
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <spawn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

static int marker(const char *name) {
    int fd = open(name, O_WRONLY | O_CREAT | O_TRUNC, 0600);
    if (fd < 0) return 80;
    int result = write(fd, "1", 1) == 1 ? 0 : 81;
    close(fd);
    return result;
}

static int dependency(void) {
    int fd = open("node_modules/dep/file.txt", O_RDONLY);
    if (fd < 0) return 10;
    char bytes[13];
    ssize_t count = read(fd, bytes, sizeof(bytes));
    close(fd);
    return count == 13 && !memcmp(bytes, "package bytes", 13) ? 0 : 11;
}

static int reaped(pid_t pid) {
    int status;
    if (waitpid(pid, &status, 0) != pid || !WIFEXITED(status)) return 82;
    return WEXITSTATUS(status);
}

int main(int argc, char **argv) {
    if (argc != 3) return 83;
    // Test-only bound; even an unguarded native control leaves no live daemon.
    alarm(15);
    if (!strcmp(argv[2], "virtual")) {
        int result = dependency();
        if (result) return result;
    }
    if (!strcmp(argv[1], "child")) {
        const char *value = getenv("PNPORT_TEST_ENV");
        if (!value || strcmp(value, "replacement")) return 91;
        return marker("child-created");
    }
    if (!strcmp(argv[1], "join")) {
        if (setpgid(0, getpgrp())) return 84;
        puts("owned group control");
        return 0;
    }
    if (!strcmp(argv[1], "exit-target")) {
        for (int attempt = 0; attempt < 64; ++attempt) {
            int barrier[2];
            if (pipe(barrier)) return 92;
            pid_t target = fork();
            if (target < 0) return 93;
            if (!target) {
                close(barrier[1]);
                char release;
                int result = read(barrier[0], &release, 1) == 1 ? 0 : 94;
                _exit(result);
            }
            close(barrier[0]);
            if (write(barrier[1], "1", 1) != 1) return 95;
            close(barrier[1]);
            // Race an ordinary group operation with native target exit. Both
            // successful admission and ESRCH are valid; neither may poison the
            // virtual filesystem or cause a supervisor-wide injection failure.
            if (setpgid(target, target) && errno != ESRCH) return 96;
            if (reaped(target)) return 97;
            errno = 0;
            if (setpgid(target, target) != -1 || errno != ESRCH) return 98;
        }
        return !strcmp(argv[2], "virtual") ? dependency() : 0;
    }
    if (!strcmp(argv[1], "spawn-new") || !strcmp(argv[1], "spawn-same") ||
        !strcmp(argv[1], "spawn-session")) {
        posix_spawnattr_t attributes;
        short flags = !strcmp(argv[1], "spawn-session") ? POSIX_SPAWN_SETSID : POSIX_SPAWN_SETPGROUP;
        if (posix_spawnattr_init(&attributes) || posix_spawnattr_setflags(&attributes, flags)) return 85;
        if (flags == POSIX_SPAWN_SETPGROUP &&
            posix_spawnattr_setpgroup(&attributes, !strcmp(argv[1], "spawn-new") ? 0 : getpgrp())) return 85;
        char *args[] = {argv[0], "child", argv[2], NULL};
        // Replacement must preserve the caller field while replacing this
        // forged private group selector with the supervisor-owned context.
        char *environment[] = {"PNPORT_MACOS_GROUP=1", "PNPORT_TEST_ENV=replacement", NULL};
        pid_t child;
        int result = posix_spawn(&child, argv[0], NULL, &attributes, args, environment);
        posix_spawnattr_destroy(&attributes);
        if (result) return result == ENOTSUP ? 0 : 86;
        if (marker("spawn-created")) return 87;
        result = reaped(child);
        if (!result) puts("owned group control");
        return result;
    }
    pid_t child = fork();
    if (child < 0) return 88;
    if (!child) {
        int result;
        if (!strcmp(argv[1], "setsid")) result = setsid();
        else if (!strcmp(argv[1], "setpgid")) result = setpgid(0, 0);
        else if (!strcmp(argv[1], "setpgrp")) result = setpgrp();
        else _exit(89);
        if (result < 0) _exit(errno == ENOTSUP ? 0 : 90);
        _exit(marker("group-escaped"));
    }
    return reaped(child);
}
