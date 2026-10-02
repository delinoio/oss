// SPDX-License-Identifier: Apache-2.0
// Synthetic process trees for signal, crash, lease and descendant conformance.
#define _GNU_SOURCE
#include <fcntl.h>
#include <signal.h>
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

int main(int argc, char **argv) {
    if (argc != 3) return 42;
    const char *role = argv[1], *mode = argv[2];
    if (strcmp(role, "root") != 0) {
        const char *value = getenv("PNPORT_TEST_ENV");
        if (!value || strcmp(value, "replacement") != 0) return 43;
    }
    int result = dependency();
    if (result) return result;
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
        pid_t child = fork();
        if (child < 0) return 47;
        if (child == 0) {
            char *args[] = {argv[0], strcmp(role, "root") == 0 ? "middle" : "leaf", argv[2], NULL};
            char *environment[] = {"PNPORT_TEST_ENV=replacement", NULL};
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
