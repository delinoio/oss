// SPDX-License-Identifier: Apache-2.0
// Native stopped-group orphaning control; no pnport injection or dependencies.
#include <fcntl.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

static int signal_file = -1;
static void received(int signal) {
    unsigned char value = (unsigned char)signal;
    if (write(signal_file, &value, 1) != 1) _exit(90);
    _exit(0);
}
static void identity(const char *role) {
    char path[64];
    snprintf(path, sizeof(path), "%s.pid", role);
    FILE *file = fopen(path, "w");
    if (!file) _exit(91);
    fprintf(file, "%d", getpid());
    fclose(file);
    snprintf(path, sizeof(path), "%s.group", role);
    file = fopen(path, "w");
    if (!file) _exit(92);
    fprintf(file, "%d", getpgrp());
    fclose(file);
}
static void handler(const char *role) {
    char path[64];
    snprintf(path, sizeof(path), "%s.signal", role);
    signal_file = open(path, O_CREAT | O_TRUNC | O_WRONLY | O_CLOEXEC, 0600);
    if (signal_file < 0) _exit(93);
    struct sigaction action = {0};
    sigemptyset(&action.sa_mask);
    action.sa_handler = received;
    if (sigaction(SIGHUP, &action, NULL)) _exit(94);
}
int main(void) {
    alarm(15);
    identity("root");
    pid_t middle = fork();
    if (middle < 0) return 95;
    if (!middle) {
        if (setpgid(0, 0)) _exit(96);
        handler("middle");
        identity("middle");
        pid_t leaf = fork();
        if (leaf < 0) _exit(97);
        if (!leaf) {
            close(signal_file);
            handler("leaf");
            identity("leaf");
            raise(SIGSTOP);
            for (;;) pause();
        }
        int status;
        if (waitpid(leaf, &status, WUNTRACED) != leaf || !WIFSTOPPED(status)) _exit(98);
        raise(SIGSTOP);
        for (;;) pause();
    }
    int status;
    if (waitpid(middle, &status, WUNTRACED) != middle || !WIFSTOPPED(status)) return 99;
    // Parent exit makes the stopped middle/leaf group orphaned in the same
    // session. XNU orphanpg sends SIGHUP then SIGCONT independently of pnport.
    return 23;
}
