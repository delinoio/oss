// SPDX-License-Identifier: Apache-2.0
// A test-only shell boundary on an isolated controlling terminal.
#include <signal.h>
#include <fcntl.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

static void marker(const char *name) {
    FILE *file = fopen(name, "w");
    if (!file) _exit(70);
    fputs("1", file);
    fclose(file);
}

int main(int argc, char **argv) {
    if (argc != 4) return 71;
    marker("terminal.driver");
    signal(SIGTTOU, SIG_IGN);
    int input[2] = {-1, -1};
    int redirected = strcmp(argv[3], "redirected") == 0;
    if (redirected && pipe(input)) return 85;
    const char *command_mode = redirected ? "terminal-pipe" :
        !strcmp(argv[3], "detached-interrupt") ? "terminal-detached" :
        !strcmp(argv[3], "new-group") ? "terminal-group" :
        !strcmp(argv[3], "pending-pause") ? "terminal-pending-pause" :
        !strcmp(argv[3], "self-stop") ? "terminal-stop" : "terminal";
    int launch[2];
    if (pipe(launch)) return 83;
    pid_t child = fork();
    if (child < 0) return 72;
    if (!child) {
        signal(SIGTTOU, SIG_DFL);
        signal(SIGTTIN, SIG_DFL);
        signal(SIGTSTP, SIG_DFL);
        signal(SIGCONT, SIG_DFL);
        close(launch[1]);
        if (redirected) {
            close(input[1]);
            if (dup2(input[0], 0) < 0) _exit(86);
            close(input[0]);
        }
        // The shell parent alone places this child in its group before
        // releasing launch. The duplicate parent/child setpgid path had
        // intermittent pre-exec failures on Darwin (child exit 73), which
        // were previously indistinguishable from pnport startup failure.
        // Keep this barrier instead of restoring the duplicate group change.
        char ready;
        if (read(launch[0], &ready, 1) != 1) _exit(84);
        close(launch[0]);
        if (getpgrp() != getpid()) _exit(90);
        // Optional test-only diagnostics retain real tty stdin/stdout while
        // avoiding an unread PTY buffer blocking explicit supervisor logs.
        // Default conformance still inherits all three terminal streams.
        if (getenv("PNPORT_TEST_TERMINAL_DIAGNOSTICS")) {
            int log = open("terminal.diagnostics", O_CREAT | O_WRONLY | O_TRUNC, 0600);
            if (log < 0 || dup2(log, STDERR_FILENO) < 0) _exit(88);
            close(log);
            execl(argv[1], argv[1], "--log-level", "debug", "--cache-dir", "store", "run", "--", argv[2],
                  "root", command_mode, (char *)NULL);
            _exit(74);
        }
        execl(argv[1], argv[1], "--cache-dir", "store", "run", "--", argv[2],
              "root", command_mode, (char *)NULL);
        _exit(74);
    }
    int status;
    FILE *supervisor = fopen("terminal.supervisor", "w");
    if (!supervisor) return 89;
    fprintf(supervisor, "%d", child);
    fclose(supervisor);
    if (redirected) {
        close(input[0]);
        if (write(input[1], "first\nsecond\n", 13) != 13) return 87;
        close(input[1]);
    }
    close(launch[0]);
    if (setpgid(child, child)) return 75;
    marker("terminal.launching");
    if (strcmp(argv[3], "background") != 0 && tcsetpgrp(0, child)) return 76;
    if (write(launch[1], "1", 1) != 1) return 77;
    close(launch[1]);
    marker("terminal.running");
    if (strcmp(argv[3], "interrupt") != 0 && strcmp(argv[3], "detached-interrupt") != 0 &&
        strcmp(argv[3], "missing-image") != 0 &&
        strcmp(argv[3], "invalid-image") != 0 && !redirected) {
        pid_t waited = waitpid(child, &status, WUNTRACED);
        if (waited != child || !WIFSTOPPED(status)) {
            int wait_error = waited < 0 ? errno : 0;
            FILE *outcome = fopen("terminal.wait", "w");
            if (outcome) {
                fprintf(outcome, "%d %d %d\n", wait_error,
                        waited == child && WIFEXITED(status) ? WEXITSTATUS(status) : -1,
                        waited == child && WIFSIGNALED(status) ? WTERMSIG(status) : -1);
                fclose(outcome);
            }
            return 78;
        }
        pid_t expected = strcmp(argv[3], "background") == 0 ? getpgrp() : child;
        if (tcgetpgrp(0) != expected) return 79;
        marker("terminal.stopped");
        if (!strcmp(argv[3], "pending-pause")) sleep(6);
        if (tcsetpgrp(0, child) || kill(-child, SIGCONT)) return 80;
    }
    if (waitpid(child, &status, 0) != child || !WIFEXITED(status)) return 81;
    // pnport must return the terminal to its caller group before completion.
    if (tcgetpgrp(0) != child || tcsetpgrp(0, getpgrp())) return 82;
    marker("terminal.restored");
    return WEXITSTATUS(status);
}
