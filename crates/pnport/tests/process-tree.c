// SPDX-License-Identifier: Apache-2.0
// Synthetic process trees for signal, crash, lease and descendant conformance.
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <signal.h>
#include <spawn.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

static int signal_file = -1;
static void stopped(int signal) {
    unsigned char value = (unsigned char)signal;
    if (write(signal_file, &value, 1) != 1) _exit(90);
    _exit(0);
}

static int dependency(void) {
    char bytes[32] = {0};
    FILE *file = fopen("node_modules/dep/file.txt", "r");
    if (!file) return 40;
    size_t count = fread(bytes, 1, sizeof(bytes), file);
    fclose(file);
    return count == 13 && memcmp(bytes, "package bytes", 13) == 0 ? 0 : 41;
}

static char *terminal_line(char *line, size_t size) {
    char *result;
    do {
        clearerr(stdin);
        errno = 0;
        result = fgets(line, (int)size, stdin);
    } while (!result && errno == EINTR);
    return result;
}

static atomic_int readers_done;
static atomic_int reader_failed;
static void *reader(void *unused) {
    (void)unused;
    while (!atomic_load(&readers_done)) {
        if (dependency()) atomic_store(&reader_failed, 1);
    }
    return NULL;
}

static void fork_callback(void) {
    // The preload's child callback must unlock its state before user callbacks.
    if (dependency()) _exit(52);
}

static int concurrent_fork(void) {
    if (pthread_atfork(NULL, NULL, fork_callback)) return 53;
    pthread_t readers[4];
    for (int index = 0; index < 4; index++)
        if (pthread_create(&readers[index], NULL, reader, NULL)) return 54;
    for (int index = 0; index < 100; index++) {
        pid_t child = fork();
        if (child < 0) return 55;
        if (!child) _exit(dependency());
        int status;
        if (waitpid(child, &status, 0) != child || !WIFEXITED(status) || WEXITSTATUS(status))
            return 56;
    }
    atomic_store(&readers_done, 1);
    for (int index = 0; index < 4; index++)
        if (pthread_join(readers[index], NULL)) return 57;
    return atomic_load(&reader_failed) ? 58 : 0;
}

int main(int argc, char **argv) {
    if (argc != 3) return 42;
    const char *role = argv[1], *mode = argv[2];
    if (strcmp(role, "root") != 0) {
        const char *value = getenv("PNPORT_TEST_ENV");
        if (!value || strcmp(value, "replacement") != 0) return 43;
        if (strcmp(mode, "spawnp") == 0) {
            sigset_t mask;
            if (sigprocmask(SIG_SETMASK, NULL, &mask) || !sigismember(&mask, SIGUSR1)) return 64;
            value = getenv("PATH");
            if (!value || strcmp(value, "/absent-child-path") != 0) return 65;
            if (write(9, "1", 1) != 1) return 74;
        }
    }
    int result = dependency();
    if (result) return result;
    if (strcmp(mode, "terminal") == 0 || strcmp(mode, "terminal-stop") == 0) {
        FILE *marker = fopen("root.group", "w");
        if (!marker) return 67;
        fprintf(marker, "%d", getpgrp());
        fclose(marker);
        marker = fopen("root.pid", "w");
        if (!marker) return 68;
        fprintf(marker, "%d", getpid());
        fclose(marker);
        char line[32];
        // A background read must produce SIGTTIN before the shell foregrounds us.
        if (!terminal_line(line, sizeof(line)) || strcmp(line, "first\n")) return 69;
        if (tcgetpgrp(0) != getpgrp()) return 70;
        marker = fopen("terminal.first", "w");
        if (!marker) return 71;
        fputs("1", marker);
        fclose(marker);
        if (strcmp(mode, "terminal-stop") == 0) raise(SIGSTOP);
        if (!terminal_line(line, sizeof(line)) || strcmp(line, "second\n")) return 72;
        return dependency() ? 73 : 23;
    }
    if (strcmp(mode, "fork-stress") == 0) {
        FILE *group = fopen("root.group", "w");
        if (!group) return 66;
        fprintf(group, "%d", getpgrp());
        fclose(group);
        return concurrent_fork();
    }
    if (strcmp(role, "middle") == 0 && strcmp(mode, "detached") == 0 && setsid() < 0)
        return 44;
    char name[64];
    snprintf(name, sizeof(name), "%s.signal", role);
    signal_file = open(name, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
    if (signal_file < 0) return 45;
    struct sigaction action = {0};
    sigemptyset(&action.sa_mask);
    action.sa_handler = strcmp(mode, "ignore") == 0 ? SIG_IGN : stopped;
    for (int index = 0; index < 3; index++) {
        int signals[] = {SIGINT, SIGTERM, SIGHUP};
        if (sigaction(signals[index], &action, NULL) != 0) return 46;
    }
    if (strcmp(role, "leaf") != 0) {
        char *args[] = {argv[0], strcmp(role, "root") == 0 ? "middle" : "leaf", argv[2], NULL};
        char *environment[] = {"PNPORT_TEST_ENV=replacement", "PATH=/absent-child-path", NULL};
        pid_t child;
        if (strcmp(mode, "spawnp") == 0) {
            char cwd[4096], path[16384];
            if (!getcwd(cwd, sizeof(cwd))) return 59;
            int length = snprintf(path, sizeof(path), "%s/node_modules/dep/missing:%s/node_modules/dep/blocked:%s/node_modules/dep/bin", cwd, cwd, cwd);
            if (length < 0 || (size_t)length >= sizeof(path)) return 60;
            if (setenv("PATH", path, 1)) return 61;
            if (posix_spawnp(&child, "absent", NULL, NULL, args, environment) != ENOENT ||
                posix_spawnp(&child, "noexec", NULL, NULL, args, environment) != EACCES) return 75;
            // Absolute PATH candidates retain opaque actions and attributes.
            posix_spawn_file_actions_t actions;
            posix_spawnattr_t attributes;
            sigset_t mask;
            sigemptyset(&mask);
            sigaddset(&mask, SIGUSR1);
            if (posix_spawn_file_actions_init(&actions) || posix_spawnattr_init(&attributes) ||
                posix_spawn_file_actions_addopen(&actions, 9, "spawn-output", O_WRONLY | O_CREAT | O_APPEND, 0600) ||
                posix_spawnattr_setsigmask(&attributes, &mask) ||
                posix_spawnattr_setflags(&attributes, POSIX_SPAWN_SETSIGMASK)) return 62;
            result = posix_spawnp(&child, "tree", &actions, &attributes, args, environment);
            posix_spawnattr_destroy(&attributes);
            posix_spawn_file_actions_destroy(&actions);
            if (result) return 63;
        } else {
            child = fork();
            if (child < 0) return 47;
        }
        if (child == 0) {
            execve(argv[0], args, environment);
            _exit(48);
        }
    }
    snprintf(name, sizeof(name), "%s.group", role);
    FILE *marker = fopen(name, "w");
    if (!marker) return 51;
    fprintf(marker, "%d", getpgrp());
    fclose(marker);
    snprintf(name, sizeof(name), "%s.pid", role);
    marker = fopen(name, "w");
    if (!marker) return 49;
    fprintf(marker, "%d", getpid());
    fclose(marker);
    if (strcmp(role, "root") == 0 && strcmp(mode, "exit") == 0) {
        struct stat info;
        for (int attempt = 0; attempt < 1000; attempt++) {
            if (stat("leaf.pid", &info) == 0 && info.st_size > 0) return 23;
            usleep(10000);
        }
        return 50;
    }
    for (;;) pause();
}
